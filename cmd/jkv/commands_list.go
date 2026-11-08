package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/store"
)

func cmdList(ctx context.Context, s *store.Store, args []string) error {
	refresh := false
	var positional []string
	for _, arg := range args {
		if arg == "--refresh" {
			refresh = true
		} else {
			positional = append(positional, arg)
		}
	}
	if len(positional) == 0 {
		candidates := catalog.Candidates
		if snapshot, err := loadSignedSnapshot(ctx, s, refresh, optionsFromContext(ctx).Quiet); err == nil {
			candidates = snapshotCandidateList(snapshot)
		} else if !optionsFromContext(ctx).Quiet {
			fmt.Fprintf(os.Stderr, "签名 catalog 不可用，显示内置工具列表: %v\n", err)
		}
		if optionsFromContext(ctx).JSON {
			return writeJSON(candidates)
		}
		fmt.Printf("%s %s %s %s\n", padRight("工具", 12), padRight("说明", 36), padRight("国内源", 22), "平台")
		for _, c := range candidates {
			fmt.Printf("%s %s %s %s\n", padRight(c.Name, 12), padRight(c.Description, 36), padRight(c.Source, 22), c.Platforms)
		}
		return nil
	}
	if len(positional) != 1 {
		return usageErrorf("用法: jkv list [工具] [--refresh]")
	}
	candidate := positional[0]
	releases, err := loadReleases(ctx, s, candidate, refresh, false)
	if err != nil {
		return err
	}
	installed, installedErr := s.Installed(candidate)
	if installedErr != nil {
		return installedErr
	}
	defaults, _ := s.Defaults()
	installedSet := map[string]bool{}
	for _, v := range installed {
		installedSet[v] = true
	}
	if len(releases) == 0 && len(installed) == 0 {
		return notFoundErrorf("当前平台 %s/%s 暂无稳定国内源", runtime.GOOS, runtime.GOARCH)
	}
	listed := mergeInstalledReleases(s, candidate, releases, installed)
	if optionsFromContext(ctx).JSON {
		type listedRelease struct {
			catalog.Release
			Installed bool `json:"installed"`
			Default   bool `json:"default"`
			Current   bool `json:"current"`
			InCatalog bool `json:"in_catalog"`
		}
		out := make([]listedRelease, 0, len(listed))
		for _, release := range listed {
			out = append(out, listedRelease{
				Release:   release,
				Installed: installedSet[release.Version],
				Default:   defaults[candidate] == release.Version,
				Current:   os.Getenv(currentVar(candidate)) == release.Version,
				InCatalog: release.AvailabilityStatus != "installed-only",
			})
		}
		return writeJSON(out)
	}
	verbose := optionsFromContext(ctx).Verbose
	groups := releaseGroups(candidate, listed)
	for groupIndex, group := range groups {
		if groupIndex > 0 {
			fmt.Println()
		}
		fmt.Println(vendorDisplay(group.vendor))
		fmt.Println(strings.Repeat("-", 100))
		if verbose {
			fmt.Printf("%-32s %-11s %-8s %-11s %-10s %s\n", "VERSION", "STATUS", "TIER", "INTEGRITY", "AVAILABLE", "SOURCE")
		} else {
			fmt.Printf("%-36s %-11s %s\n", "VERSION", "STATUS", "AVAILABLE")
		}
		for _, r := range group.releases {
			status := ""
			if installedSet[r.Version] {
				status = "installed"
			}
			if defaults[candidate] == r.Version {
				status = "default"
			}
			if os.Getenv(currentVar(candidate)) == r.Version {
				status = "current"
			}
			available := "×"
			source := hostOf(r.URL)
			if r.AvailabilityStatus == "installed-only" {
				available = "-"
				source = "local"
			} else if r.Available {
				available = "√"
			}
			if verbose {
				fmt.Printf("%-32s %-11s %-8s %-11s %-10s %s\n",
					r.Version, status, r.SupportTier, r.IntegrityLevel, available, source)
			} else {
				fmt.Printf("%-36s %-11s %s\n", r.Version, status, available)
			}
		}
	}
	return nil
}

func mergeInstalledReleases(s *store.Store, candidate string, releases []catalog.Release, installed []string) []catalog.Release {
	out := append([]catalog.Release(nil), releases...)
	inCatalog := make(map[string]bool, len(releases))
	for _, release := range releases {
		inCatalog[release.Version] = true
	}
	var local []catalog.Release
	for _, installedVersion := range installed {
		if inCatalog[installedVersion] {
			continue
		}
		release, err := s.InstalledRelease(candidate, installedVersion)
		if err != nil {
			release = catalog.Release{Candidate: candidate, Version: installedVersion, Vendor: "local", SupportTier: "local", IntegrityLevel: "unknown"}
		}
		release.Candidate = candidate
		release.Version = installedVersion
		if release.Vendor == "" {
			release.Vendor = "local"
		}
		if release.SupportTier == "" {
			release.SupportTier = "local"
		}
		if release.IntegrityLevel == "" {
			release.IntegrityLevel = "unknown"
		}
		release.URL = ""
		release.Available = false
		release.AvailabilityKnown = false
		release.AvailabilityStatus = "installed-only"
		local = append(local, release)
	}
	sort.SliceStable(local, func(i, j int) bool {
		return catalog.VersionLess(local[j].Version, local[i].Version)
	})
	return append(out, local...)
}

type releaseGroup struct {
	vendor   string
	releases []catalog.Release
}

func releaseGroups(candidate string, releases []catalog.Release) []releaseGroup {
	byVendor := map[string][]catalog.Release{}
	for _, release := range releases {
		byVendor[release.Vendor] = append(byVendor[release.Vendor], release)
	}
	var order []string
	if candidate == "java" {
		order = []string{"temurin", "dragonwell", "bisheng"}
		known := map[string]bool{"temurin": true, "dragonwell": true, "bisheng": true}
		for _, vendor := range sortedKeys(byVendor) {
			if !known[vendor] {
				order = append(order, vendor)
			}
		}
	} else {
		order = sortedKeys(byVendor)
	}
	var groups []releaseGroup
	for _, vendor := range order {
		if len(byVendor[vendor]) > 0 {
			groups = append(groups, releaseGroup{vendor: vendor, releases: byVendor[vendor]})
		}
	}
	return groups
}

func vendorDisplay(vendor string) string {
	if display := map[string]string{"temurin": "Temurin", "dragonwell": "Alibaba Dragonwell", "bisheng": "Huawei BiSheng", "local": "本地安装"}[vendor]; display != "" {
		return display
	}
	return vendor
}
