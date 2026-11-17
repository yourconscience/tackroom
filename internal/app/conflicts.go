package app

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tackroomStateDir holds machine-local state: $XDG_STATE_HOME/tackroom, else
// ~/.local/state/tackroom.
func tackroomStateDir(home string) string {
	if state := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); state != "" {
		return filepath.Join(state, "tackroom")
	}
	return filepath.Join(home, ".local", "state", "tackroom")
}

// conflictBackupRoot is where replaced native files are moved.
func conflictBackupRoot(home string) string {
	return filepath.Join(tackroomStateDir(home), "backups")
}

// newConflictBackupDir returns a fresh, timestamped backup directory path.
// The directory is created on first use.
func newConflictBackupDir(home string) string {
	return filepath.Join(conflictBackupRoot(home), time.Now().UTC().Format("20060102T150405Z"))
}

// replaceConflicts moves every replaceable native path into backupDir (keeping
// its path relative to home) and turns the conflict into a pending link, so
// the normal apply step links it. Other conflicts stay on the report.
func replaceConflicts(report *agentReport, backupDir string, home string) ([]string, error) {
	if len(report.Replaceable) == 0 {
		return nil, nil
	}
	var moved []string
	var dests []string
	resolved := map[string]bool{}
	for _, item := range report.Replaceable {
		dest := backupPathFor(item.Path, backupDir, home)
		if err := moveToBackup(item.Path, dest); err != nil {
			// Put back what this agent already moved, so a failed backup
			// never leaves a native path empty.
			for i := len(moved) - 1; i >= 0; i-- {
				_ = os.Rename(dests[i], moved[i])
			}
			return nil, fmt.Errorf("%s: back up %s: %w", report.Name, item.Path, err)
		}
		moved = append(moved, item.Path)
		dests = append(dests, dest)
		resolved[item.Message] = true
		switch {
		case item.Skill != "":
			report.Adds = append(report.Adds, item.Skill)
		case item.Role != "":
			report.AddsAgent = append(report.AddsAgent, item.Role)
		default:
			report.RootState = stateMissing
		}
	}
	var remaining, roleRemaining []string
	for _, conflict := range report.Conflicts {
		if !resolved[conflict] {
			remaining = append(remaining, conflict)
		}
	}
	for _, conflict := range report.RoleConflicts {
		if !resolved[conflict] {
			roleRemaining = append(roleRemaining, conflict)
		}
	}
	report.Conflicts = remaining
	report.RoleConflicts = roleRemaining
	report.Replaceable = nil
	return moved, nil
}

// backupIdenticalCopies moves every native copy that matches the shared one
// into backupDir before sync links it, so `tackroom restore` can put the
// machine back exactly. A path two agents share (Gemini CLI and Antigravity
// CLI's GEMINI.md) is moved once.
func backupIdenticalCopies(reports []agentReport, backupDir string, home string) ([]string, error) {
	var moved []string
	done := map[string]bool{}
	for i := range reports {
		r := &reports[i]
		for _, path := range r.IdenticalCopies {
			if !done[path] {
				if err := moveToBackup(path, backupPathFor(path, backupDir, home)); err != nil {
					return moved, fmt.Errorf("%s: back up %s: %w", r.Name, path, err)
				}
				done[path] = true
				moved = append(moved, path)
			}
			if path == r.RootPath {
				r.RootState = stateMissing
			}
		}
		r.IdenticalCopies = nil
	}
	return moved, nil
}

// blockingConflicts are the conflicts that keep sync away from the whole
// agent. A role conflict is not one of them: sync leaves that role file in
// place and syncs the rest.
func blockingConflicts(report agentReport) []string {
	var out []string
	for _, conflict := range report.Conflicts {
		if !stringInSlice(conflict, report.RoleConflicts) {
			out = append(out, conflict)
		}
	}
	return out
}

func backupPathFor(path string, backupDir string, home string) string {
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join(backupDir, rel)
	}
	return filepath.Join(backupDir, "abs", strings.TrimPrefix(filepath.Clean(path), string(filepath.Separator)))
}

// moveToBackup renames src to dest, copying across filesystems when needed.
// Backup directories are private (0700): they may hold instructions or skills
// that sat under a private parent directory before.
func moveToBackup(src string, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("backup path %s already exists", dest)
	}
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	if err := copyTree(src, dest); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func copyTree(src string, dest string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyBackupFile(path, target, info.Mode().Perm())
		}
	})
}

// copyBackupFile closes both files before returning, so a large tree never holds
// more than two descriptors open.
func copyBackupFile(src string, dest string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// filesEqual reports whether two regular files have identical bytes.
func filesEqual(left string, right string) (bool, error) {
	a, err := os.ReadFile(left)
	if err != nil {
		return false, err
	}
	b, err := os.ReadFile(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(a, b), nil
}

// conflictHint is the next step printed when sync leaves conflicts in place.
func conflictHint(home string) string {
	return fmt.Sprintf("run `tackroom sync --replace-conflicts` to move these into %s and put the shared copies in their place, or delete them yourself", conflictBackupRoot(home))
}
