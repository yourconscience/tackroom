package app

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// setupOverlay is `setup --overlay NAME`: overlays/NAME.yaml, committed in the
// config repository for one kind of machine, copied to tackroom.local.yaml
// before the first sync. Only setup reads overlays/.
type setupOverlay struct {
	src  string // overlays/NAME.yaml in the config root
	dst  string // tackroom.local.yaml in the config root
	data []byte // the overlay as found by planSetupOverlay, for --dry-run
}

// planSetupOverlay finds the overlay in lookRoot (the config root, or the
// throwaway clone a dry run plans against), and otherwise fails naming the
// overlays there.
func planSetupOverlay(name, lookRoot, repoRoot, repo string) (setupOverlay, error) {
	file := name + ".yaml"
	data, err := os.ReadFile(filepath.Join(lookRoot, "overlays", file))
	if err == nil {
		return setupOverlay{src: filepath.Join(repoRoot, "overlays", file), dst: filepath.Join(repoRoot, "tackroom.local.yaml"), data: data}, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return setupOverlay{}, err
	}
	entries, err := os.ReadDir(filepath.Join(lookRoot, "overlays"))
	if errors.Is(err, fs.ErrNotExist) {
		return setupOverlay{}, fmt.Errorf("no overlay %q: %s has no overlays/ folder", name, repo)
	}
	if err != nil {
		return setupOverlay{}, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
			names = append(names, strings.TrimSuffix(entry.Name(), ".yaml"))
		}
	}
	if len(names) == 0 {
		return setupOverlay{}, fmt.Errorf("no overlay %q: overlays/ in %s has no .yaml files", name, repo)
	}
	return setupOverlay{}, fmt.Errorf("no overlay %q in %s (overlays there: %s)", name, repo, strings.Join(names, ", "))
}

// action is the step as --dry-run and --json list it.
func (o setupOverlay) action() string {
	existing, err := os.ReadFile(o.dst)
	switch {
	case err != nil:
		return fmt.Sprintf("copy %s to %s", o.src, o.dst)
	case sameLocalOverlay(existing, o.data):
		return fmt.Sprintf("keep %s, which already matches %s", o.dst, o.src)
	}
	return fmt.Sprintf("leave %s as it is: it differs from %s, which setup lists as needing your attention", o.dst, o.src)
}

// apply copies the overlay into place, after any clone or pull. It returns the
// summary line for a copy made now, or a problem line when a different
// tackroom.local.yaml is already there: that file is the user's and is kept.
func (o setupOverlay) apply() (line string, problem string, err error) {
	data, err := os.ReadFile(o.src)
	if err != nil {
		return "", "", err
	}
	existing, err := os.ReadFile(o.dst)
	switch {
	case err == nil && sameLocalOverlay(existing, data):
		return "", "", nil
	case err == nil:
		return "", fmt.Sprintf("%s differs from %s, so setup left it as it is and synced with it. To use the overlay, replace it (cp %s %s) and run setup again", o.dst, o.src, o.src, o.dst), nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", "", err
	}
	if err := os.WriteFile(o.dst, data, 0o644); err != nil {
		return "", "", err
	}
	return fmt.Sprintf("Overlay: copied overlays/%s to tackroom.local.yaml", filepath.Base(o.src)), "", nil
}

// sameLocalOverlay reports whether tackroom.local.yaml already holds the
// overlay. periodic_sync is left out of the comparison: setup --no-cron and
// tackroom cron record it in that file, so a rerun must not call it changed.
func sameLocalOverlay(local, overlay []byte) bool {
	if bytes.Equal(local, overlay) {
		return true
	}
	a, b := map[string]any{}, map[string]any{}
	if yaml.Unmarshal(local, &a) != nil || yaml.Unmarshal(overlay, &b) != nil {
		return false
	}
	delete(a, "periodic_sync")
	delete(b, "periodic_sync")
	return reflect.DeepEqual(a, b)
}
