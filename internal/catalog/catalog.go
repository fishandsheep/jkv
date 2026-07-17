package catalog

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type Platform struct {
	OS   string
	Arch string
}

type Release struct {
	Candidate         string
	Version           string
	Vendor            string
	URL               string
	ChecksumURL       string
	Available         bool
	AvailabilityKnown bool
}

type Candidate struct {
	Name        string
	Description string
	Source      string
	Platforms   string
}

var Candidates = []Candidate{
	{"java", "JDK：Temurin、Alibaba Dragonwell、Huawei BiSheng", "清华 / 阿里 OSS / 华为云", "按发行商"},
	{"maven", "Apache Maven", "阿里云 Apache 镜像", "全平台"},
	{"gradle", "Gradle", "腾讯云镜像", "全平台"},
	{"ant", "Apache Ant", "阿里云 Apache 镜像", "全平台"},
	{"groovy", "Apache Groovy", "阿里云 Apache 镜像", "全平台"},
	{"jmeter", "Apache JMeter", "阿里云 Apache 镜像", "全平台"},
	{"tomcat", "Apache Tomcat", "阿里云 Apache 镜像", "全平台"},
	{"springboot", "Spring Boot CLI", "阿里云 Maven 仓库", "全平台"},
}

var candidateNames = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range Candidates {
		m[c.Name] = true
	}
	return m
}()

type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func CurrentPlatform() Platform {
	a := runtime.GOARCH
	if a == "amd64" {
		a = "x64"
	} else if a == "arm64" {
		a = "aarch64"
	}
	return Platform{OS: runtime.GOOS, Arch: a}
}

func IsCandidate(name string) bool { return candidateNames[name] }

func (c *Client) List(ctx context.Context, candidate string, p Platform) ([]Release, error) {
	if !IsCandidate(candidate) {
		return nil, fmt.Errorf("不支持 candidate %q", candidate)
	}
	var releases []Release
	var err error
	switch candidate {
	case "java":
		releases, err = c.java(ctx, p)
	case "gradle":
		releases, err = c.gradle(ctx)
	case "springboot":
		releases, err = c.springboot(ctx)
	case "maven":
		releases, err = c.maven(ctx, p)
	case "jmeter":
		releases, err = c.flatArchives(ctx, candidate, "https://mirrors.aliyun.com/apache/jmeter/binaries/", `apache-jmeter-([0-9][0-9A-Za-z.+-]*)`, p, false)
	case "ant":
		releases, err = c.flatArchives(ctx, candidate, "https://mirrors.aliyun.com/apache/ant/binaries/", `apache-ant-([0-9][0-9A-Za-z.+-]*)-bin`, p, false)
	case "groovy":
		releases, err = c.groovy(ctx)
	case "tomcat":
		releases, err = c.tomcat(ctx, p)
	}
	if err != nil {
		return nil, err
	}
	return uniqueSorted(releases, 40), nil
}

func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "jkv/0.1")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: HTTP %s", rawURL, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (c *Client) CheckAvailability(ctx context.Context, releases []Release) []Release {
	out := append([]Release(nil), releases...)
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := 12
	if len(out) < workers {
		workers = len(out)
	}
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				out[i].Available = c.downloadAvailable(ctx, out[i].URL)
				out[i].AvailabilityKnown = true
			}
		}()
	}
	for i := range out {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return out
}

func (c *Client) downloadAvailable(ctx context.Context, rawURL string) bool {
	check := func(method string, ranged bool) (int, error) {
		requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(requestCtx, method, rawURL, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("User-Agent", "jkv/0.1")
		if ranged {
			req.Header.Set("Range", "bytes=0-0")
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}
	status, err := check(http.MethodHead, false)
	if err == nil && status >= 200 && status < 400 {
		return true
	}
	if err == nil && status != http.StatusForbidden && status != http.StatusMethodNotAllowed && status != http.StatusNotImplemented {
		return false
	}
	status, err = check(http.MethodGet, true)
	return err == nil && status >= 200 && status < 400
}

var hrefRE = regexp.MustCompile(`(?i)href\s*=\s*["']?([^"' >]+)`)

func links(body []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range hrefRE.FindAllSubmatch(body, -1) {
		s := html.UnescapeString(string(m[1]))
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func resolve(base, ref string) string {
	b, _ := url.Parse(base)
	r, _ := url.Parse(ref)
	return b.ResolveReference(r).String()
}

func stableVersion(s string) bool {
	u := strings.ToUpper(s)
	return !strings.Contains(u, "SNAPSHOT") && !strings.Contains(u, "RC") &&
		!strings.Contains(u, "MILESTONE") && !regexp.MustCompile(`(^|[.-])M[0-9]`).MatchString(u) &&
		!strings.Contains(u, "ALPHA") && !strings.Contains(u, "BETA")
}

func archiveForPlatform(name string, p Platform) bool {
	l := strings.ToLower(name)
	if p.OS == "windows" {
		return strings.HasSuffix(l, ".zip")
	}
	return strings.HasSuffix(l, ".tar.gz") || strings.HasSuffix(l, ".tgz") || strings.HasSuffix(l, ".zip")
}

func (c *Client) java(ctx context.Context, p Platform) ([]Release, error) {
	type result struct {
		r []Release
		e error
	}
	ch := make(chan result, 3)
	go func() { r, e := c.temurin(ctx, p); ch <- result{r, e} }()
	go func() { r, e := c.dragonwell(ctx, p); ch <- result{r, e} }()
	go func() { r, e := c.bisheng(ctx, p); ch <- result{r, e} }()
	var all []Release
	var errs []string
	for range 3 {
		x := <-ch
		all = append(all, x.r...)
		if x.e != nil {
			errs = append(errs, x.e.Error())
		}
	}
	if len(all) == 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return all, nil
}

func (c *Client) temurin(ctx context.Context, p Platform) ([]Release, error) {
	osName := map[string]string{"darwin": "mac", "linux": "linux", "windows": "windows"}[p.OS]
	if osName == "" || (p.Arch != "x64" && p.Arch != "aarch64") {
		return nil, nil
	}
	majors := []string{"8", "11", "17", "21", "25"}
	type result struct{ r Release }
	ch := make(chan result, len(majors))
	var wg sync.WaitGroup
	for _, major := range majors {
		major := major
		wg.Add(1)
		go func() {
			defer wg.Done()
			base := fmt.Sprintf("https://mirrors.tuna.tsinghua.edu.cn/Adoptium/%s/jdk/%s/%s/", major, p.Arch, osName)
			body, err := c.get(ctx, base)
			if err != nil {
				return
			}
			for _, link := range links(body) {
				l := strings.ToLower(link)
				if !strings.Contains(l, "openjdk") || !strings.Contains(l, "-jdk_") || !archiveForPlatform(link, p) {
					continue
				}
				if p.OS == "darwin" && !strings.HasSuffix(l, ".tar.gz") || p.OS == "windows" && !strings.HasSuffix(l, ".zip") {
					continue
				}
				v := regexp.MustCompile(`hotspot_([^/]+?)(?:\.tar\.gz|\.zip)$`).FindStringSubmatch(link)
				if len(v) == 2 {
					version := strings.Replace(v[1], "_", "+", 1) + "-tem"
					ch <- result{Release{Candidate: "java", Version: version, Vendor: "temurin", URL: resolve(base, link)}}
				}
			}
		}()
	}
	go func() { wg.Wait(); close(ch) }()
	var out []Release
	for x := range ch {
		out = append(out, x.r)
	}
	return out, nil
}

func (c *Client) dragonwell(ctx context.Context, p Platform) ([]Release, error) {
	if p.OS == "darwin" || (p.Arch != "x64" && p.Arch != "aarch64") {
		return nil, nil
	}
	body, err := c.get(ctx, "https://dragonwell-jdk.io/releases.json")
	if err != nil {
		return dragonwellFallback(p), nil
	}
	var data map[string]map[string]map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	standard := data["oss"]["standard"]
	var out []Release
	for _, major := range []string{"8", "11", "17", "21", "25"} {
		v, _ := standard["version"+major].(string)
		key := "xurl" + major
		if p.Arch == "aarch64" {
			key = "aurl" + major
		} else if p.OS == "windows" {
			key = "wurl" + major
		}
		u, _ := standard[key].(string)
		if v != "" && v != "0" && u != "" {
			out = append(out, Release{Candidate: "java", Version: v + "-dragonwell", Vendor: "dragonwell", URL: u})
		}
	}
	return out, nil
}

// Official metadata endpoint is occasionally unavailable. Keep latest known
// Alibaba OSS coordinates as fallback; downloads still come from official OSS.
func dragonwellFallback(p Platform) []Release {
	versions := map[string]string{
		"8": "8.29.28", "11": "11.0.31.27.11", "17": "17.0.19.0.20.10",
		"21": "21.0.11.0.11.10", "25": "25.0.3.0.3.9",
	}
	buildPaths := map[string]string{
		"8": "8.29.28", "11": "11.0.31.27.11", "17": "17.0.19.0.20%2B10",
		"21": "21.0.11.0.11%2B10", "25": "25.0.3.0.3%2B9",
	}
	arch := p.Arch
	if arch == "x64" {
		arch = "x64"
	}
	var out []Release
	for _, major := range []string{"8", "11", "17", "21", "25"} {
		if p.OS == "windows" && p.Arch != "x64" {
			continue
		}
		ext := ".tar.gz"
		osName := "linux"
		if p.OS == "windows" {
			ext, osName = ".zip", "windows"
		}
		name := fmt.Sprintf("Alibaba_Dragonwell_Standard_%s_%s_%s%s", versions[major], arch, osName, ext)
		u := "https://dragonwell.oss-cn-shanghai.aliyuncs.com/" + buildPaths[major] + "/" + name
		out = append(out, Release{Candidate: "java", Version: versions[major] + "-dragonwell", Vendor: "dragonwell", URL: u})
	}
	return out
}

func (c *Client) bisheng(ctx context.Context, p Platform) ([]Release, error) {
	if p.OS != "linux" || (p.Arch != "x64" && p.Arch != "aarch64") {
		return nil, nil
	}
	base := "https://mirrors.huaweicloud.com/kunpeng/archive/compiler/bisheng_jdk/"
	body, err := c.get(ctx, base)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`^bisheng-jdk-?(8u[0-9]+(?:-b[0-9]+)?|(?:11|17|21|25)\.[0-9.]+(?:-b[0-9]+)?)-linux-` + regexp.QuoteMeta(p.Arch) + `\.tar\.gz$`)
	best := map[string]Release{}
	for _, link := range links(body) {
		m := re.FindStringSubmatch(link)
		if len(m) != 2 || strings.Contains(link, "debug") || strings.Contains(link, "fusion") {
			continue
		}
		major := strings.Split(strings.TrimPrefix(m[1], "8u"), ".")[0]
		if strings.HasPrefix(m[1], "8u") {
			major = "8"
		}
		r := Release{Candidate: "java", Version: m[1] + "-bisheng", Vendor: "bisheng", URL: resolve(base, link), ChecksumURL: resolve(base, link+".sha256")}
		if old, ok := best[major]; !ok || versionLess(old.Version, r.Version) {
			best[major] = r
		}
	}
	var out []Release
	for _, r := range best {
		out = append(out, r)
	}
	return out, nil
}

func (c *Client) gradle(ctx context.Context) ([]Release, error) {
	base := "https://mirrors.cloud.tencent.com/gradle/"
	body, err := c.get(ctx, base)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`^gradle-([0-9]+(?:\.[0-9]+){1,2})-bin\.zip$`)
	var out []Release
	for _, link := range links(body) {
		if m := re.FindStringSubmatch(link); len(m) == 2 {
			out = append(out, Release{Candidate: "gradle", Version: m[1], Vendor: "gradle", URL: resolve(base, link)})
		}
	}
	return uniqueSorted(out, 30), nil
}

func (c *Client) maven(ctx context.Context, p Platform) ([]Release, error) {
	root := "https://mirrors.aliyun.com/apache/maven/maven-3/"
	body, err := c.get(ctx, root)
	if err != nil {
		return nil, err
	}
	var bases []string
	for _, dir := range links(body) {
		if !regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)+/$`).MatchString(dir) {
			continue
		}
		bases = append(bases, resolve(root, dir+"binaries/"))
	}
	return parallelReleases(bases, 8, func(base string) []Release {
		b, err := c.get(ctx, base)
		if err != nil {
			return nil
		}
		r, _ := c.archivesFromBody("maven", base, b, `apache-maven-([0-9][0-9A-Za-z.+-]*)-bin`, p, false)
		return r
	}), nil
}

func (c *Client) flatArchives(ctx context.Context, candidate, base, pattern string, p Platform, zipOnly bool) ([]Release, error) {
	body, err := c.get(ctx, base)
	if err != nil {
		return nil, err
	}
	return c.archivesFromBody(candidate, base, body, pattern, p, zipOnly)
}

func (c *Client) archivesFromBody(candidate, base string, body []byte, pattern string, p Platform, zipOnly bool) ([]Release, error) {
	re, err := regexp.Compile(`^(` + pattern + `)(?:\.tar\.gz|\.tgz|\.zip)$`)
	if err != nil {
		return nil, err
	}
	byVersion := map[string]Release{}
	for _, link := range links(body) {
		m := re.FindStringSubmatch(link)
		if len(m) < 3 || !stableVersion(m[2]) || !archiveForPlatform(link, p) {
			continue
		}
		if zipOnly && !strings.HasSuffix(strings.ToLower(link), ".zip") {
			continue
		}
		if p.OS == "windows" && !strings.HasSuffix(strings.ToLower(link), ".zip") {
			continue
		}
		if p.OS != "windows" && strings.HasSuffix(strings.ToLower(link), ".zip") {
			if _, exists := byVersion[m[2]]; exists {
				continue
			}
		}
		byVersion[m[2]] = Release{Candidate: candidate, Version: m[2], Vendor: candidate, URL: resolve(base, link)}
	}
	var out []Release
	for _, r := range byVersion {
		out = append(out, r)
	}
	return out, nil
}

func (c *Client) groovy(ctx context.Context) ([]Release, error) {
	root := "https://mirrors.aliyun.com/apache/groovy/"
	body, err := c.get(ctx, root)
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, dir := range links(body) {
		v := strings.TrimSuffix(dir, "/")
		if !regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)+$`).MatchString(v) || !stableVersion(v) {
			continue
		}
		versions = append(versions, v)
	}
	return parallelReleases(versions, 8, func(v string) []Release {
		base := resolve(root, v+"/distribution/")
		b, err := c.get(ctx, base)
		if err != nil {
			return nil
		}
		name := "apache-groovy-binary-" + v + ".zip"
		if strings.Contains(string(b), name) {
			return []Release{{Candidate: "groovy", Version: v, Vendor: "groovy", URL: resolve(base, name)}}
		}
		return nil
	}), nil
}

func (c *Client) tomcat(ctx context.Context, p Platform) ([]Release, error) {
	root := "https://mirrors.aliyun.com/apache/tomcat/"
	body, err := c.get(ctx, root)
	if err != nil {
		return nil, err
	}
	var bases []string
	for _, branch := range links(body) {
		if !regexp.MustCompile(`^tomcat-(9|10|11)/$`).MatchString(branch) {
			continue
		}
		branchURL := resolve(root, branch)
		b, e := c.get(ctx, branchURL)
		if e != nil {
			continue
		}
		for _, dir := range links(b) {
			v := strings.TrimPrefix(strings.TrimSuffix(dir, "/"), "v")
			if !regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)+$`).MatchString(v) {
				continue
			}
			bases = append(bases, resolve(branchURL, dir+"bin/"))
		}
	}
	return parallelReleases(bases, 8, func(base string) []Release {
		b, err := c.get(ctx, base)
		if err != nil {
			return nil
		}
		r, _ := c.archivesFromBody("tomcat", base, b, `apache-tomcat-([0-9]+(?:\.[0-9]+)+)`, p, false)
		return r
	}), nil
}

func (c *Client) springboot(ctx context.Context) ([]Release, error) {
	const root = "https://maven.aliyun.com/repository/central/org/springframework/boot/spring-boot-cli/"
	body, err := c.get(ctx, root+"maven-metadata.xml")
	if err != nil {
		return nil, err
	}
	var m struct {
		Versions []string `xml:"versioning>versions>version"`
	}
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	var out []Release
	for _, v := range m.Versions {
		if stableVersion(v) {
			u := root + v + "/spring-boot-cli-" + v + "-bin.zip"
			out = append(out, Release{Candidate: "springboot", Version: v, Vendor: "spring", URL: u})
		}
	}
	return uniqueSorted(out, 30), nil
}

func uniqueSorted(in []Release, limit int) []Release {
	seen := map[string]bool{}
	var out []Release
	for _, r := range in {
		key := r.Candidate + "\x00" + r.Version
		if r.URL == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return versionLess(out[j].Version, out[i].Version) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func parallelReleases[T any](items []T, limit int, fn func(T) []Release) []Release {
	if limit < 1 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	results := make(chan []Release, len(items))
	var wg sync.WaitGroup
	for _, item := range items {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results <- fn(item)
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	var out []Release
	for releases := range results {
		out = append(out, releases...)
	}
	return out
}

func versionLess(a, b string) bool {
	parts := func(s string) []int {
		re := regexp.MustCompile(`[0-9]+`)
		ms := re.FindAllString(strings.Split(s, "-")[0], -1)
		out := make([]int, len(ms))
		for i, x := range ms {
			fmt.Sscanf(x, "%d", &out[i])
		}
		return out
	}
	aa, bb := parts(a), parts(b)
	n := len(aa)
	if len(bb) > n {
		n = len(bb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(aa) {
			x = aa[i]
		}
		if i < len(bb) {
			y = bb[i]
		}
		if x != y {
			return x < y
		}
	}
	return a < b
}
