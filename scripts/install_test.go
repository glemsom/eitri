package scripts_test

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallScript_InstallsVerifiedRelease(t *testing.T) {
	releaseDir := t.TempDir()
	writeReleaseFixture(t, releaseDir, "new-binary", checksumModeValid)

	homeDir := t.TempDir()
	output, err := runInstallScript(t, releaseDir, homeDir, runInstallOptions{})
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}

	installedPath := filepath.Join(homeDir, ".local", "bin", "eitri")
	installed, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if string(installed) != "new-binary" {
		t.Fatalf("installed binary = %q, want %q", string(installed), "new-binary")
	}
	if !strings.Contains(output, "Release: v9.9.9") {
		t.Fatalf("output missing parsed release tag:\n%s", output)
	}
	if !strings.Contains(output, "Verifying SHA256 checksum...") {
		t.Fatalf("output missing checksum verification message:\n%s", output)
	}
}

func TestInstallScript_FailsOnChecksumMismatchWithoutOverwritingExistingBinary(t *testing.T) {
	releaseDir := t.TempDir()
	writeReleaseFixture(t, releaseDir, "new-binary", checksumModeMismatch)

	homeDir := t.TempDir()
	installedPath := filepath.Join(homeDir, ".local", "bin", "eitri")
	if err := os.MkdirAll(filepath.Dir(installedPath), 0o755); err != nil {
		t.Fatalf("mkdir install dir: %v", err)
	}
	if err := os.WriteFile(installedPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("seed existing binary: %v", err)
	}

	output, err := runInstallScript(t, releaseDir, homeDir, runInstallOptions{})
	if err == nil {
		t.Fatalf("install.sh succeeded unexpectedly\n%s", output)
	}
	if !strings.Contains(output, "Error: SHA256 verification failed.") {
		t.Fatalf("output missing checksum failure:\n%s", output)
	}

	installed, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if string(installed) != "old-binary" {
		t.Fatalf("installed binary overwritten: got %q, want %q", string(installed), "old-binary")
	}
}

func TestInstallScript_FailsWhenChecksumsMissing(t *testing.T) {
	releaseDir := t.TempDir()
	writeReleaseFixture(t, releaseDir, "new-binary", checksumModeMissing)

	homeDir := t.TempDir()
	output, err := runInstallScript(t, releaseDir, homeDir, runInstallOptions{failChecksumDownload: true})
	if err == nil {
		t.Fatalf("install.sh succeeded unexpectedly\n%s", output)
	}
	if !strings.Contains(output, "Error: checksums.txt not found") {
		t.Fatalf("output missing missing-checksum failure:\n%s", output)
	}

	installedPath := filepath.Join(homeDir, ".local", "bin", "eitri")
	if _, err := os.Stat(installedPath); !os.IsNotExist(err) {
		t.Fatalf("installed binary exists unexpectedly: stat err = %v", err)
	}
}

type checksumMode string

const (
	checksumModeValid    checksumMode = "valid"
	checksumModeMismatch checksumMode = "mismatch"
	checksumModeMissing  checksumMode = "missing"
)

type runInstallOptions struct {
	failChecksumDownload bool
}

func runInstallScript(t *testing.T, releaseDir, homeDir string, opts runInstallOptions) (string, error) {
	t.Helper()

	shimDir := t.TempDir()
	writeExecutable(t, filepath.Join(shimDir, "curl"), fakeCurlScript)

	cmd := exec.Command("bash", "install.sh")
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_RELEASE_DIR="+releaseDir,
		"FAKE_RELEASE_TAG=v9.9.9",
	)
	if opts.failChecksumDownload {
		cmd.Env = append(cmd.Env, "FAKE_FAIL_CHECKSUM_DOWNLOAD=1")
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeReleaseFixture(t *testing.T, dir, binaryContent string, mode checksumMode) {
	t.Helper()

	tarballPath := filepath.Join(dir, "eitri-linux-amd64.tar.gz")
	writeTarball(t, tarballPath, "eitri", []byte(binaryContent))

	if mode == checksumModeMissing {
		return
	}

	hash := fileSHA256(t, tarballPath)
	if mode == checksumModeMismatch {
		hash = strings.Repeat("0", len(hash))
	}
	checksums := hash + "  eitri-linux-amd64.tar.gz\n"
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(checksums), 0o644); err != nil {
		t.Fatalf("write checksums.txt: %v", err)
	}
}

func writeTarball(t *testing.T, tarballPath, name string, content []byte) {
	t.Helper()

	file, err := os.Create(tarballPath)
	if err != nil {
		t.Fatalf("create tarball: %v", err)
	}
	defer file.Close()

	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	header := &tar.Header{
		Name: name,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file for sha256: %v", err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

const fakeCurlScript = `#!/usr/bin/env bash
set -euo pipefail

output=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      output="$2"
      shift 2
      ;;
    -*)
      shift
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done

case "$url" in
  https://api.github.com/repos/*/releases/latest)
    printf '{"tag_name":"%s"}\n' "${FAKE_RELEASE_TAG}"
    ;;
  */eitri-linux-amd64.tar.gz)
    cp "${FAKE_RELEASE_DIR}/eitri-linux-amd64.tar.gz" "$output"
    ;;
  */checksums.txt)
    if [ "${FAKE_FAIL_CHECKSUM_DOWNLOAD:-0}" = "1" ]; then
      exit 22
    fi
    cp "${FAKE_RELEASE_DIR}/checksums.txt" "$output"
    ;;
  *)
    echo "unexpected curl URL: $url" >&2
    exit 1
    ;;
esac
`
