package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	starter "github.com/yourconscience/tackroom"
)

// starterManifestName records which starter files this config root received
// from tackroom and what they looked like when it wrote them. It is committed
// with the config root so every machine tracks the same baseline.
const starterManifestName = ".tackroom-starter.json"

const starterManifestVersion = 1

// starterManagedPrefixes are the starter paths tackroom owns as code. It
// refreshes them while they are unmodified and removes them once a release
// stops shipping them. Everything else in the starter set (AGENTS.md,
// tackroom.yaml, agents/*.md, skills/) is user content: tackroom only ever
// creates those when missing.
var starterManagedPrefixes = []string{"memory/hooks/", "memory/lib/"}

// legacyStarterHashes lists hashes of managed starter files that earlier
// tackroom releases wrote. It bridges config roots created before the manifest
// existed: a managed file is refreshed or removed only while its content
// matches one of these hashes or the manifest baseline, so tackroom never
// overwrites or deletes content it did not write itself.
//
// The manifest takes over for every release after v0.9.0, so this table is a
// one-time bridge and does not need to grow with each release.
var legacyStarterHashes = map[string][]string{
	// Refreshed when they still match the v0.8.0 content.
	"memory/hooks/common.sh":               {"f13b590485d585d2d3d73a87feffc885737bf33202daf558c37d22c4c3127ca3"},
	"memory/hooks/session-end.sh":          {"6d1cb6e961d3b2a4179d7efbae41de0882a9eec6a8f322acb9f76f39917fafc8"},
	"memory/hooks/stop.sh":                 {"6d98219c8a2e6d019e2001917a30664f04cdedb0c3eda91e7e0e2db696814f3c"},
	"memory/hooks/sync-memory-to-vault.sh": {"7d6689ad7a1cf6188d19e8cd71879f0e2a752cbe033827d6daf6928327b99d26"},
	"memory/hooks/sync-vault-to-memory.sh": {"3b9df7dd08e9a9d45977a7d0b2e44b01e7a680162127e23ad5eea0f1399e9c81"},
	"memory/lib/basic_memory.py":           {"effd29bac6e8d75bc70b150e84833e3157eda6928a6f974a62ca54e3142a0900"},
	"memory/lib/sync.py":                   {"5bb300805c1cfc7f673150e4c8bfbcdaf65db882720033f46c34df2ec78523ea"},
	// Removed when they still match the content a past release shipped.
	"memory/hooks/README-codex-omp.md": {"3cdc9cf3d1403650bb44cb117b79d2ed2bbe656f8914206860cdbdf60ccc5ead"},
	"memory/hooks/omp-memory.ts":       {"3da7a801ef62593ba53f3e8a6095a1cce5cc125e1d971d8995a6b04d936eda08"},
	"memory/lib/amp_digest.py":         {"a2fca5d854bc89dbde8f8aa93c6ede1f85cbc5f8b751cc36d5f745ec99d04840"},
	"memory/lib/factory_digest.py":     {"deae3192f371d0f863f1d84b9aa46ce71011dd548b29c83c5d60eb6b7a015e4e"},
	"memory/lib/hermes_digest.py":      {"fa6c061d85fc829689142a55e08abcc12aadaffae529907b33bb94bcd326b20b"},
}

type starterManifest struct {
	Version int               `json:"version"`
	Files   map[string]string `json:"files"`
}

// starterChanges reports what a reconcile pass did, so sync can show it.
type starterChanges struct {
	Scaffolded   []string
	Updated      []string
	Removed      []string
	KeptModified []string
}

func (c starterChanges) empty() bool {
	return len(c.Scaffolded)+len(c.Updated)+len(c.Removed)+len(c.KeptModified) == 0
}

func (c starterChanges) report(out io.Writer) {
	if c.empty() {
		return
	}
	fmt.Fprintf(out, "starter files: updated %d, removed %d, scaffolded %d, kept modified %d\n",
		len(c.Updated), len(c.Removed), len(c.Scaffolded), len(c.KeptModified))
	printStarterList(out, "updated", c.Updated)
	printStarterList(out, "removed", c.Removed)
	printStarterList(out, "scaffolded", c.Scaffolded)
	printStarterList(out, "kept (modified by you)", c.KeptModified)
}

func printStarterList(out io.Writer, label string, paths []string) {
	if len(paths) == 0 {
		return
	}
	fmt.Fprintf(out, "  %s: %s\n", label, strings.Join(paths, ", "))
}

func fileHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isManagedStarterPath(path string) bool {
	for _, prefix := range starterManagedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// isSafeStarterManifestPath reports whether a manifest entry is a plain relative
// path inside the managed layer. Entries that are absolute, contain a `.` or
// `..` segment, or name user content are rejected, so a hand-edited manifest
// cannot make tackroom read or remove a file outside the config root.
func isSafeStarterManifestPath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, '\\') {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return isManagedStarterPath(path)
}

func hashIn(hashes []string, want string) bool {
	for _, hash := range hashes {
		if hash == want {
			return true
		}
	}
	return false
}

func starterManifestPath(root string) string {
	return filepath.Join(root, starterManifestName)
}

func loadStarterManifest(root string) (starterManifest, bool, error) {
	data, err := os.ReadFile(starterManifestPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return starterManifest{Version: starterManifestVersion, Files: map[string]string{}}, false, nil
	}
	if err != nil {
		return starterManifest{}, false, fmt.Errorf("read %s: %w", starterManifestPath(root), err)
	}
	var manifest starterManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return starterManifest{}, false, fmt.Errorf("parse %s: %w", starterManifestPath(root), err)
	}
	// The manifest is a committed file, so it can be edited or corrupted by
	// hand: drop anything that is not a plain managed path before it is used
	// for reads, writes, or removals.
	for path := range manifest.Files {
		if !isSafeStarterManifestPath(path) {
			delete(manifest.Files, path)
		}
	}
	if manifest.Files == nil {
		manifest.Files = map[string]string{}
	}
	manifest.Version = starterManifestVersion
	return manifest, true, nil
}

func saveStarterManifest(root string, manifest starterManifest) error {
	manifest.Version = starterManifestVersion
	if manifest.Files == nil {
		manifest.Files = map[string]string{}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", starterManifestName, err)
	}
	data = append(data, '\n')
	path := starterManifestPath(root)
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// shippedStarterFiles returns the managed starter files this release ships.
func shippedStarterFiles() (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(starter.StarterAssets, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isManagedStarterPath(path) {
			return nil
		}
		data, err := starter.StarterAssets.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read starter %s: %w", path, err)
		}
		files[path] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func writeStarterFile(root, path string, data []byte) error {
	target := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(target), err)
	}
	mode := fs.FileMode(0o644)
	if strings.HasPrefix(path, "memory/hooks/") {
		mode = 0o755
	}
	if err := os.WriteFile(target, data, mode); err != nil {
		return fmt.Errorf("write starter %s: %w", target, err)
	}
	return nil
}

// reconcileStarterFiles keeps the managed starter code layer in step with the
// running release: missing files are scaffolded, files tackroom wrote and the
// user did not touch are refreshed to the shipped version, files a release
// stopped shipping are removed, and anything the user modified is reported and
// left alone.
func reconcileStarterFiles(root string, streams setupIO, confirm bool) (starterChanges, error) {
	shipped, err := shippedStarterFiles()
	if err != nil {
		return starterChanges{}, err
	}
	return reconcileStarterSet(root, shipped, legacyStarterHashes, streams, confirm)
}

func reconcileStarterSet(root string, shipped map[string][]byte, legacy map[string][]string, streams setupIO, confirm bool) (starterChanges, error) {
	var changes starterChanges
	manifest, _, err := loadStarterManifest(root)
	if err != nil {
		return changes, err
	}

	next := make(map[string]string, len(shipped))
	for _, path := range sortedKeysNative(shipped) {
		data := shipped[path]
		shippedHash := fileHash(data)
		target := filepath.Join(root, filepath.FromSlash(path))
		disk, err := os.ReadFile(target)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := writeStarterFile(root, path, data); err != nil {
				return changes, err
			}
			changes.Scaffolded = append(changes.Scaffolded, path)
			next[path] = shippedHash
		case err != nil:
			return changes, fmt.Errorf("read %s: %w", target, err)
		default:
			diskHash := fileHash(disk)
			recorded, tracked := manifest.Files[path]
			switch {
			case diskHash == shippedHash:
				next[path] = shippedHash
			case (tracked && diskHash == recorded) || hashIn(legacy[path], diskHash):
				// Untouched content tackroom wrote in this or an earlier
				// release: safe to refresh.
				if err := writeStarterFile(root, path, data); err != nil {
					return changes, err
				}
				changes.Updated = append(changes.Updated, path)
				next[path] = shippedHash
			default:
				// Content tackroom did not write: keep the file and keep
				// reporting it, so a customized layer is never clobbered.
				changes.KeptModified = append(changes.KeptModified, path)
				if tracked {
					next[path] = recorded
				}
			}
		}
	}

	// Managed files this release no longer ships.
	for _, path := range sortedKeysNative(manifest.Files) {
		if _, stillShipped := shipped[path]; stillShipped {
			continue
		}
		removed, kept, err := retireStarterFile(root, path, manifest.Files[path], legacy[path], streams, confirm)
		if err != nil {
			return changes, err
		}
		switch {
		case removed:
			changes.Removed = append(changes.Removed, path)
		case kept:
			changes.KeptModified = append(changes.KeptModified, path)
			next[path] = manifest.Files[path]
		}
	}
	for _, path := range sortedKeysNative(legacy) {
		if _, stillShipped := shipped[path]; stillShipped {
			continue
		}
		if _, tracked := manifest.Files[path]; tracked {
			continue
		}
		removed, kept, err := retireStarterFile(root, path, "", legacy[path], streams, confirm)
		if err != nil {
			return changes, err
		}
		switch {
		case removed:
			changes.Removed = append(changes.Removed, path)
		case kept:
			changes.KeptModified = append(changes.KeptModified, path)
		}
	}

	manifest.Files = next
	if err := saveStarterManifest(root, manifest); err != nil {
		return changes, err
	}
	return changes, nil
}

// retireStarterFile removes a managed file that is no longer shipped while its
// content is still one tackroom wrote, and keeps it otherwise.
func retireStarterFile(root, path, recorded string, legacy []string, streams setupIO, confirm bool) (bool, bool, error) {
	target := filepath.Join(root, filepath.FromSlash(path))
	disk, err := os.ReadFile(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read %s: %w", target, err)
	}
	diskHash := fileHash(disk)
	if diskHash != recorded && !hashIn(legacy, diskHash) {
		return false, true, nil
	}
	if confirm && !promptYesNoDefaultNo(streams, fmt.Sprintf("remove retired starter file %s?", path)) {
		return false, true, nil
	}
	if err := os.Remove(target); err != nil {
		return false, false, fmt.Errorf("remove %s: %w", path, err)
	}
	return true, false, nil
}
