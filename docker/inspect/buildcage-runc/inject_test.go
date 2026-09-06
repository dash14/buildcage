package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newBundle lays out a minimal OCI bundle: a rootfs carrying one system CA
// candidate and a config.json with the given process env.
func newBundle(t *testing.T, env []string) (bundle, rootfs string) {
	t.Helper()
	bundle = t.TempDir()
	rootfs = filepath.Join(bundle, "rootfs")
	if err := os.MkdirAll(filepath.Join(rootfs, "etc", "ssl", "certs"), 0o755); err != nil {
		t.Fatal(err)
	}
	systemStore := filepath.Join(rootfs, "etc", "ssl", "certs", "ca-certificates.crt")
	if err := os.WriteFile(systemStore, []byte("ORIGINAL-ROOTS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{
		"root":    map[string]any{"path": "rootfs"},
		"process": map[string]any{"env": toAny(env)},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "config.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return bundle, rootfs
}

func toAny(env []string) []any {
	out := make([]any, len(env))
	for i, e := range env {
		out[i] = e
	}
	return out
}

// newBundleNoStore lays out a bundle with no CA store at all under
// rootfs/etc/ssl/certs — the node:*-slim shape: no OS trust store, only
// Node's own bundled roots, which findSystemStore cannot find.
func newBundleNoStore(t *testing.T, env []string) (bundle, rootfs string) {
	t.Helper()
	bundle = t.TempDir()
	rootfs = filepath.Join(bundle, "rootfs")
	// /etc exists, as it does in any real base image (passwd, hostname, ...);
	// only etc/ssl/certs and its siblings are absent, which is what actually
	// makes findSystemStore fail.
	if err := os.MkdirAll(filepath.Join(rootfs, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{
		"root":    map[string]any{"path": "rootfs"},
		"process": map[string]any{"env": toAny(env)},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "config.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return bundle, rootfs
}

func loadEnv(t *testing.T, bundle string) map[string]string {
	t.Helper()
	s, err := loadSpec(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return s.env
}

func loadMounts(t *testing.T, bundle string) []map[string]any {
	t.Helper()
	s, err := loadSpec(bundle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := s.raw["mounts"].([]any)
	mounts := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		if entry, ok := m.(map[string]any); ok {
			mounts = append(mounts, entry)
		}
	}
	return mounts
}

func findMount(t *testing.T, mounts []map[string]any, dest string) map[string]any {
	t.Helper()
	for _, m := range mounts {
		if m["destination"] == dest {
			return m
		}
	}
	t.Fatalf("no mount for %s", dest)
	return nil
}

// Additive variables (NODE_EXTRA_CA_CERTS, DENO_CERT) get their own file
// holding only the proxy's CA, so a tool's built-in bundle stays intact.
// Replacing variables (REQUESTS_CA_BUNDLE, PIP_CERT, SSL_CERT_FILE) get
// pointed at the system store instead, which already carries both.
func TestInjectSetsEachUnsetVariableAccordingToItsKind(t *testing.T) {
	useFakeRsync(t)
	bundle, rootfs := newBundle(t, []string{"PATH=/usr/bin"})

	restore, err := inject(bundle, []byte("BUILDCAGE-CA"))
	if err != nil {
		t.Fatal(err)
	}
	defer restore()

	env := loadEnv(t, bundle)

	for _, additive := range []string{"NODE_EXTRA_CA_CERTS", "DENO_CERT"} {
		if env[additive] != ownCAPath {
			t.Errorf("%s = %q, want %q", additive, env[additive], ownCAPath)
		}
	}
	own, err := os.ReadFile(filepath.Join(rootfs, strings.TrimPrefix(ownCAPath, "/")))
	if err != nil {
		t.Fatal(err)
	}
	if string(own) != "BUILDCAGE-CA" {
		t.Fatalf("own CA file = %q", own)
	}

	systemStorePath := "/etc/ssl/certs/ca-certificates.crt"
	for _, replacing := range []string{"REQUESTS_CA_BUNDLE", "PIP_CERT", "SSL_CERT_FILE"} {
		if env[replacing] != systemStorePath {
			t.Errorf("%s = %q, want %q", replacing, env[replacing], systemStorePath)
		}
	}

	if _, set := env["CURL_CA_BUNDLE"]; set {
		t.Error("CURL_CA_BUNDLE should be left unset; curl already reads the system store")
	}

	// The CA goes into the scratch mirror bind-mounted over the store's
	// directory, never into the real rootfs directly.
	mount := findMount(t, loadMounts(t, bundle), "/etc/ssl/certs")
	scratchDir, _ := mount["source"].(string)
	mirrored, err := os.ReadFile(filepath.Join(scratchDir, "ca-certificates.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mirrored), "BUILDCAGE-CA") || !strings.HasPrefix(string(mirrored), "ORIGINAL-ROOTS") {
		t.Fatalf("scratch mirror not patched correctly: %q", mirrored)
	}

	store, err := os.ReadFile(filepath.Join(rootfs, "etc", "ssl", "certs", "ca-certificates.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(store) != "ORIGINAL-ROOTS\n" {
		t.Fatalf("the real store must stay untouched until the step changes it: %q", store)
	}
}

// A step that already points a variable somewhere of its own keeps that
// choice; the CA is appended to that file instead of the variable being
// redirected, so whatever the author put there is not discarded.
func TestInjectAppendsToAnAlreadySetVariableInstead(t *testing.T) {
	useFakeRsync(t)
	bundle, rootfs := newBundle(t, []string{"DENO_CERT=/custom/roots.pem"})
	if err := os.MkdirAll(filepath.Join(rootfs, "custom"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootfs, "custom", "roots.pem"), []byte("CUSTOM\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	restore, err := inject(bundle, []byte("BUILDCAGE-CA"))
	if err != nil {
		t.Fatal(err)
	}
	defer restore()

	env := loadEnv(t, bundle)
	if env["DENO_CERT"] != "/custom/roots.pem" {
		t.Fatalf("DENO_CERT was redirected to %q", env["DENO_CERT"])
	}

	mount := findMount(t, loadMounts(t, bundle), "/custom")
	scratchDir, _ := mount["source"].(string)
	custom, err := os.ReadFile(filepath.Join(scratchDir, "roots.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(custom), "BUILDCAGE-CA") {
		t.Fatal("the CA was not appended to the custom file")
	}

	real, err := os.ReadFile(filepath.Join(rootfs, "custom", "roots.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if string(real) != "CUSTOM\n" {
		t.Fatalf("the real file must stay untouched until the step changes it: %q", real)
	}
}

// A step that never touches the store leaves the real rootfs file alone:
// finish() finds the scratch mirror unchanged from its post-injection
// baseline and never writes back.
func TestInjectFinishLeavesAnUntouchedStoreAlone(t *testing.T) {
	useFakeRsync(t)
	bundle, rootfs := newBundle(t, []string{"PATH=/usr/bin"})

	restore, err := inject(bundle, []byte("BUILDCAGE-CA"))
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}

	store, err := os.ReadFile(filepath.Join(rootfs, "etc", "ssl", "certs", "ca-certificates.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(store) != "ORIGINAL-ROOTS\n" {
		t.Fatalf("the real store was written to even though nothing changed: %q", store)
	}
	if _, err := os.Stat(filepath.Join(rootfs, strings.TrimPrefix(ownCAPath, "/"))); !os.IsNotExist(err) {
		t.Fatalf("own CA file still present: %v", err)
	}
}

// A step that regenerates the store (e.g. apt-get install --reinstall
// ca-certificates) gets its change mirrored back onto the real rootfs.
func TestInjectWritesBackWhenTheStepChangesTheStore(t *testing.T) {
	useFakeRsync(t)
	bundle, rootfs := newBundle(t, []string{"PATH=/usr/bin"})

	restore, err := inject(bundle, []byte("BUILDCAGE-CA"))
	if err != nil {
		t.Fatal(err)
	}

	mount := findMount(t, loadMounts(t, bundle), "/etc/ssl/certs")
	scratchDir, _ := mount["source"].(string)
	if err := os.WriteFile(filepath.Join(scratchDir, "ca-certificates.crt"), []byte("REGENERATED\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls int
	orig := runRsync
	runRsync = func(args []string) ([]byte, error) {
		calls++
		return orig(args)
	}
	defer func() { runRsync = orig }()

	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("got %d rsync invocations for the write-back, want 2 (dry run, then apply)", calls)
	}

	got, err := os.ReadFile(filepath.Join(rootfs, "etc", "ssl", "certs", "ca-certificates.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "REGENERATED\n" {
		t.Fatalf("write-back did not reach the real store: %q", got)
	}
}

// A failure applying the write-back must fail the build rather than ship a
// half-written layer.
func TestInjectWriteBackFailurePropagates(t *testing.T) {
	useFakeRsync(t)
	bundle, _ := newBundle(t, []string{"PATH=/usr/bin"})

	restore, err := inject(bundle, []byte("BUILDCAGE-CA"))
	if err != nil {
		t.Fatal(err)
	}

	mount := findMount(t, loadMounts(t, bundle), "/etc/ssl/certs")
	scratchDir, _ := mount["source"].(string)
	if err := os.WriteFile(filepath.Join(scratchDir, "ca-certificates.crt"), []byte("REGENERATED\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls int
	orig := runRsync
	runRsync = func(args []string) ([]byte, error) {
		calls++
		if calls == 2 { // the apply, right after a successful dry run
			return []byte("boom"), fmt.Errorf("simulated failure")
		}
		return orig(args)
	}
	defer func() { runRsync = orig }()

	if err := restore(); err == nil {
		t.Fatal("expected the write-back failure to propagate")
	}
}

// A base image with no CA store of its own (node:*-slim before
// ca-certificates is installed, or a scratch/distroless image) must not lose
// every variable just because the system store is missing: all six fall back
// to the same proxy-CA-only file. This is the corepack/node:22-slim and
// apt-install-then-curl/debian:bookworm-slim failure modes under the inspect
// engine — see docs/inspect-engine.md's "No system CA store" for what this
// fallback does and does not cover (ordinary MITM'd traffic works; a
// passthrough connection's real certificate still does not verify).
func TestInjectWithoutSystemStoreFallsBackToOwnCAForEveryVariable(t *testing.T) {
	bundle, rootfs := newBundleNoStore(t, []string{"PATH=/usr/bin"})

	restore, err := inject(bundle, []byte("BUILDCAGE-CA"))
	if err != nil {
		t.Fatal(err)
	}

	env := loadEnv(t, bundle)

	for _, variable := range []string{
		"NODE_EXTRA_CA_CERTS", "DENO_CERT",
		"CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE", "PIP_CERT", "SSL_CERT_FILE",
	} {
		if env[variable] != ownCAPath {
			t.Errorf("%s = %q, want %q", variable, env[variable], ownCAPath)
		}
	}
	own, err := os.ReadFile(filepath.Join(rootfs, strings.TrimPrefix(ownCAPath, "/")))
	if err != nil {
		t.Fatal(err)
	}
	if string(own) != "BUILDCAGE-CA" {
		t.Fatalf("own CA file = %q", own)
	}

	restore()
	if _, err := os.Stat(filepath.Join(rootfs, strings.TrimPrefix(ownCAPath, "/"))); !os.IsNotExist(err) {
		t.Fatalf("own CA file still present after restore: %v", err)
	}
}
