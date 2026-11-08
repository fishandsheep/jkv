package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/store"
)

func cmdClean(ctx context.Context, s *store.Store, args []string) error {
	dryRun := false
	var olderThan time.Duration
	var positional []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--dry-run":
			dryRun = true
		case arg == "--older-than":
			if index+1 >= len(args) {
				return usageErrorf("--older-than 缺少时长")
			}
			index++
			parsed, err := time.ParseDuration(args[index])
			if err != nil || parsed <= 0 {
				return usageErrorf("无效缓存年龄 %q", args[index])
			}
			olderThan = parsed
		case strings.HasPrefix(arg, "--older-than="):
			parsed, err := time.ParseDuration(strings.TrimPrefix(arg, "--older-than="))
			if err != nil || parsed <= 0 {
				return usageErrorf("无效缓存年龄 %q", strings.TrimPrefix(arg, "--older-than="))
			}
			olderThan = parsed
		default:
			positional = append(positional, arg)
		}
	}
	args = positional
	if len(args) > 3 {
		return usageErrorf("用法: jkv clean [--dry-run] | jkv clean downloads [工具] [版本] | jkv clean partials --older-than 24h；详见 jkv clean --help")
	}
	kind, candidate, version := "", "", ""
	if len(args) > 0 {
		kind = args[0]
	}
	if kind != "" && kind != "downloads" && kind != "partials" {
		return usageErrorf("不支持缓存类型 %q", kind)
	}
	if len(args) > 1 {
		candidate = args[1]
		if !catalog.IsCandidate(candidate) {
			return usageErrorf("不支持工具 %q", candidate)
		}
	}
	if len(args) > 2 {
		if kind != "downloads" && kind != "partials" {
			return usageErrorf("此缓存类型不支持 version 参数")
		}
		version = args[2]
	}
	if kind == "partials" && olderThan <= 0 {
		return usageErrorf("清理中断下载必须指定 --older-than")
	}
	var result store.CleanResult
	var err error
	if kind == "partials" && dryRun {
		result, err = s.InspectPartialsOlderThan(candidate, version, olderThan)
	} else if kind == "partials" {
		result, err = s.CleanPartialsOlderThan(candidate, version, olderThan)
	} else if dryRun && olderThan > 0 {
		result, err = s.InspectCacheOlderThan(kind, candidate, version, olderThan)
	} else if dryRun {
		result, err = s.InspectCache(kind, candidate, version)
	} else if olderThan > 0 {
		result, err = s.CleanCacheOlderThan(kind, candidate, version, olderThan)
	} else {
		result, err = s.CleanCache(kind, candidate, version)
	}
	if err != nil {
		return err
	}
	if optionsFromContext(ctx).JSON {
		return writeJSON(map[string]any{"dry_run": dryRun, "older_than": olderThan.String(), "files": result.Files, "bytes": result.Bytes})
	}
	if dryRun {
		fmt.Printf("将清理 %d 个文件，释放 %s\n", result.Files, formatBytes(result.Bytes))
		return nil
	}
	fmt.Printf("已清理 %d 个文件，释放 %s\n", result.Files, formatBytes(result.Bytes))
	return nil
}

func formatBytes(n int64) string {
	const unit = int64(1024)
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	for _, suffix := range units {
		value /= 1024
		if value < 1024 || suffix == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%d B", n)
}
