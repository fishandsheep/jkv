package catalog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixtureClient(t *testing.T, fixture func(string) (int, string)) *Client {
	t.Helper()
	return &Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status, body := fixture(r.URL.String())
		if status == 0 {
			return nil, errors.New("fixture connection failure")
		}
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}}
}

func TestVersionLess(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"8.9", "8.10"}, {"17.0.9", "17.0.10"}, {"21.0.1", "25.0.0"},
	} {
		if !versionLess(tc.a, tc.b) {
			t.Fatalf("expected %s < %s", tc.a, tc.b)
		}
	}
}

func TestLinks(t *testing.T) {
	got := links([]byte(`<a href="one.zip">x</a><a href=two.tar.gz>y</a><a href="one.zip">z</a>`))
	if len(got) != 2 || got[0] != "one.zip" || got[1] != "two.tar.gz" {
		t.Fatalf("links = %#v", got)
	}
	if got := resolve("https://example.test/a/", "../b.zip"); got != "https://example.test/b.zip" {
		t.Fatalf("resolve = %q", got)
	}
}

func TestProvidersLive(t *testing.T) {
	if testing.Short() {
		t.Skip("live mirror test")
	}
	for _, candidate := range Candidates {
		t.Run(candidate.Name, func(t *testing.T) {
			r, err := NewClient().List(context.Background(), candidate.Name, Platform{OS: "linux", Arch: "x64"})
			if err != nil {
				t.Fatal(err)
			}
			if len(r) == 0 {
				t.Fatal("provider returned no releases")
			}
		})
	}
}

func TestCheckAvailability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
		case "/range":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.Header.Get("Range") != "bytes=0-0" {
				t.Errorf("Range = %q", r.Header.Get("Range"))
			}
			w.WriteHeader(http.StatusPartialContent)
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	releases := []Release{
		{URL: server.URL + "/ok"},
		{URL: server.URL + "/range"},
		{URL: server.URL + "/missing"},
	}
	got := (&Client{HTTP: server.Client()}).CheckAvailability(context.Background(), releases)
	if !got[0].Available || !got[1].Available || got[2].Available {
		t.Fatalf("availability = %#v", got)
	}
	for _, release := range got {
		if !release.AvailabilityKnown {
			t.Fatal("availability was not marked as checked")
		}
	}
}

func TestCatalogFixtures(t *testing.T) {
	client := fixtureClient(t, func(rawURL string) (int, string) {
		switch {
		case strings.Contains(rawURL, "/Adoptium/"):
			return 200, `<a href="OpenJDK21U-jdk_x64_linux_hotspot_21.0.8_9.tar.gz">jdk</a>`
		case rawURL == "https://dragonwell-jdk.io/releases.json":
			return 200, `{"oss":{"standard":{"version21":"21.0.8","xurl21":"https://oss.test/dragonwell.tar.gz"}}}`
		case strings.Contains(rawURL, "bisheng_jdk"):
			return 200, `<a href="bisheng-jdk-21.0.8-b9-linux-x64.tar.gz">jdk</a>`
		case rawURL == "https://mirrors.cloud.tencent.com/gradle/":
			return 200, `<a href="gradle-8.14.3-bin.zip">ok</a><a href="gradle-9.0-rc1-bin.zip">rc</a>`
		case rawURL == "https://mirrors.aliyun.com/apache/maven/maven-3/":
			return 200, `<a href="3.9.11/">version</a>`
		case strings.Contains(rawURL, "/maven-3/3.9.11/binaries/"):
			return 200, `<a href="apache-maven-3.9.11-bin.tar.gz">tar</a><a href="apache-maven-3.9.11-bin.zip">zip</a>`
		case rawURL == "https://mirrors.aliyun.com/apache/ant/binaries/":
			return 200, `<a href="apache-ant-1.10.15-bin.tar.gz">tar</a>`
		case rawURL == "https://mirrors.aliyun.com/apache/jmeter/binaries/":
			return 200, `<a href="apache-jmeter-5.6.3.tgz">tgz</a>`
		case rawURL == "https://mirrors.aliyun.com/apache/groovy/":
			return 200, `<a href="4.0.28/">stable</a><a href="5.0.0-rc1/">rc</a>`
		case strings.Contains(rawURL, "/groovy/4.0.28/distribution/"):
			return 200, `apache-groovy-binary-4.0.28.zip`
		case rawURL == "https://mirrors.aliyun.com/apache/tomcat/":
			return 200, `<a href="tomcat-11/">branch</a>`
		case rawURL == "https://mirrors.aliyun.com/apache/tomcat/tomcat-11/":
			return 200, `<a href="v11.0.10/">version</a>`
		case strings.Contains(rawURL, "/tomcat-11/v11.0.10/bin/"):
			return 200, `<a href="apache-tomcat-11.0.10.tar.gz">tar</a>`
		case strings.HasSuffix(rawURL, "/spring-boot-cli/maven-metadata.xml"):
			return 200, `<metadata><versioning><versions><version>3.5.4</version><version>4.0.0-RC1</version></versions></versioning></metadata>`
		default:
			return 404, ""
		}
	})

	for _, tc := range []struct {
		candidate string
		version   string
		tier      string
	}{
		{"java", "21.0.8+9-tem", "core"},
		{"maven", "3.9.11", "core"},
		{"gradle", "8.14.3", "core"},
		{"ant", "1.10.15", "beta"},
		{"jmeter", "5.6.3", "beta"},
		{"groovy", "4.0.28", "beta"},
		{"tomcat", "11.0.10", "beta"},
		{"springboot", "3.5.4", "beta"},
	} {
		t.Run(tc.candidate, func(t *testing.T) {
			got, err := client.List(context.Background(), tc.candidate, Platform{OS: "linux", Arch: "x64"})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, release := range got {
				if release.Version == tc.version && release.SupportTier == tc.tier {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s tier %s in %#v", tc.version, tc.tier, got)
			}
		})
	}
}

func TestCatalogErrorAndFallbacks(t *testing.T) {
	if _, err := NewClient().List(context.Background(), "unknown", CurrentPlatform()); err == nil {
		t.Fatal("unsupported candidate accepted")
	}
	client := fixtureClient(t, func(rawURL string) (int, string) {
		if strings.Contains(rawURL, "dragonwell-jdk.io") {
			return 0, ""
		}
		return 500, ""
	})
	got, err := client.dragonwell(context.Background(), Platform{OS: "windows", Arch: "x64"})
	if err != nil || len(got) != 5 {
		t.Fatalf("fallback = %#v, %v", got, err)
	}
	if got, err := client.temurin(context.Background(), Platform{OS: "plan9", Arch: "x64"}); err != nil || got != nil {
		t.Fatalf("unsupported platform = %#v, %v", got, err)
	}
	if got, err := client.bisheng(context.Background(), Platform{OS: "darwin", Arch: "x64"}); err != nil || got != nil {
		t.Fatalf("unsupported bisheng = %#v, %v", got, err)
	}
	if _, err := client.gradle(context.Background()); err == nil {
		t.Fatal("expected HTTP error")
	}
}

func TestArchiveSelectionAndSorting(t *testing.T) {
	if stableVersion("1.0-RC1") || stableVersion("1.0-beta") || !stableVersion("1.0.1") {
		t.Fatal("stableVersion classification failed")
	}
	if !archiveForPlatform("x.zip", Platform{OS: "windows"}) ||
		archiveForPlatform("x.tar.gz", Platform{OS: "windows"}) ||
		!archiveForPlatform("x.tgz", Platform{OS: "linux"}) {
		t.Fatal("archiveForPlatform classification failed")
	}
	client := &Client{}
	got, err := client.archivesFromBody("tool", "https://example.test/", []byte(
		`<a href="tool-1.10.tar.gz"></a><a href="tool-1.10.zip"></a><a href="tool-1.9.tar.gz"></a><a href="tool-2.0-RC1.tar.gz"></a>`),
		`tool-([0-9][0-9A-Za-z.+-]*)`, Platform{OS: "linux", Arch: "x64"}, false)
	if err != nil || len(got) != 2 {
		t.Fatalf("archives = %#v, %v", got, err)
	}
	sorted := uniqueSorted([]Release{
		{Candidate: "x", Version: "1.9", URL: "a"},
		{Candidate: "x", Version: "1.10", URL: "b"},
		{Candidate: "x", Version: "1.10", URL: "duplicate"},
		{Candidate: "x", Version: "2.0"},
	}, 1)
	if len(sorted) != 1 || sorted[0].Version != "1.10" {
		t.Fatalf("sorted = %#v", sorted)
	}
	if got := parallelReleases([]int{1, 2}, 0, func(n int) []Release {
		return []Release{{Version: string(rune('0' + n))}}
	}); len(got) != 2 {
		t.Fatalf("parallel releases = %#v", got)
	}
}
