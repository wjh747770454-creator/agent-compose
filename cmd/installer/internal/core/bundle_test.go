package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyChecksum(t *testing.T) {
	data := []byte("bundle")
	sum := sha256.Sum256(data)
	checksums := []byte(fmt.Sprintf("%x  %s\n", sum, bundleAsset))
	if err := verifyChecksum(bundleAsset, data, checksums); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(bundleAsset, []byte("changed"), checksums); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestExtractBundleRejectsUnexpectedPath(t *testing.T) {
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	content := []byte("unsafe")
	if err := tarWriter.WriteHeader(&tar.Header{Name: "../outside", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractBundle(archive.Bytes(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "unexpected file") {
		t.Fatalf("extractBundle error = %v", err)
	}
}

func TestOpenBundleRejectsUnknownPayloadVersion(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "docker-compose.yml"), "services: {}\n", 0o644)
	writeTestFile(t, filepath.Join(dir, ".env.example"), "AUTH_PASSWORD=\n", 0o644)
	writeTestFile(t, filepath.Join(dir, "images", "manifest.env"), "INSTALLER_PAYLOAD_VERSION=2\n", 0o644)
	if _, err := openBundle(dir, nil); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("openBundle error = %v", err)
	}
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
