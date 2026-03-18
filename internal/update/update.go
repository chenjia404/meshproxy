package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

var Version = "devel"

type Service struct {
	owner      string
	repo       string
	binaryName string
	client     *http.Client

	mu       sync.Mutex
	applying bool
}

type Info struct {
	CurrentVersion  string    `json:"current_version"`
	LatestVersion   string    `json:"latest_version"`
	UpdateAvailable bool      `json:"update_available"`
	AssetAvailable  bool      `json:"asset_available"`
	AssetName       string    `json:"asset_name,omitempty"`
	AssetURL        string    `json:"asset_url,omitempty"`
	ReleaseURL      string    `json:"release_url,omitempty"`
	PublishedAt     time.Time `json:"published_at,omitempty"`
	Notes           string    `json:"notes,omitempty"`
	OS              string    `json:"os"`
	Arch            string    `json:"arch"`

	assetDigest string
}

type ApplyResult struct {
	Info
	Status           string `json:"status"`
	RestartScheduled bool   `json:"restart_scheduled"`
}

type githubRelease struct {
	TagName     string               `json:"tag_name"`
	HTMLURL     string               `json:"html_url"`
	PublishedAt time.Time            `json:"published_at"`
	Body        string               `json:"body"`
	Assets      []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

func NewService(owner, repo, binaryName string) *Service {
	return &Service{
		owner:      owner,
		repo:       repo,
		binaryName: binaryName,
		client: &http.Client{
			Timeout: 45 * time.Second,
		},
	}
}

func CurrentVersion() string {
	if v := normalizeVersion(Version); v != "" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := normalizeVersion(info.Main.Version); v != "" {
			return v
		}
	}
	return "devel"
}

func (s *Service) Check(ctx context.Context) (Info, error) {
	if s == nil {
		return Info{}, errors.New("update service is nil")
	}
	release, err := s.fetchLatestRelease(ctx)
	if err != nil {
		return Info{}, err
	}
	currentVersion := CurrentVersion()
	latestVersion := normalizeVersion(release.TagName)
	info := Info{
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVersion,
		UpdateAvailable: compareVersion(latestVersion, currentVersion) > 0,
		ReleaseURL:      strings.TrimSpace(release.HTMLURL),
		PublishedAt:     release.PublishedAt,
		Notes:           strings.TrimSpace(release.Body),
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
	}
	asset, ok := selectAsset(release.Assets, s.binaryName, latestVersion)
	if ok {
		info.AssetAvailable = true
		info.AssetName = asset.Name
		info.AssetURL = asset.BrowserDownloadURL
		info.assetDigest = asset.Digest
	}
	return info, nil
}

func (s *Service) Apply(ctx context.Context) (ApplyResult, error) {
	if s == nil {
		return ApplyResult{}, errors.New("update service is nil")
	}
	s.mu.Lock()
	if s.applying {
		s.mu.Unlock()
		return ApplyResult{}, errors.New("update is already in progress")
	}
	s.applying = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.applying = false
		s.mu.Unlock()
	}()

	info, err := s.Check(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	result := ApplyResult{Info: info}
	if !info.UpdateAvailable {
		result.Status = "up_to_date"
		return result, nil
	}
	if !info.AssetAvailable || info.AssetURL == "" {
		return ApplyResult{}, fmt.Errorf("no release asset for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	exePath, err := os.Executable()
	if err != nil {
		return ApplyResult{}, err
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return ApplyResult{}, err
	}
	workDir := filepath.Dir(exePath)
	cleanupOldUpdateDirs(workDir)
	tmpDir, err := os.MkdirTemp(workDir, "meshproxy-update-*")
	if err != nil {
		return ApplyResult{}, err
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			log.Printf("[update] cleanup temp dir failed: %v", err)
		}
	}()
	archivePath := filepath.Join(tmpDir, filepath.Base(info.AssetName))
	if err := s.downloadAsset(ctx, info.AssetURL, archivePath, info.assetDigest); err != nil {
		return ApplyResult{}, err
	}
	extractedPath := filepath.Join(tmpDir, filepath.Base(exePath)+".new")
	if err := extractBinary(archivePath, extractedPath, filepath.Base(exePath)); err != nil {
		return ApplyResult{}, err
	}
	if runtime.GOOS == "windows" {
		if err := stageReplacement(extractedPath, exePath+".new"); err != nil {
			return ApplyResult{}, err
		}
		if err := launchWindowsUpdater(exePath, os.Args[1:]); err != nil {
			return ApplyResult{}, err
		}
		time.AfterFunc(1200*time.Millisecond, func() { os.Exit(0) })
		result.Status = "scheduled"
		result.RestartScheduled = true
		return result, nil
	}
	if err := replaceAndRestartUnix(extractedPath, exePath, os.Args[1:]); err != nil {
		return ApplyResult{}, err
	}
	time.AfterFunc(1200*time.Millisecond, func() { os.Exit(0) })
	result.Status = "scheduled"
	result.RestartScheduled = true
	return result, nil
}

func cleanupOldUpdateDirs(workDir string) {
	if workDir == "" {
		return
	}
	entries, err := os.ReadDir(workDir)
	if err != nil {
		log.Printf("[update] scan temp dirs failed: %v", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "meshproxy-update-") {
			continue
		}
		path := filepath.Join(workDir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			log.Printf("[update] remove stale temp dir %s failed: %v", path, err)
		}
	}
}

func (s *Service) fetchLatestRelease(ctx context.Context) (githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", s.owner, s.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "meshproxy-update-checker")
	resp, err := s.client.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return githubRelease{}, fmt.Errorf("github latest release failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, err
	}
	if normalizeVersion(release.TagName) == "" {
		return githubRelease{}, errors.New("github latest release is missing tag_name")
	}
	return release, nil
}

func selectAsset(assets []githubReleaseAsset, binaryName, version string) (githubReleaseAsset, bool) {
	wantExt := ".tar.gz"
	if runtime.GOOS == "windows" {
		wantExt = ".zip"
	}
	wantName := fmt.Sprintf("%s_%s_%s_%s%s", binaryName, version, runtime.GOOS, runtime.GOARCH, wantExt)
	for _, asset := range assets {
		if asset.Name == wantName {
			return asset, true
		}
	}
	return githubReleaseAsset{}, false
}

func (s *Service) downloadAsset(ctx context.Context, url, destPath, digest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "meshproxy-updater")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download release asset failed: %s", resp.Status)
	}
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), resp.Body); err != nil {
		return err
	}
	expected := strings.TrimSpace(strings.TrimPrefix(digest, "sha256:"))
	if expected != "" {
		actual := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(actual, expected) {
			return fmt.Errorf("downloaded asset checksum mismatch: want=%s got=%s", expected, actual)
		}
	}
	return nil
}

func extractBinary(archivePath, destPath, exeBase string) error {
	if strings.HasSuffix(strings.ToLower(archivePath), ".zip") {
		return extractZipBinary(archivePath, destPath, exeBase)
	}
	return extractTarGzBinary(archivePath, destPath, exeBase)
}

func extractZipBinary(archivePath, destPath, exeBase string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if !matchesBinaryName(file.Name, exeBase) {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeExecutable(destPath, rc, file.Mode())
	}
	return fmt.Errorf("binary %s not found in archive", exeBase)
}

func extractTarGzBinary(archivePath, destPath, exeBase string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		if !matchesBinaryName(hdr.Name, exeBase) {
			continue
		}
		mode := os.FileMode(0o755)
		if hdr.FileInfo() != nil {
			mode = hdr.FileInfo().Mode()
		}
		return writeExecutable(destPath, tr, mode)
	}
	return fmt.Errorf("binary %s not found in archive", exeBase)
}

func matchesBinaryName(entryName, exeBase string) bool {
	base := filepath.Base(entryName)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(base, exeBase)
	}
	return base == exeBase
}

func writeExecutable(destPath string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if mode == 0 {
		mode = 0o755
	}
	return os.Chmod(destPath, mode.Perm())
}

func stageReplacement(src, dest string) error {
	if err := os.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Chmod(dest, 0o755)
	}
	return nil
}

func replaceAndRestartUnix(extractedPath, exePath string, args []string) error {
	replacementPath := exePath + ".new"
	if err := stageReplacement(extractedPath, replacementPath); err != nil {
		return err
	}
	backupPath := exePath + ".old"
	_ = os.Remove(backupPath)
	if err := os.Rename(exePath, backupPath); err != nil {
		return err
	}
	if err := os.Rename(replacementPath, exePath); err != nil {
		_ = os.Rename(backupPath, exePath)
		return err
	}
	cmd := exec.Command(exePath, args...)
	cmd.Dir = filepath.Dir(exePath)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return err
	}
	return nil
}

func launchWindowsUpdater(exePath string, args []string) error {
	stagedPath := exePath + ".new"
	scriptPath := exePath + ".update.cmd"
	commandLine := quoteWindowsArgs(append([]string{exePath}, args...))
	script := strings.Join([]string{
		"@echo off",
		"setlocal",
		"ping 127.0.0.1 -n 3 >nul",
		"del /f /q \"" + exePath + ".old\" >nul 2>nul",
		"move /Y \"" + exePath + "\" \"" + exePath + ".old\" >nul",
		"move /Y \"" + stagedPath + "\" \"" + exePath + "\" >nul",
		"start \"\" " + commandLine,
		"del /f /q \"%~f0\" >nul 2>nul",
	}, "\r\n")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/C", scriptPath)
	cmd.Dir = filepath.Dir(exePath)
	return cmd.Start()
}

func quoteWindowsArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" {
			quoted = append(quoted, "\"\"")
			continue
		}
		if !strings.ContainsAny(arg, " \t\"") {
			quoted = append(quoted, arg)
			continue
		}
		quoted = append(quoted, "\""+strings.ReplaceAll(arg, "\"", "\\\"")+"\"")
	}
	return strings.Join(quoted, " ")
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" || v == "(devel)" || v == "devel" {
		return ""
	}
	return v
}

func compareVersion(version1, version2 string) int {
	version1 = normalizeVersion(version1)
	version2 = normalizeVersion(version2)
	if version1 == "" && version2 == "" {
		return 0
	}
	if version1 != "" && version2 == "" {
		return 1
	}
	if version1 == "" && version2 != "" {
		return 1
	}
	ver1Strs := strings.Split(version1, ".")
	ver2Strs := strings.Split(version2, ".")
	verLen := len(ver1Strs)
	if len(ver2Strs) > verLen {
		verLen = len(ver2Strs)
	}
	for i := 0; i < verLen; i++ {
		var ver1Int, ver2Int int
		if i < len(ver1Strs) {
			ver1Int, _ = strconv.Atoi(ver1Strs[i])
		}
		if i < len(ver2Strs) {
			ver2Int, _ = strconv.Atoi(ver2Strs[i])
		}
		if ver1Int < ver2Int {
			return -1
		}
		if ver1Int > ver2Int {
			return 1
		}
	}
	return 0
}
