// Package updater checks public GitHub Releases and downloads verified updates.
// It never replaces an installed executable or reads the user's token data.
package updater

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

const (
	maxAPIBytes      int64 = 2 << 20
	maxManifestBytes int64 = 512 << 10
	MaxDownloadBytes int64 = 256 << 20
	checksumsName          = "SHA256SUMS"
	apiVersion             = "2026-03-10"
)

var (
	ErrBusy  = errors.New("正在检查或下载更新，请稍后重试")
	repoPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

type Config struct {
	Owner          string
	Repo           string
	CurrentVersion string
	// Client is optional. Its Transport may be supplied for tests. Production
	// defaults preserve Go's standard HTTP_PROXY / HTTPS_PROXY / NO_PROXY support.
	// Redirect restrictions remain active for a supplied client.
	Client *http.Client
}

type Service struct {
	config  Config
	current version
	client  *http.Client
	busy    atomic.Bool
}

type Release struct {
	Version     string `json:"version"`
	Tag         string `json:"tag"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"publishedAt"`
	URL         string `json:"url"`
	AssetName   string `json:"assetName"`
	AssetSize   int64  `json:"assetSize"`
	// Download uses this private snapshot, never editable display fields or URLs
	// received from a frontend. Only releases returned by this service are valid.
	service        *Service
	packageVersion string
	asset          githubAsset
	manifest       githubAsset
}

type Progress struct {
	Downloaded int64 `json:"downloaded"`
	Total      int64 `json:"total"`
	Percent    int   `json:"percent"`
}

type Download struct {
	Path    string
	SHA256  string
	Version string
	dir     string
}

// Cleanup removes only the private staging directory created for this download.
// Call it after the installer has copied the verified executable to its own job.
func (d *Download) Cleanup() error {
	if d == nil || d.dir == "" {
		return nil
	}
	return os.RemoveAll(d.dir)
}

type githubAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	State  string `json:"state"`
	Digest string `json:"digest"`
}

type githubRelease struct {
	Tag         string        `json:"tag_name"`
	Body        string        `json:"body"`
	PublishedAt string        `json:"published_at"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []githubAsset `json:"assets"`
}

func New(config Config) (*Service, error) {
	if !repoPart.MatchString(config.Owner) || !repoPart.MatchString(config.Repo) {
		return nil, errors.New("未正确配置 GitHub 更新仓库")
	}
	current, err := parseVersion(config.CurrentVersion)
	if err != nil {
		return nil, err
	}
	client := &http.Client{}
	if config.Client != nil {
		copy := *config.Client
		client = &copy
	}
	previousRedirect := client.CheckRedirect
	s := &Service{config: config, current: current, client: client}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("更新下载重定向次数过多")
		}
		if !s.allowedRedirect(req.URL, via) {
			return errors.New("更新下载跳转到了未允许的地址")
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	return s, nil
}

// AssetName is the required Windows amd64 release asset name (without a v prefix).
func AssetName(v string) string {
	return "Luma-" + strings.TrimPrefix(v, "v") + "-windows-amd64.exe"
}

func (s *Service) repositoryPath() string {
	return "/" + s.config.Owner + "/" + s.config.Repo
}

func (s *Service) apiURL() string {
	return "https://api.github.com/repos" + s.repositoryPath() + "/releases/latest"
}

func secureURL(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && u.Fragment == "" && u.Opaque == ""
}

func (s *Service) allowedRedirect(u *url.URL, via []*http.Request) bool {
	if !secureURL(u) || len(via) == 0 {
		return false
	}
	if via[0].URL.Host == "api.github.com" {
		// Repository renames require an application release updating its configured
		// source; an API redirect cannot silently replace the trusted repository.
		return u.String() == s.apiURL()
	}
	switch u.Host {
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return true
	case "github.com":
		// A GitHub redirect may add a query, but cannot switch repository or asset.
		return u.EscapedPath() == via[0].URL.EscapedPath()
	default:
		return false
	}
}

func (s *Service) validateAsset(a githubAsset, tag string, limit int64) error {
	if a.State != "uploaded" || a.Size <= 0 || a.Size > limit {
		return fmt.Errorf("更新文件 %s 的状态或大小无效", a.Name)
	}
	u, err := url.Parse(a.URL)
	expected := s.repositoryPath() + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(a.Name)
	if err != nil || !secureURL(u) || u.Host != "github.com" || u.RawQuery != "" || u.EscapedPath() != expected {
		return fmt.Errorf("更新文件 %s 的下载地址不属于本程序的 GitHub 发布", a.Name)
	}
	if a.Digest != "" {
		if !strings.HasPrefix(a.Digest, "sha256:") || !validHash(strings.TrimPrefix(a.Digest, "sha256:")) {
			return fmt.Errorf("更新文件 %s 的 GitHub 校验值无效", a.Name)
		}
	}
	return nil
}

func (s *Service) request(ctx context.Context, address, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LumaAuthenticator/"+s.config.CurrentVersion)
	req.Header.Set("Accept", accept)
	if req.URL.Host == "api.github.com" {
		req.Header.Set("X-GitHub-Api-Version", apiVersion)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 GitHub 更新服务失败：%w", err)
	}
	return resp, nil
}

func statusError(resp *http.Response) error {
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("GitHub 暂时限制了请求，请稍后再试（HTTP %d）", resp.StatusCode)
	}
	return fmt.Errorf("GitHub 更新请求失败（HTTP %d）", resp.StatusCode)
}

func readLimited(resp *http.Response, limit int64) ([]byte, error) {
	if resp.ContentLength > limit {
		return nil, errors.New("更新响应超过允许的大小")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("更新响应超过允许的大小")
	}
	return data, nil
}

// Check returns nil if no release exists or the installed version is current.
// Prereleases, drafts, downgrades, and releases without verified assets cannot
// become install candidates. A newer release missing assets reports an error.
func (s *Service) Check(ctx context.Context) (*Release, error) {
	if !s.busy.CompareAndSwap(false, true) {
		return nil, ErrBusy
	}
	defer s.busy.Store(false)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := s.request(ctx, s.apiURL(), "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}
	data, err := readLimited(resp, maxAPIBytes)
	if err != nil {
		return nil, fmt.Errorf("读取更新信息失败：%w", err)
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return nil, errors.New("GitHub 返回的更新信息无效")
	}
	v, err := parseVersion(release.Tag)
	if err != nil {
		return nil, err
	}
	if release.Draft || release.Prerelease || len(v.pre) != 0 || v.compare(s.current) <= 0 {
		return nil, nil
	}
	assetName := AssetName(release.Tag)
	var binary, manifest *githubAsset
	for _, asset := range release.Assets {
		switch asset.Name {
		case assetName:
			if binary != nil {
				return nil, errors.New("发布中存在重复的更新文件")
			}
			copy := asset
			binary = &copy
		case checksumsName:
			if manifest != nil {
				return nil, errors.New("发布中存在重复的校验文件")
			}
			copy := asset
			manifest = &copy
		}
	}
	if binary == nil || manifest == nil {
		return nil, errors.New("此版本缺少 Windows 更新文件或 SHA256SUMS 校验文件")
	}
	if err := s.validateAsset(*binary, release.Tag, MaxDownloadBytes); err != nil {
		return nil, err
	}
	if err := s.validateAsset(*manifest, release.Tag, maxManifestBytes); err != nil {
		return nil, err
	}
	return &Release{
		Version: strings.TrimPrefix(release.Tag, "v"), Tag: release.Tag,
		Notes: release.Body, PublishedAt: release.PublishedAt,
		URL:       "https://github.com" + s.repositoryPath() + "/releases/tag/" + url.PathEscape(release.Tag),
		AssetName: assetName, AssetSize: binary.Size,
		service: s, packageVersion: strings.TrimPrefix(release.Tag, "v"), asset: *binary, manifest: *manifest,
	}, nil
}

func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func manifestHash(data []byte, name string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), int(maxManifestBytes))
	var found string
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) < 67 || !validHash(line[:64]) || line[64] != ' ' || (line[65] != ' ' && line[65] != '*') {
			return "", errors.New("SHA256SUMS 文件格式无效")
		}
		if line[66:] == name {
			if found != "" {
				return "", errors.New("SHA256SUMS 存在重复的更新文件校验值")
			}
			found = strings.ToLower(line[:64])
		}
	}
	if err := scanner.Err(); err != nil {
		return "", errors.New("SHA256SUMS 文件格式无效")
	}
	if found == "" {
		return "", errors.New("SHA256SUMS 未包含当前更新文件")
	}
	return found, nil
}

func verifyAPIDigest(digest, actual string) error {
	if digest != "" && !strings.EqualFold(digest, "sha256:"+actual) {
		return errors.New("更新文件与 GitHub 的 SHA-256 校验值不匹配")
	}
	return nil
}

// Download fetches and verifies SHA256SUMS before creating an isolated staging
// directory. Failed/cancelled downloads remove their own partial files. Callers
// must still arrange an explicit application exit before replacing its EXE.
func (s *Service) Download(ctx context.Context, release *Release, stagingDir string, progress func(Progress)) (*Download, error) {
	if !s.busy.CompareAndSwap(false, true) {
		return nil, ErrBusy
	}
	defer s.busy.Store(false)
	if release == nil || release.service != s || release.packageVersion == "" {
		return nil, errors.New("请先检查并选择有效的更新")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	resp, err := s.request(ctx, release.manifest.URL, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	var manifest []byte
	if resp.StatusCode != http.StatusOK {
		err = statusError(resp)
	} else {
		manifest, err = readLimited(resp, maxManifestBytes)
	}
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("读取更新校验文件失败：%w", err)
	}
	if int64(len(manifest)) != release.manifest.Size {
		return nil, errors.New("更新校验文件大小与发布信息不一致")
	}
	manifestDigest := sha256.Sum256(manifest)
	if err := verifyAPIDigest(release.manifest.Digest, hex.EncodeToString(manifestDigest[:])); err != nil {
		return nil, err
	}
	expected, err := manifestHash(manifest, release.asset.Name)
	if err != nil {
		return nil, err
	}
	if err := verifyAPIDigest(release.asset.Digest, expected); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stagingDir == "" {
		return nil, errors.New("未提供更新暂存目录")
	}
	if err := os.MkdirAll(stagingDir, 0700); err != nil {
		return nil, fmt.Errorf("无法创建更新暂存目录：%w", err)
	}
	dir, err := os.MkdirTemp(stagingDir, "luma-download-")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(dir)
		}
	}()
	partial := filepath.Join(dir, release.asset.Name+".part")
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	resp, err = s.request(ctx, release.asset.URL, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != release.asset.Size {
		return nil, errors.New("更新文件大小与发布信息不一致")
	}
	hash := sha256.New()
	var downloaded int64
	lastProgress := time.Time{}
	report := func(force bool) {
		if progress != nil && (force || time.Since(lastProgress) >= 100*time.Millisecond) {
			progress(Progress{Downloaded: downloaded, Total: release.asset.Size, Percent: int(downloaded * 100 / release.asset.Size)})
			lastProgress = time.Now()
		}
	}
	report(true)
	buffer := make([]byte, 64<<10)
	reader := io.LimitReader(resp.Body, release.asset.Size+1)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := reader.Read(buffer)
		if n > 0 {
			downloaded += int64(n)
			if downloaded > release.asset.Size {
				return nil, errors.New("更新文件超过声明的大小")
			}
			if _, err := file.Write(buffer[:n]); err != nil {
				return nil, err
			}
			hash.Write(buffer[:n])
			report(false)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("下载更新中断：%w", readErr)
		}
	}
	if downloaded != release.asset.Size {
		return nil, errors.New("更新文件不完整，请重新下载")
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return nil, errors.New("更新文件 SHA-256 校验失败，请重新下载")
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, release.asset.Name)
	if err := os.Rename(partial, path); err != nil {
		return nil, err
	}
	report(true)
	keep = true
	return &Download{Path: path, SHA256: actual, Version: release.packageVersion, dir: dir}, nil
}
