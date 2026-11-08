package catalog

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
)

type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type Release struct {
	Candidate            string   `json:"candidate"`
	Version              string   `json:"version"`
	Vendor               string   `json:"vendor,omitempty"`
	ArtifactID           string   `json:"artifact_id,omitempty"`
	SupportTier          string   `json:"support_tier"`
	IntegrityLevel       string   `json:"integrity_level"`
	URL                  string   `json:"url"`
	AllowedRedirectHosts []string `json:"allowed_redirect_hosts,omitempty"`
	ChecksumURL          string   `json:"checksum_url,omitempty"`
	ChecksumValue        string   `json:"checksum_value,omitempty"`
	Available            bool     `json:"available"`
	AvailabilityKnown    bool     `json:"availability_known"`
	AvailabilityStatus   string   `json:"availability_status"`
}

type Candidate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Platforms   string `json:"platforms"`
	SupportTier string `json:"support_tier"`
}

// Candidates is the built-in fallback shown before a signed catalog snapshot
// is available. Version data itself always comes from the signed catalog.
var Candidates = []Candidate{
	{"java", "JDK：Temurin、Alibaba Dragonwell、Huawei BiSheng", "签名 Catalog", "按发行商", "core"},
	{"maven", "Apache Maven", "签名 Catalog", "全平台", "core"},
	{"gradle", "Gradle", "签名 Catalog", "全平台", "core"},
	{"ant", "Apache Ant", "签名 Catalog", "全平台", "beta"},
	{"groovy", "Apache Groovy", "签名 Catalog", "全平台", "beta"},
	{"jmeter", "Apache JMeter", "签名 Catalog", "全平台", "beta"},
	{"tomcat", "Apache Tomcat", "签名 Catalog", "全平台", "beta"},
	{"springboot", "Spring Boot CLI", "签名 Catalog", "全平台", "beta"},
}

var candidateNames = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range Candidates {
		m[c.Name] = true
	}
	return m
}()

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

// stableVersion rejects pre-release identifiers during catalog validation.
func stableVersion(s string) bool {
	u := strings.ToUpper(s)
	return !strings.Contains(u, "SNAPSHOT") && !strings.Contains(u, "RC") &&
		!strings.Contains(u, "MILESTONE") && !regexp.MustCompile(`(^|[.-])M[0-9]`).MatchString(u) &&
		!strings.Contains(u, "ALPHA") && !strings.Contains(u, "BETA") &&
		!regexp.MustCompile(`(^|[.+_-])EA([.+_-]|$)`).MatchString(u)
}

// VersionLess compares version strings numerically. Clients use it when
// appending installed-only releases after catalog results.
func VersionLess(a, b string) bool {
	return versionLess(a, b)
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
