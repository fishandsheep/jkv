package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/store"
)

func cmdInstall(ctx context.Context, s *store.Store, args []string) error {
	setDefault := false
	requireChecksum := envEnabled("JKV_REQUIRE_CHECKSUM")
	var pos []string
	for _, arg := range args {
		if arg == "--default" {
			setDefault = true
			continue
		}
		if arg == "--require-checksum" {
			requireChecksum = true
			continue
		}
		if strings.HasPrefix(arg, "--") {
			return usageErrorf("install 不支持选项 %q", arg)
		}
		pos = append(pos, arg)
	}
	if len(pos) < 1 || len(pos) > 2 {
		return usageErrorf("用法: jkv install <工具> [版本] [--default]")
	}
	candidate := pos[0]
	want := ""
	if len(pos) == 2 {
		want = pos[1]
	}
	var r catalog.Release
	var err error
	foundCached := false
	if want != "" && want != "latest" {
		r, foundCached = s.CachedRelease(candidate, want)
	}
	if !foundCached {
		fmt.Fprintln(os.Stderr, "解析国内镜像版本...")
		releases, loadErr := loadReleases(ctx, s, candidate, false, false)
		if loadErr != nil {
			return loadErr
		}
		r, err = selectRelease(releases, want)
		if err != nil {
			return err
		}
	}
	if err := rejectRevokedArtifact(s, r); err != nil {
		return err
	}
	options := optionsFromContext(ctx)
	if r.ChecksumURL == "" && r.ChecksumValue == "" {
		fmt.Fprintln(os.Stderr, "警告: 此镜像未提供同源 SHA-256；下载仅由 HTTPS 保护。")
	}
	if !options.Quiet {
		fmt.Fprintf(os.Stderr, "安装 %s %s，来源 %s\n", candidate, r.Version, hostOf(r.URL))
	}
	var progress io.Writer = os.Stderr
	if options.Quiet || !isTerminal(os.Stderr) {
		progress = nil
	}
	if err := s.InstallWithOptions(ctx, r, progress, store.InstallOptions{RequireChecksum: requireChecksum}); err != nil {
		return err
	}
	defaults, _ := s.Defaults()
	if setDefault || defaults[candidate] == "" {
		if err := s.SetDefault(candidate, r.Version); err != nil {
			return err
		}
		fmt.Printf("已安装并设为默认: %s %s\n", candidate, r.Version)
	} else {
		fmt.Printf("已安装: %s %s\n", candidate, r.Version)
	}
	return nil
}

func cmdRepair(ctx context.Context, s *store.Store, args []string) error {
	if len(args) != 2 {
		return usageErrorf("用法: jkv repair <工具> <版本>")
	}
	version, err := installedMatch(s, args[0], args[1])
	if err != nil {
		return err
	}
	release, err := s.InstalledRelease(args[0], version)
	if err != nil {
		return err
	}
	if err := rejectRevokedArtifact(s, release); err != nil {
		return err
	}
	var progress io.Writer = os.Stderr
	if optionsFromContext(ctx).Quiet || !isTerminal(os.Stderr) {
		progress = nil
	}
	if err := s.InstallWithOptions(ctx, release, progress, store.InstallOptions{
		Repair:          true,
		RequireChecksum: envEnabled("JKV_REQUIRE_CHECKSUM"),
	}); err != nil {
		return err
	}
	fmt.Printf("已修复: %s %s\n", args[0], version)
	return nil
}

func rejectRevokedArtifact(s *store.Store, release catalog.Release) error {
	revocation, revoked, err := s.ArtifactRevocation(release.ArtifactID)
	if err != nil {
		return stateErrorf("读取 artifact 撤销状态: %w", err)
	}
	if !revoked {
		return nil
	}
	message := revocation.Reason
	if revocation.Message != "" {
		message = revocation.Message
	}
	return integrityErrorf("制品 %s 已撤销：%s", release.ArtifactID, message)
}

func selectRelease(releases []catalog.Release, want string) (catalog.Release, error) {
	available := make([]catalog.Release, 0, len(releases))
	for _, release := range releases {
		if !release.AvailabilityKnown || release.Available {
			available = append(available, release)
		}
	}
	releases = available
	if len(releases) == 0 {
		return catalog.Release{}, notFoundErrorf("当前平台无可用稳定版本")
	}
	if want == "" || want == "latest" {
		for _, r := range releases {
			if r.Candidate != "java" || r.Vendor == "temurin" {
				return r, nil
			}
		}
		return releases[0], nil
	}
	for _, r := range releases {
		if r.Version == want {
			return r, nil
		}
	}
	vendor := ""
	prefix := want
	aliases := map[string]string{"tem": "temurin", "temurin": "temurin", "dragonwell": "dragonwell", "albba": "dragonwell", "bisheng": "bisheng"}
	for suffix, v := range aliases {
		if strings.HasSuffix(want, "-"+suffix) {
			vendor = v
			prefix = strings.TrimSuffix(want, "-"+suffix)
			break
		}
	}
	var matches []catalog.Release
	for _, r := range releases {
		base := strings.Split(r.Version, "-")[0]
		if (base == prefix || strings.HasPrefix(base, prefix+".")) && (vendor == "" || r.Vendor == vendor) {
			matches = append(matches, r)
		}
	}
	if len(matches) > 0 {
		// 不带发行商后缀时，多个发行商命中同一主版本，优先 Temurin，
		// 与 latest 的选择规则一致。
		if vendor == "" {
			for _, r := range matches {
				if r.Vendor == "temurin" {
					return r, nil
				}
			}
		}
		return matches[0], nil
	}
	return catalog.Release{}, notFoundErrorf("未找到版本 %q；先运行 jkv list %s", want, releases[0].Candidate)
}
