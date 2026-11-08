package main

import (
	"context"
	"fmt"
	"os"

	"github.com/fishandsheep/jkv/internal/selfmanage"
	"github.com/fishandsheep/jkv/internal/store"
)

func cmdSelf(ctx context.Context, s *store.Store, args []string) error {
	if len(args) == 0 {
		return usageErrorf("用法: jkv self <update|uninstall>")
	}
	manager := selfmanage.New(selfmanage.Config{
		Root:               s.Root,
		CurrentVersion:     version,
		StdinTerminal:      isTerminal(os.Stdin),
		CNBLatestURL:       selfCNBLatestURL,
		GitHubLatestURL:    selfGitHubLatestURL,
		CNBDownloadBase:    selfCNBDownloadBase,
		GitHubDownloadBase: selfGitHubDownloadBase,
	})
	switch args[0] {
	case "update", "up":
		if len(args) != 1 {
			return usageErrorf("用法: jkv self update")
		}
		latest, changed, err := manager.Update(ctx)
		if err != nil {
			return err
		}
		if changed {
			fmt.Printf("jkv 已更新: %s\n", latest)
		} else {
			fmt.Printf("jkv 已是最新版: %s\n", latest)
		}
		return nil
	case "uninstall", "rm":
		purge, yes := false, false
		for _, arg := range args[1:] {
			switch arg {
			case "--purge":
				purge = true
			case "--yes":
				yes = true
			default:
				return usageErrorf("self uninstall 不支持选项 %q", arg)
			}
		}
		if yes && !purge {
			return usageErrorf("用法: --yes 只能与 --purge 一起使用")
		}
		if err := manager.Uninstall(purge, yes); err != nil {
			return err
		}
		if purge {
			fmt.Printf("jkv 已彻底卸载: %s（不可恢复）\n", s.Root)
		} else {
			fmt.Printf("jkv 已卸载；已安装工具和配置保留在: %s\n", s.Root)
		}
		return nil
	default:
		return usageErrorf("未知命令 %q；用法: jkv self <update|uninstall>", args[0])
	}
}
