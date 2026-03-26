package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/amurg-ai/amurg/runtime/internal/daemon"
)

const (
	githubRepo       = "amurg-ai/amurg"
	releasesEndpoint = "https://api.github.com/repos/" + githubRepo + "/releases/latest"
	binaryName       = "amurg-runtime"
)

// githubRelease is a minimal representation of a GitHub release.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

// githubAsset is a minimal representation of a GitHub release asset.
type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update amurg-runtime to the latest release",
		RunE:  runUpdate,
	}
	cmd.Flags().Bool("force", false, "update even if already on latest version")
	cmd.Flags().Bool("check", false, "only check for updates, don't download")
	return cmd
}

func runUpdate(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	check, _ := cmd.Flags().GetBool("check")

	// Fetch latest release info from GitHub.
	release, err := fetchLatestRelease(http.DefaultClient, releasesEndpoint)
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}

	latest := normalizeVersion(release.TagName)
	current := normalizeVersion(version)

	fmt.Fprintf(os.Stdout, "Current version: %s\n", current)
	fmt.Fprintf(os.Stdout, "Latest version:  %s\n", latest)

	if check {
		if current == latest {
			fmt.Fprintln(os.Stdout, "You are up to date.")
		} else {
			fmt.Fprintln(os.Stdout, "Update available.")
		}
		return nil
	}

	if current == latest && !force {
		fmt.Fprintln(os.Stdout, "Already up to date. Use --force to reinstall.")
		return nil
	}

	// Determine current executable path early so we can fail before downloading.
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("determine executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve executable symlinks: %w", err)
	}

	// Check that the binary location is writable.
	if err := checkWritable(execPath); err != nil {
		return fmt.Errorf("binary at %s is not writable (installed via package manager?): %w", execPath, err)
	}

	// Find the correct archive asset and checksums asset.
	archiveName := buildArchiveName(latest, runtime.GOOS, runtime.GOARCH)
	var archiveURL, checksumsURL string
	for _, a := range release.Assets {
		switch a.Name {
		case archiveName:
			archiveURL = a.BrowserDownloadURL
		case "checksums.txt":
			checksumsURL = a.BrowserDownloadURL
		}
	}
	if archiveURL == "" {
		return fmt.Errorf("no release asset found for %s", archiveName)
	}
	if checksumsURL == "" {
		return fmt.Errorf("checksums.txt not found in release assets")
	}

	// Download checksums and parse the expected hash.
	fmt.Fprintln(os.Stdout, "Downloading checksums...")
	checksumsBody, err := httpGet(http.DefaultClient, checksumsURL)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	expectedHash, err := parseChecksum(string(checksumsBody), archiveName)
	if err != nil {
		return err
	}

	// Download the archive to a temp file.
	fmt.Fprintf(os.Stdout, "Downloading %s...\n", archiveName)
	archiveData, err := httpGet(http.DefaultClient, archiveURL)
	if err != nil {
		return fmt.Errorf("download archive: %w", err)
	}

	// Verify checksum.
	actualHash := sha256Hex(archiveData)
	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, actualHash)
	}
	fmt.Fprintln(os.Stdout, "Checksum verified.")

	// Extract the binary from the archive.
	var newBinary []byte
	if strings.HasSuffix(archiveName, ".zip") {
		newBinary, err = extractFromZip(archiveData, binaryName+".exe")
	} else {
		newBinary, err = extractFromTarGz(archiveData, binaryName)
	}
	if err != nil {
		return fmt.Errorf("extract binary: %w", err)
	}

	// Replace the current binary atomically.
	if err := replaceBinary(execPath, newBinary); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Updated amurg-runtime: %s → %s\n", current, latest)

	// Warn if daemon is running.
	if pid, err := daemon.ReadPID(); err == nil && pid != 0 && daemon.IsRunning(pid) {
		fmt.Fprintf(os.Stdout, "⚠ The runtime daemon (PID %d) is still running the old version.\n", pid)
		fmt.Fprintln(os.Stdout, "  Run 'amurg-runtime stop && amurg-runtime start' to restart with the new version.")
	}

	return nil
}

// fetchLatestRelease fetches the latest release metadata from GitHub.
func fetchLatestRelease(client *http.Client, endpoint string) (*githubRelease, error) {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "amurg-runtime/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, endpoint)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("parse release JSON: %w", err)
	}
	return &rel, nil
}

// httpGet performs a GET request and returns the response body.
func httpGet(client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "amurg-runtime/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// normalizeVersion strips a leading "v" prefix for comparison.
func normalizeVersion(v string) string {
	return strings.TrimPrefix(v, "v")
}

// buildArchiveName returns the expected archive filename for a given version,
// OS, and architecture.
func buildArchiveName(ver, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s.%s", binaryName, ver, goos, goarch, ext)
}

// parseChecksum finds the SHA256 hash for the given filename in a
// goreleaser-format checksums file (one "hash  filename" per line).
func parseChecksum(checksums, filename string) (string, error) {
	for _, line := range strings.Split(checksums, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == filename {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("checksum for %s not found in checksums.txt", filename)
}

// sha256Hex returns the lowercase hex-encoded SHA256 hash of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// extractFromTarGz extracts the named file from a tar.gz archive.
func extractFromTarGz(data []byte, name string) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		// Match the base name — archives may contain a directory prefix.
		if filepath.Base(hdr.Name) == name && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", name)
}

// extractFromZip extracts the named file from a zip archive held in memory.
func extractFromZip(data []byte, name string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if filepath.Base(f.Name) == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", name)
}

// checkWritable verifies that the directory containing path is writable,
// which is sufficient for an atomic rename-based replacement. We check the
// directory instead of the file itself because the running binary cannot be
// opened for writing on Linux ("text file busy").
func checkWritable(path string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".amurg-write-check-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	tmp.Close()
	return os.Remove(name)
}

// replaceBinary atomically replaces the binary at dst with the contents of
// newBinary. It writes to a temp file in the same directory, then renames.
func replaceBinary(dst string, newBinary []byte) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".amurg-runtime-update-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(newBinary); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
