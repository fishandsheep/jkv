package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/store"
)

var commandAliases = map[string]string{
	"list": "list", "ls": "list",
	"install": "install", "i": "install",
	"repair": "repair",
	"use":    "use", "u": "use",
	"default": "default", "d": "default",
	"current": "current", "c": "current",
	"uninstall": "uninstall", "rm": "uninstall",
	"self": "self", "s": "self",
	"home": "home", "h": "home",
	"env": "env", "e": "env",
	"init": "init", "in": "init",
	"mirror": "mirror", "m": "mirror",
	"clean": "clean", "cl": "clean",
	"doctor":  "doctor",
	"version": "version", "v": "version", "--version": "version", "-v": "version",
	"help": "help", "--help": "help", "-h": "help",
}

var topLevelCompletions = []string{
	"list", "ls", "install", "i", "repair", "use", "u", "default", "d", "current", "c",
	"uninstall", "rm", "home", "h", "env", "e", "init", "in", "mirror", "m",
	"self", "s",
	"clean", "cl", "version", "v", "--version", "-v", "help", "--help", "-h",
	"doctor",
}

func cmdComplete(ctx context.Context, s *store.Store, args []string) error {
	for _, completion := range completions(ctx, s, args) {
		fmt.Println(completion)
	}
	return nil
}

func completions(ctx context.Context, s *store.Store, args []string) []string {
	if len(args) == 0 {
		return topLevelCompletions
	}
	prefix := args[len(args)-1]
	if len(args) == 1 {
		return matching(topLevelCompletions, prefix)
	}
	command := commandAliases[args[0]]
	if command == "" {
		return nil
	}
	candidates := candidateNames()
	completed := args[1 : len(args)-1]
	switch command {
	case "list":
		options := remainingOptions(completed, "--refresh")
		positionals := omitOptions(completed, "--refresh")
		if len(positionals) == 0 {
			return matching(append(candidates, options...), prefix)
		}
		if len(positionals) == 1 {
			return matching(options, prefix)
		}
	case "install":
		options := remainingOptions(completed, "--default", "--require-checksum")
		positionals := omitOptions(completed, "--default", "--require-checksum")
		if len(positionals) == 0 {
			return matching(append(candidates, options...), prefix)
		}
		if len(positionals) == 1 && catalog.IsCandidate(positionals[0]) {
			return matching(append(installVersions(ctx, s, positionals[0]), options...), prefix)
		}
		if len(positionals) == 2 {
			return matching(options, prefix)
		}
	case "use", "default", "uninstall", "home", "repair":
		if len(args) == 2 {
			return matching(candidates, prefix)
		}
		if len(args) == 3 && catalog.IsCandidate(args[1]) {
			versions, _ := s.Installed(args[1])
			return matching(versions, prefix)
		}
	case "current":
		if len(args) == 2 {
			return matching(candidates, prefix)
		}
	case "self":
		if len(args) == 2 {
			return matching([]string{"update", "up", "uninstall", "rm"}, prefix)
		}
		if len(args) >= 3 && (args[1] == "uninstall" || args[1] == "rm") {
			return matching(remainingOptions(completed[1:], "--purge", "--yes"), prefix)
		}
	case "env":
		if len(args) == 2 {
			return matching([]string{"init", "apply", "clear", "defaults"}, prefix)
		}
	case "init":
		if len(args) == 2 {
			return matching([]string{"bash", "zsh", "fish", "powershell", "pwsh"}, prefix)
		}
	case "mirror":
		if len(args) == 2 {
			return matching([]string{"maven", "gradle", "status"}, prefix)
		}
		if len(args) == 3 && (args[1] == "maven" || args[1] == "gradle") {
			return matching([]string{"--apply"}, prefix)
		}
	case "clean":
		if len(args) == 2 {
			return matching([]string{"downloads", "catalog", "partials", "--dry-run", "--older-than"}, prefix)
		}
		if len(args) == 3 && (args[1] == "downloads" || args[1] == "catalog" || args[1] == "partials") {
			return matching(candidates, prefix)
		}
		if len(args) == 4 && (args[1] == "downloads" || args[1] == "partials") && catalog.IsCandidate(args[2]) {
			return matching(s.CachedVersions(args[2]), prefix)
		}
	}
	return nil
}

func installVersions(ctx context.Context, s *store.Store, candidate string) []string {
	versions := append([]string{"latest"}, s.CachedVersions(candidate)...)
	if releases, err := loadReleases(ctx, s, candidate, false, false, true); err == nil {
		for _, release := range releases {
			versions = append(versions, release.Version)
		}
	}
	return uniqueStrings(versions)
}

func remainingOptions(completed []string, options ...string) []string {
	var remaining []string
	for _, option := range options {
		found := false
		for _, value := range completed {
			if value == option {
				found = true
				break
			}
		}
		if !found {
			remaining = append(remaining, option)
		}
	}
	return remaining
}

func omitOptions(values []string, options ...string) []string {
	var positionals []string
	for _, value := range values {
		isOption := false
		for _, option := range options {
			if value == option {
				isOption = true
				break
			}
		}
		if !isOption {
			positionals = append(positionals, value)
		}
	}
	return positionals
}

func candidateNames() []string {
	names := make([]string, 0, len(catalog.Candidates))
	for _, candidate := range catalog.Candidates {
		names = append(names, candidate.Name)
	}
	return names
}

func matching(values []string, prefix string) []string {
	var matches []string
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			matches = append(matches, value)
		}
	}
	return uniqueStrings(matches)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			unique = append(unique, value)
		}
	}
	sort.Strings(unique)
	return unique
}
