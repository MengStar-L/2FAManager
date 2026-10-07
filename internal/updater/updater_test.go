package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testTransport struct {
	endpoint *url.URL
	base     http.RoundTripper
}

func (t testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	address := *req.URL
	copy.Host = req.URL.Host
	address.Scheme, address.Host = t.endpoint.Scheme, t.endpoint.Host
	copy.URL = &address
	return t.base.RoundTrip(copy)
}

func fixtureService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	endpoint, _ := url.Parse(server.URL)
	client := server.Client()
	client.Transport = testTransport{endpoint: endpoint, base: client.Transport}
	service, err := New(Config{Owner: "example", Repo: "2FAManager", CurrentVersion: "0.2.3", Client: client})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func fixtureRelease(binary []byte) (githubRelease, []byte) {
	name := AssetName("0.3.0")
	manifest := []byte(digest(binary) + "  " + name + "\n")
	base := "https://github.com/example/2FAManager/releases/download/v0.3.0/"
	return githubRelease{
		Tag: "v0.3.0", Body: "New update\n<script>untrusted notes</script>", PublishedAt: "2026-10-07T12:00:00Z",
		Assets: []githubAsset{
			{Name: name, URL: base + name, Size: int64(len(binary)), State: "uploaded", Digest: "sha256:" + digest(binary)},
			{Name: checksumsName, URL: base + checksumsName, Size: int64(len(manifest)), State: "uploaded", Digest: "sha256:" + digest(manifest)},
		},
	}, manifest
}

func fixtureHandler(release githubRelease, manifest, binary []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Host == "api.github.com":
			json.NewEncoder(w).Encode(release)
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			w.Write(manifest)
		default:
			w.Write(binary)
		}
	}
}

func TestVersionOrdering(t *testing.T) {
	ordered := []string{"0.2.3", "v0.3.0-alpha", "0.3.0-alpha.1", "0.3.0-alpha.beta", "0.3.0-beta", "0.3.0-beta.2", "0.3.0-beta.11", "0.3.0-rc.1", "0.3.0", "0.10.0", "1.0.0", "123456789012345678901234567890.0.0"}
	for i, a := range ordered {
		for j, b := range ordered {
			got, err := CompareVersions(a, b)
			if err != nil || (i < j && got >= 0) || (i > j && got <= 0) || (i == j && got != 0) {
				t.Fatalf("compare %s %s: %d, %v", a, b, got, err)
			}
		}
	}
	if got, err := CompareVersions("v0.3.0+build.1", "0.3.0+other"); err != nil || got != 0 {
		t.Fatalf("build metadata must be ignored: %d %v", got, err)
	}
	for _, invalid := range []string{"", "0.3", "v0.3.00", "01.2.3", "1.0.0-01", "1.0.0-alpha..beta", "1.0.0+foo..bar", "1.0.0-", "1.0.0;command"} {
		if _, err := CompareVersions(invalid, "0.3.0"); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
}

func TestCheckAndVerifiedDownload(t *testing.T) {
	binary := []byte("test fixture bytes, never executed")
	release, manifest := fixtureRelease(binary)
	service := fixtureService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "api.github.com" {
			if r.URL.Path != "/repos/example/2FAManager/releases/latest" || r.Header.Get("X-GitHub-Api-Version") != apiVersion || r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("Authorization") != "" {
				t.Errorf("unexpected request %s headers %v", r.URL.Path, r.Header)
			}
		}
		fixtureHandler(release, manifest, binary)(w, r)
	})
	found, err := service.Check(context.Background())
	if err != nil || found == nil || found.Version != "0.3.0" || found.AssetSize != int64(len(binary)) || found.Notes != release.Body || found.URL != "https://github.com/example/2FAManager/releases/tag/v0.3.0" {
		t.Fatalf("check: %#v %v", found, err)
	}
	// Display fields are deliberately mutable; they cannot change what is downloaded.
	found.Version, found.AssetName, found.AssetSize = "999.0.0", "../../evil.exe", 1
	staging := t.TempDir()
	sentinel := filepath.Join(staging, "user-data.dat")
	os.WriteFile(sentinel, []byte("untouched"), 0600)
	var updates []Progress
	download, err := service.Download(context.Background(), found, staging, func(p Progress) { updates = append(updates, p) })
	if err != nil {
		t.Fatal(err)
	}
	defer download.Cleanup()
	got, err := os.ReadFile(download.Path)
	if err != nil || string(got) != string(binary) || download.SHA256 != digest(binary) || download.Version != "0.3.0" || filepath.Base(download.Path) != AssetName("0.3.0") {
		t.Fatalf("bad verified download: %#v %s %v", download, got, err)
	}
	if len(updates) < 2 || updates[0].Percent != 0 || updates[len(updates)-1].Percent != 100 {
		t.Fatalf("missing start/end progress: %v", updates)
	}
	if err := download.Cleanup(); err != nil {
		t.Fatal(err)
	}
	remaining, _ := os.ReadDir(staging)
	if len(remaining) != 1 || remaining[0].Name() != "user-data.dat" {
		t.Fatalf("cleanup affected unrelated files: %v", remaining)
	}
}

func TestCheckRejectsUnsafeOrIncompleteReleases(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*githubRelease)
		nilOK  bool
	}{
		{"same version", func(r *githubRelease) { r.Tag = "v0.2.3" }, true},
		{"downgrade", func(r *githubRelease) { r.Tag = "v0.1.0" }, true},
		{"prerelease", func(r *githubRelease) { r.Prerelease = true }, true},
		{"semver prerelease", func(r *githubRelease) { r.Tag = "v0.3.0-rc.1" }, true},
		{"draft", func(r *githubRelease) { r.Draft = true }, true},
		{"invalid version", func(r *githubRelease) { r.Tag = "nightly" }, false},
		{"missing checksum", func(r *githubRelease) { r.Assets = r.Assets[:1] }, false},
		{"wrong platform", func(r *githubRelease) { r.Assets[0].Name = "Luma-0.3.0-linux-amd64.exe" }, false},
		{"duplicate executable", func(r *githubRelease) { r.Assets = append(r.Assets, r.Assets[0]) }, false},
		{"incomplete upload", func(r *githubRelease) { r.Assets[0].State = "starter" }, false},
		{"oversized executable", func(r *githubRelease) { r.Assets[0].Size = MaxDownloadBytes + 1 }, false},
		{"oversized checksum", func(r *githubRelease) { r.Assets[1].Size = maxManifestBytes + 1 }, false},
		{"http asset", func(r *githubRelease) { r.Assets[0].URL = strings.Replace(r.Assets[0].URL, "https:", "http:", 1) }, false},
		{"wrong repository", func(r *githubRelease) {
			r.Assets[0].URL = strings.Replace(r.Assets[0].URL, "/example/", "/attacker/", 1)
		}, false},
		{"wrong tag", func(r *githubRelease) { r.Assets[0].URL = strings.Replace(r.Assets[0].URL, "/v0.3.0/", "/v9.0.0/", 1) }, false},
		{"external host", func(r *githubRelease) {
			r.Assets[0].URL = strings.Replace(r.Assets[0].URL, "github.com", "github.com.evil.test", 1)
		}, false},
		{"URL credentials", func(r *githubRelease) {
			r.Assets[0].URL = strings.Replace(r.Assets[0].URL, "github.com", "user@github.com", 1)
		}, false},
		{"URL port", func(r *githubRelease) {
			r.Assets[0].URL = strings.Replace(r.Assets[0].URL, "github.com", "github.com:443", 1)
		}, false},
		{"bad API digest", func(r *githubRelease) { r.Assets[0].Digest = "md5:abc" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, manifest := fixtureRelease([]byte("test"))
			tc.modify(&r)
			service := fixtureService(t, fixtureHandler(r, manifest, []byte("test")))
			found, err := service.Check(context.Background())
			if found != nil || (tc.nilOK && err != nil) || (!tc.nilOK && err == nil) {
				t.Fatalf("unexpected result %#v %v", found, err)
			}
		})
	}
}

func TestAPIResponseFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		nilOK  bool
	}{
		{"no published release", 404, "", true},
		{"rate limited", 403, "sensitive remote content must not be shown", false},
		{"server error", 503, "<html>unavailable</html>", false},
		{"bad JSON", 200, "not json", false},
		{"oversized JSON", 200, strings.Repeat(" ", int(maxAPIBytes)+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixtureService(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) })
			got, err := s.Check(context.Background())
			if got != nil || (tc.nilOK && err != nil) || (!tc.nilOK && err == nil) {
				t.Fatalf("unexpected API result: %#v %v", got, err)
			}
			if err != nil && strings.Contains(err.Error(), "sensitive remote content") {
				t.Fatal("response body leaked into error")
			}
		})
	}
}

func TestDownloadIntegrityAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*githubRelease, *[]byte, *[]byte)
	}{
		{"corrupt binary", func(r *githubRelease, m, b *[]byte) { *b = []byte("evil") }},
		{"truncated binary", func(r *githubRelease, m, b *[]byte) { *b = []byte("a") }},
		{"oversized binary", func(r *githubRelease, m, b *[]byte) { *b = []byte("too many bytes") }},
		{"wrong checksum", func(r *githubRelease, m, b *[]byte) {
			*m = []byte(strings.Repeat("0", 64) + "  " + r.Assets[0].Name + "\n")
			r.Assets[1].Digest = ""
			r.Assets[0].Digest = ""
		}},
		{"wrong manifest API digest", func(r *githubRelease, m, b *[]byte) { r.Assets[1].Digest = "sha256:" + strings.Repeat("0", 64) }},
		{"wrong binary API digest", func(r *githubRelease, m, b *[]byte) { r.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64) }},
		{"duplicate checksums", func(r *githubRelease, m, b *[]byte) {
			*m = append(*m, *m...)
			r.Assets[1].Size = int64(len(*m))
			r.Assets[1].Digest = ""
		}},
		{"malformed checksums", func(r *githubRelease, m, b *[]byte) {
			*m = []byte("not a checksum")
			r.Assets[1].Size = int64(len(*m))
			r.Assets[1].Digest = ""
		}},
		{"missing filename", func(r *githubRelease, m, b *[]byte) {
			*m = []byte(digest(*b) + "  elsewhere.exe\n")
			r.Assets[1].Size = int64(len(*m))
			r.Assets[1].Digest = ""
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := []byte("good")
			r, manifest := fixtureRelease(binary)
			tc.modify(&r, &manifest, &binary)
			s := fixtureService(t, fixtureHandler(r, manifest, binary))
			found, err := s.Check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			staging := t.TempDir()
			got, err := s.Download(context.Background(), found, staging, nil)
			if got != nil || err == nil {
				t.Fatalf("accepted invalid download: %#v %v", got, err)
			}
			remaining, _ := os.ReadDir(staging)
			if len(remaining) != 0 {
				t.Fatalf("failed download left partial data: %v", remaining)
			}
		})
	}
}

func TestChecksumsWorkWithoutOptionalGitHubDigest(t *testing.T) {
	binary := []byte("verified with mandatory SHA256SUMS")
	r, _ := fixtureRelease(binary)
	manifest := []byte(strings.ToUpper(digest(binary)) + " *" + r.Assets[0].Name + "\r\n")
	r.Assets[0].Digest, r.Assets[1].Digest = "", ""
	r.Assets[1].Size = int64(len(manifest))
	s := fixtureService(t, fixtureHandler(r, manifest, binary))
	found, err := s.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	download, err := s.Download(context.Background(), found, t.TempDir(), nil)
	if err != nil || download == nil || download.SHA256 != digest(binary) {
		t.Fatalf("SHA256SUMS fallback failed: %#v %v", download, err)
	}
	download.Cleanup()
}

func TestChunkedOversizeAndHTTPFailureLeaveNoDownload(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusPartialContent, http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			binary := []byte("good")
			r, manifest := fixtureRelease(binary)
			s := fixtureService(t, func(w http.ResponseWriter, req *http.Request) {
				if strings.HasSuffix(req.URL.Path, ".exe") {
					w.WriteHeader(status)
					w.(http.Flusher).Flush() // No Content-Length: exercise streaming limits.
					w.Write(append(binary, 'x'))
					return
				}
				fixtureHandler(r, manifest, binary)(w, req)
			})
			found, err := s.Check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			staging := t.TempDir()
			if got, err := s.Download(context.Background(), found, staging, nil); err == nil || got != nil {
				t.Fatalf("accepted bad download %#v %v", got, err)
			}
			files, _ := os.ReadDir(staging)
			if len(files) != 0 {
				t.Fatalf("partial files remain: %v", files)
			}
		})
	}
}

func TestAssetRedirectAllowlist(t *testing.T) {
	for _, tc := range []struct {
		target string
		good   bool
	}{
		{"https://release-assets.githubusercontent.com/github-production-release-asset/test?token=value", true},
		{"https://objects.githubusercontent.com/github-production-release-asset/test", true},
		{"https://github-releases.githubusercontent.com/test", true},
		{"https://attacker.test/file.exe", false},
		{"http://release-assets.githubusercontent.com/test", false},
		{"https://release-assets.githubusercontent.com.attacker.test/test", false},
		{"https://github.com/attacker/2FAManager/releases/download/v0.3.0/file.exe", false},
		{"https://github.com/example/2FAManager/releases/download/v0.3.0/other.exe", false},
	} {
		t.Run(tc.target, func(t *testing.T) {
			binary := []byte("good")
			r, manifest := fixtureRelease(binary)
			s := fixtureService(t, func(w http.ResponseWriter, req *http.Request) {
				if req.Host == "github.com" && strings.HasSuffix(req.URL.Path, ".exe") {
					http.Redirect(w, req, tc.target, http.StatusFound)
					return
				}
				fixtureHandler(r, manifest, binary)(w, req)
			})
			found, err := s.Check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Download(context.Background(), found, t.TempDir(), nil)
			if (tc.good && (got == nil || err != nil)) || (!tc.good && (got != nil || err == nil)) {
				t.Fatalf("redirect result: %#v %v", got, err)
			}
			if got != nil {
				got.Cleanup()
			}
		})
	}
}

func TestCancellationBusyGuardAndRetry(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	binary := []byte("good")
	r, manifest := fixtureRelease(binary)
	var hold atomic.Bool
	hold.Store(true)
	s := fixtureService(t, func(w http.ResponseWriter, req *http.Request) {
		if hold.Load() {
			once.Do(func() { close(entered) })
			<-req.Context().Done()
			return
		}
		fixtureHandler(r, manifest, binary)(w, req)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Check(ctx); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("check did not start")
	}
	if _, err := s.Check(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent check: %v", err)
	}
	if _, err := s.Download(context.Background(), nil, t.TempDir(), nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent download: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	hold.Store(false)
	if _, err := s.Check(context.Background()); err != nil {
		t.Fatalf("busy guard not released: %v", err)
	}
}

func TestCancelledDownloadCleansPartialFiles(t *testing.T) {
	binary := []byte(strings.Repeat("x", 100000))
	r, manifest := fixtureRelease(binary)
	started := make(chan struct{})
	s := fixtureService(t, func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, ".exe") {
			w.Header().Set("Content-Length", fmt.Sprint(len(binary)))
			w.Write(binary[:100])
			w.(http.Flusher).Flush()
			close(started)
			<-req.Context().Done()
			return
		}
		fixtureHandler(r, manifest, binary)(w, req)
	})
	found, err := s.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	staging := t.TempDir()
	go func() { _, err := s.Download(ctx, found, staging, nil); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	if _, err := s.Check(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("check allowed during download: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	remaining, _ := os.ReadDir(staging)
	if len(remaining) != 0 {
		t.Fatalf("cancelled download left partial data: %v", remaining)
	}
}

func TestUntrustedReleaseCannotBeDownloaded(t *testing.T) {
	s, err := New(Config{Owner: "example", Repo: "2FAManager", CurrentVersion: "0.3.0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Download(context.Background(), &Release{Version: "9.0.0"}, t.TempDir(), nil); err == nil {
		t.Fatal("accepted caller-forged release")
	}
	if _, err := New(Config{Owner: "../elsewhere", Repo: "repo", CurrentVersion: "0.3.0"}); err == nil {
		t.Fatal("accepted invalid repository owner")
	}
}
