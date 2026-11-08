package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/fishandsheep/jkv/internal/store"
)

type doctorResult struct {
	Root       string               `json:"root"`
	OS         string               `json:"os"`
	Arch       string               `json:"arch"`
	Writable   bool                 `json:"writable"`
	HTTPSProxy bool                 `json:"https_proxy"`
	HTTPProxy  bool                 `json:"http_proxy"`
	NoProxy    bool                 `json:"no_proxy"`
	Installed  map[string]int       `json:"installed"`
	Trust      *doctorCatalogTrust  `json:"catalog_trust,omitempty"`
	Mirrors    []doctorMirrorResult `json:"mirrors"`
}

type doctorCatalogTrust struct {
	Sequence       uint64   `json:"sequence"`
	SnapshotAge    string   `json:"snapshot_age"`
	Endpoint       string   `json:"endpoint"`
	VerifiedKeyIDs []string `json:"verified_key_ids"`
	Revocations    int      `json:"revocations"`
}

type doctorMirrorResult struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Reachable bool   `json:"reachable"`
	Status    int    `json:"status,omitempty"`
}

var doctorHTTPClient = &http.Client{Timeout: 4 * time.Second}

func cmdDoctor(ctx context.Context, s *store.Store, args []string) error {
	if len(args) != 0 {
		return usageErrorf("用法: jkv doctor")
	}
	result := doctorResult{
		Root:       "<JKV_DIR>",
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		HTTPSProxy: os.Getenv("HTTPS_PROXY") != "" || os.Getenv("https_proxy") != "",
		HTTPProxy:  os.Getenv("HTTP_PROXY") != "" || os.Getenv("http_proxy") != "",
		NoProxy:    os.Getenv("NO_PROXY") != "" || os.Getenv("no_proxy") != "",
		Installed:  map[string]int{},
	}
	if err := os.MkdirAll(s.Root, 0o755); err == nil {
		if f, createErr := os.CreateTemp(s.Root, ".jkv-doctor-"); createErr == nil {
			result.Writable = true
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
		}
	}
	if state, err := s.LoadTrustedCatalog(); err == nil && len(state.Snapshots) > 0 {
		current := state.Snapshots[0]
		for _, snapshot := range state.Snapshots[1:] {
			if snapshot.Sequence > current.Sequence {
				current = snapshot
			}
		}
		result.Trust = &doctorCatalogTrust{
			Sequence:       state.HighestSequence,
			SnapshotAge:    time.Since(current.FetchedAt).Round(time.Second).String(),
			Endpoint:       current.Endpoint,
			VerifiedKeyIDs: append([]string(nil), current.KeyIDs...),
			Revocations:    len(state.Revocations),
		}
	}
	for _, candidate := range []string{"java", "maven", "gradle"} {
		if versions, err := s.Installed(candidate); err == nil {
			result.Installed[candidate] = len(versions)
		}
	}
	endpoints := []struct {
		name string
		url  string
	}{
		{"temurin", "https://mirrors.tuna.tsinghua.edu.cn/Adoptium/"},
		{"maven", "https://mirrors.aliyun.com/apache/maven/maven-3/"},
		{"gradle", "https://mirrors.cloud.tencent.com/gradle/"},
	}
	type mirrorCheck struct {
		index  int
		result doctorMirrorResult
	}
	checks := make(chan mirrorCheck, len(endpoints))
	for index, endpoint := range endpoints {
		go func() {
			checkCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(checkCtx, http.MethodHead, endpoint.url, nil)
			result := doctorMirrorResult{Name: endpoint.name, Host: hostOf(endpoint.url)}
			if err == nil {
				resp, requestErr := doctorHTTPClient.Do(req)
				if requestErr == nil {
					result.Status = resp.StatusCode
					result.Reachable = resp.StatusCode >= 200 && resp.StatusCode < 400
					_ = resp.Body.Close()
				}
			}
			checks <- mirrorCheck{index: index, result: result}
		}()
	}
	result.Mirrors = make([]doctorMirrorResult, len(endpoints))
	for range endpoints {
		check := <-checks
		result.Mirrors[check.index] = check.result
	}
	if optionsFromContext(ctx).JSON {
		if err := writeJSON(result); err != nil {
			return err
		}
		if !result.Writable {
			return stateErrorf("JKV_DIR 不可写: <JKV_DIR>")
		}
		return nil
	}
	fmt.Printf("jkv doctor\n目录: %s\n平台: %s/%s\n可写: %t\n", result.Root, result.OS, result.Arch, result.Writable)
	if result.Trust != nil {
		fmt.Printf("可信 Catalog: sequence %d，年龄 %s，端点 %s，撤销 %d\n", result.Trust.Sequence, result.Trust.SnapshotAge, result.Trust.Endpoint, result.Trust.Revocations)
	}
	for _, mirror := range result.Mirrors {
		fmt.Printf("镜像 %-8s %-38s 可达: %t", mirror.Name, mirror.Host, mirror.Reachable)
		if mirror.Status != 0 {
			fmt.Printf(" (HTTP %d)", mirror.Status)
		}
		fmt.Println()
	}
	if !result.Writable {
		return stateErrorf("JKV_DIR 不可写: <JKV_DIR>")
	}
	return nil
}
