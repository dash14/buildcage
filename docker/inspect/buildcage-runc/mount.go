package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// scratchRoot is a var, not a const, so tests can point it at a temp
// directory instead of the real host path.
var scratchRoot = "/run/buildcage/ca"

const (
	// A real CA bundle directory is a few hundred KB across a handful of
	// files. These bound how far a Dockerfile-chosen custom path (an
	// already-set env var pointing somewhere of its own) can drag this
	// mechanism before injection is skipped for it instead of mirroring
	// something like an application directory wholesale.
	maxCustomDirBytes = 20 << 20
	maxCustomDirFiles = 512
)

// runRsync is the only place this file spawns a process, so tests can
// replace it to exercise the decision logic without rsync installed.
var runRsync = func(args []string) ([]byte, error) {
	return exec.Command("rsync", args...).CombinedOutput()
}

// dirBind mounts a scratch mirror of one directory over the step's view of
// it, so the CA can be added without ever opening the real rootfs file for
// writing. Grouped by directory rather than by file: two variables can
// resolve to different files in the same store directory, and mounting two
// separate sources over the same destination would let the second shadow
// the first.
type dirBind struct {
	hostDir      string
	containerDir string
	scratchDir   string
	bundleFiles  []string
	custom       bool

	original []fileEntry // hostDir's state before mirroring
	baseline []fileEntry // scratchDir's state right after the CA was appended
}

func groupTargetsByDir(targets map[string]bool) map[string][]string {
	groups := make(map[string][]string)
	for target := range targets {
		dir := filepath.Dir(target)
		groups[dir] = append(groups[dir], target)
	}
	return groups
}

func newScratchDir(bundle string) (string, error) {
	if err := os.MkdirAll(scratchRoot, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(scratchRoot, filepath.Base(bundle)+"-")
}

func mountConflicts(raw map[string]any, dest string) bool {
	mounts, _ := raw["mounts"].([]any)
	for _, m := range mounts {
		entry, ok := m.(map[string]any)
		if !ok {
			continue
		}
		existing, _ := entry["destination"].(string)
		if existing == "" {
			continue
		}
		if existing == dest || strings.HasPrefix(dest, existing+"/") || strings.HasPrefix(existing, dest+"/") {
			return true
		}
	}
	return false
}

func addBindMount(raw map[string]any, dest, src string) {
	mounts, _ := raw["mounts"].([]any)
	raw["mounts"] = append(mounts, map[string]any{
		"destination": dest,
		"type":        "bind",
		"source":      src,
		"options":     []any{"rbind", "rw"},
	})
}

func mirrorDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	_, err := runRsync([]string{"-aHAX", "--numeric-ids", "--", src + "/", dst + "/"})
	return err
}

// writeBack mirrors scratchDir onto hostDir. The dry run is logged so a
// write-back is auditable after the fact; the real failure signal is the
// second rsync's own exit code; a partial transfer already makes rsync exit
// non-zero, so this doesn't also diff the two runs' output against each other.
func writeBack(scratchDir, hostDir string) error {
	args := []string{"-aHAX", "--checksum", "--delete", "--numeric-ids", "--no-specials", "--no-devices", "--itemize-changes"}
	src, dst := scratchDir+"/", hostDir+"/"

	planned, err := runRsync(append(append([]string{}, args...), "-n", "--", src, dst))
	if err != nil {
		return fmt.Errorf("rsync dry run for %s: %w: %s", hostDir, err, planned)
	}
	logf("CA store write-back for %s:\n%s", hostDir, planned)

	if out, err := runRsync(append(append([]string{}, args...), "--", src, dst)); err != nil {
		return fmt.Errorf("rsync write-back to %s: %w: %s", hostDir, err, out)
	}
	return nil
}

type fileEntry struct {
	path      string
	mode      fs.FileMode
	uid, gid  uint32
	mtime     time.Time
	symlinkTo string
	sha256    [32]byte
}

func captureManifest(root string) ([]fileEntry, error) {
	var entries []fileEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == root {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fe := fileEntry{path: rel, mode: info.Mode(), mtime: info.ModTime()}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			fe.uid, fe.gid = st.Uid, st.Gid
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fe.symlinkTo = target
		case info.Mode().IsRegular():
			sum, err := hashFile(path)
			if err != nil {
				return err
			}
			fe.sha256 = sum
		}
		entries = append(entries, fe)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries, nil
}

func hashFile(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// manifestsEqual ignores mtime: the marker append/strip round trip can leave
// a file's mtime different even when every byte is back where it started.
func manifestsEqual(a, b []fileEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.path != y.path || x.mode != y.mode || x.uid != y.uid || x.gid != y.gid ||
			x.symlinkTo != y.symlinkTo || x.sha256 != y.sha256 {
			return false
		}
	}
	return true
}

func sizeAndCount(root string) (bytes int64, files int, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	})
	return bytes, files, err
}

// restoreUnchangedMtimes resets an untouched entry's mtime to what it was
// before mirroring. Without this, rsync's own mtime-preserving write-back
// would see a real difference (same content, different mtime) and copy the
// file up even though the step never touched it.
func restoreUnchangedMtimes(original, current []fileEntry, scratchDir string) error {
	byPath := make(map[string]fileEntry, len(original))
	for _, e := range original {
		byPath[e.path] = e
	}
	for _, c := range current {
		if c.mode&os.ModeSymlink != 0 {
			continue // no portable Lutimes in the standard library; harmless to skip
		}
		o, ok := byPath[c.path]
		if !ok || o.mode != c.mode || o.uid != c.uid || o.gid != c.gid || o.sha256 != c.sha256 {
			continue
		}
		if err := os.Chtimes(filepath.Join(scratchDir, c.path), o.mtime, o.mtime); err != nil {
			return err
		}
	}
	return nil
}

func (b *dirBind) prepare(ca []byte) error {
	if b.custom {
		size, count, err := sizeAndCount(b.hostDir)
		if err != nil {
			return err
		}
		if size > maxCustomDirBytes || count > maxCustomDirFiles {
			return fmt.Errorf("%s is too large to mirror (%d bytes, %d files)", b.hostDir, size, count)
		}
	}

	if err := mirrorDir(b.hostDir, b.scratchDir); err != nil {
		return fmt.Errorf("mirroring %s: %w", b.hostDir, err)
	}

	original, err := captureManifest(b.scratchDir)
	if err != nil {
		return err
	}
	b.original = original

	for _, name := range b.bundleFiles {
		if err := appendCA(filepath.Join(b.scratchDir, name), ca); err != nil {
			return err
		}
	}

	baseline, err := captureManifest(b.scratchDir)
	if err != nil {
		return err
	}
	b.baseline = baseline
	return nil
}

// finish diffs the scratch mirror against its post-injection baseline and
// only touches the real rootfs directory when the step actually changed it.
func (b *dirBind) finish() error {
	current, err := captureManifest(b.scratchDir)
	if err != nil {
		return err
	}
	if manifestsEqual(current, b.baseline) {
		return nil
	}

	for _, name := range b.bundleFiles {
		if err := removeCA(filepath.Join(b.scratchDir, name)); err != nil {
			return err
		}
	}

	stripped, err := captureManifest(b.scratchDir)
	if err != nil {
		return err
	}
	if err := restoreUnchangedMtimes(b.original, stripped, b.scratchDir); err != nil {
		return err
	}

	return writeBack(b.scratchDir, b.hostDir)
}

func (b *dirBind) cleanup() {
	if err := os.RemoveAll(b.scratchDir); err != nil {
		logf("cannot remove %s: %v", b.scratchDir, err)
	}
}
