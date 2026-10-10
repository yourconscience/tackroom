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

// conflictBackupRoot is where replaced native files are moved:
// $XDG_STATE_HOME/tackroom/backups, else ~/.local/state/tackroom/backups.
func conflictBackupRoot(home string) string {
	if state := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); state != "" {
		return filepath.Join(state, "tackroom", "backups")
	}
	return filepath.Join(home, ".local", "state", "tackroom", "backups")
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
	resolved := map[string]bool{}
	for _, item := range report.Replaceable {
		dest := backupPathFor(item.Path, backupDir, home)
		if err := moveToBackup(item.Path, dest); err != nil {
			return moved, fmt.Errorf("%s: back up %s: %w", report.Name, item.Path, err)
		}
		moved = append(moved, item.Path)
		resolved[item.Message] = true
		if item.Skill == "" {
			report.RootState = stateMissing
			continue
		}
		report.Adds = append(report.Adds, item.Skill)
	}
	var remaining []string
	for _, conflict := range report.Conflicts {
		if !resolved[conflict] {
			remaining = append(remaining, conflict)
		}
	}
	report.Conflicts = remaining
	report.Replaceable = nil
	return moved, nil
}

func backupPathFor(path string, backupDir string, home string) string {
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join(backupDir, rel)
	}
	return filepath.Join(backupDir, "abs", strings.TrimPrefix(filepath.Clean(path), string(filepath.Separator)))
}

// moveToBackup renames src to dest, copying across filesystems when needed.
func moveToBackup(src string, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
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
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, info.Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
	})
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
	return fmt.Sprintf("run `tackroom sync --replace-conflicts` to move these into %s and link the shared copies, or delete them yourself", conflictBackupRoot(home))
}
