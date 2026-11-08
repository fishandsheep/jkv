package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/store"
)

func shellFlag(args []string) (string, []string, error) {
	shell := ""
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--shell" {
			if i+1 >= len(args) {
				return "", nil, usageErrorf("--shell 缺少值")
			}
			shell = args[i+1]
			i++
		} else {
			out = append(out, args[i])
		}
	}
	return shell, out, nil
}

func cmdUse(s *store.Store, args []string) error {
	shell, args, err := shellFlag(args)
	if err != nil {
		return err
	}
	if len(args) != 2 {
		return usageErrorf("用法: jkv use <工具> <版本>")
	}
	v, err := installedMatch(s, args[0], args[1])
	if err != nil {
		return err
	}
	if shell == "" {
		return usageErrorf("二进制无法修改父进程环境；先加载: %s", shellInitHint(guessedShell()))
	}
	fmt.Fprintf(os.Stderr, "已切换当前终端: %s %s\n", args[0], v)
	return printEnv(s, map[string]string{args[0]: v}, shell, false)
}

func cmdDefault(s *store.Store, args []string) error {
	shell, args, err := shellFlag(args)
	if err != nil {
		return err
	}
	if len(args) != 2 {
		return usageErrorf("用法: jkv default <工具> <版本>")
	}
	v, err := installedMatch(s, args[0], args[1])
	if err != nil {
		return err
	}
	if err := s.SetDefault(args[0], v); err != nil {
		return err
	}
	if shell != "" {
		fmt.Fprintf(os.Stderr, "已设置默认版本: %s %s\n", args[0], v)
		return printEnv(s, map[string]string{args[0]: v}, shell, false)
	}
	fmt.Printf("已设置默认版本: %s %s\n", args[0], v)
	return nil
}

func installedMatch(s *store.Store, candidate, want string) (string, error) {
	if candidate == "" || strings.ContainsAny(candidate, `/\\`) {
		return "", usageErrorf("不支持工具 %q", candidate)
	}
	versions, err := s.Installed(candidate)
	if err != nil {
		return "", err
	}
	for _, v := range versions {
		if v == want {
			return v, nil
		}
	}
	var matches []string
	for _, v := range versions {
		base := strings.Split(v, "-")[0]
		if base == want || strings.HasPrefix(base, want+".") || strings.HasSuffix(v, "-"+strings.TrimPrefix(want, strings.Split(want, "-")[0]+"-")) && strings.HasPrefix(base, strings.Split(want, "-")[0]) {
			matches = append(matches, v)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return "", notFoundErrorf("未安装 %s %s", candidate, want)
}

func cmdCurrent(ctx context.Context, s *store.Store, args []string) error {
	d, err := s.Defaults()
	if err != nil {
		return err
	}
	if len(args) == 1 {
		v := os.Getenv(currentVar(args[0]))
		if v == "" {
			v = d[args[0]]
		}
		if v == "" {
			return notFoundErrorf("%s 未设置默认版本", args[0])
		}
		if optionsFromContext(ctx).JSON {
			return writeJSON(map[string]string{"candidate": args[0], "version": v})
		}
		fmt.Println(v)
		return nil
	}
	for _, c := range catalog.Candidates {
		if v := os.Getenv(currentVar(c.Name)); v != "" {
			d[c.Name] = v
		}
	}
	keys := sortedKeys(d)
	if len(keys) == 0 {
		if optionsFromContext(ctx).JSON {
			return writeJSON(map[string]string{})
		}
		fmt.Println("尚未设置默认版本")
		return nil
	}
	if optionsFromContext(ctx).JSON {
		return writeJSON(d)
	}
	for _, k := range keys {
		fmt.Printf("%-12s %s\n", k, d[k])
	}
	return nil
}

func cmdUninstall(s *store.Store, args []string) error {
	if len(args) != 2 {
		return usageErrorf("用法: jkv uninstall <工具> <版本>")
	}
	v, err := installedMatch(s, args[0], args[1])
	if err != nil {
		return err
	}
	if err := s.Remove(args[0], v); err != nil {
		return err
	}
	fmt.Printf("已卸载: %s %s\n", args[0], v)
	return nil
}

func cmdHome(ctx context.Context, s *store.Store, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return usageErrorf("用法: jkv home <工具> [版本]")
	}
	v := ""
	if len(args) == 2 {
		v = args[1]
	} else {
		d, _ := s.Defaults()
		v = d[args[0]]
	}
	if v == "" {
		return notFoundErrorf("%s 未设置默认版本", args[0])
	}
	v, err := installedMatch(s, args[0], v)
	if err != nil {
		return err
	}
	h, err := s.Home(args[0], v)
	if err == nil {
		if optionsFromContext(ctx).JSON {
			return writeJSON(map[string]string{"candidate": args[0], "version": v, "home": h})
		}
		fmt.Println(h)
	}
	return err
}
