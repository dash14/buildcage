package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This is the only test in the package that runs the real rsync binary.
// Everything else replaces runRsync with a fake so `go test` never depends
// on rsync being installed; this test instead skips when it isn't, or when
// it's a variant that doesn't support -aHAX (macOS ships openrsync, which
// doesn't; the production image installs GNU rsync via apk).
func TestRealRsyncMirrorsAndWritesBack(t *testing.T) {
	if out, err := exec.Command("rsync", "--version").CombinedOutput(); err != nil || strings.Contains(string(out), "openrsync") {
		t.Skip("no GNU-compatible rsync available")
	}

	hostDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostDir, "bundle.pem"), []byte("ORIGINAL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("bundle.pem", filepath.Join(hostDir, "alias.pem")); err != nil {
		t.Fatal(err)
	}

	scratchDir := t.TempDir()
	if err := mirrorDir(hostDir, scratchDir); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(scratchDir, "bundle.pem")); err != nil || string(got) != "ORIGINAL\n" {
		t.Fatalf("mirror did not copy the bundle: %q, %v", got, err)
	}
	if target, err := os.Readlink(filepath.Join(scratchDir, "alias.pem")); err != nil || target != "bundle.pem" {
		t.Fatalf("mirror did not preserve the symlink: %q, %v", target, err)
	}

	if err := os.WriteFile(filepath.Join(scratchDir, "bundle.pem"), []byte("CHANGED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(scratchDir, "alias.pem")); err != nil {
		t.Fatal(err)
	}

	if err := writeBack(scratchDir, hostDir); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(hostDir, "bundle.pem")); err != nil || string(got) != "CHANGED\n" {
		t.Fatalf("write-back did not apply the change: %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(hostDir, "alias.pem")); !os.IsNotExist(err) {
		t.Fatalf("write-back did not delete alias.pem: %v", err)
	}
}
