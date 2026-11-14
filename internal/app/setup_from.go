package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// setupFrom is what `setup --from URL` does to the config root before the rest
// of setup runs: clone into a missing or empty root, or pull a root that is
// already a clone of the same repository. Anything else is refused before a
// single file changes.
type setupFrom struct {
	url   string
	root  string
	shown string // root as ~/... for messages
	clone bool   // false: the root is already a clone, pull it
	// changed is set by apply: the clone was made or the pull moved HEAD.
	changed bool
}

func planSetupFrom(url string, root string, home string) (setupFrom, error) {
	url = strings.TrimSpace(url)
	shown := displayPath(root, home)
	plan := setupFrom{url: url, root: root, shown: shown}
	if _, err := exec.LookPath("git"); err != nil {
		return plan, errors.New("setup --from needs git; install git and run it again")
	}
	info, err := os.Stat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		plan.clone = true
		return plan, nil
	case err != nil:
		return plan, fmt.Errorf("check %s: %w", root, err)
	case !info.IsDir():
		return plan, fmt.Errorf("%s exists and is not a directory; nothing was changed. Move it aside, then run tackroom setup --from %s again", shown, redactURL(url))
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return plan, fmt.Errorf("read %s: %w", root, err)
	}
	if len(entries) == 0 {
		plan.clone = true
		return plan, nil
	}
	shownURL := redactURL(url)
	top, err := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").Output()
	if err != nil || !sameResolvedPath(strings.TrimSpace(string(top)), root) {
		return plan, fmt.Errorf("%s already has files and is not a clone of %s; nothing was changed. Move it aside (or set TACKROOM_HOME to an empty directory), then run tackroom setup --from %s again", shown, shownURL, shownURL)
	}
	// The configured URL, before any url.<base>.insteadOf rewriting.
	out, _ := exec.Command("git", "-C", root, "config", "--get", "remote.origin.url").Output()
	origin := strings.TrimSpace(string(out))
	if origin == "" {
		return plan, fmt.Errorf("%s is a git repository with no origin; nothing was changed. If it is a copy of %s, connect it and run tackroom setup --from %s again:\n  git -C %s remote add origin %s\n  git -C %s pull --ff-only --set-upstream origin main\nIf it is not, move it aside first", shown, shownURL, shownURL, shown, shownURL, shown)
	}
	if !sameGitURL(origin, url) && !sameGitURL(gitExpandedURL(root, "origin"), gitExpandedURL(root, url)) {
		return plan, fmt.Errorf("%s is a git repository whose origin is %s, not %s; nothing was changed. Use that URL, or move the directory aside and run tackroom setup --from %s again", shown, redactURL(origin), shownURL, shownURL)
	}
	return plan, nil
}

// gitExpandedURL is a remote name or URL after the url.<base>.insteadOf
// rules, as git would use it; "" when git cannot say.
func gitExpandedURL(root string, remote string) string {
	out, err := exec.Command("git", "-C", root, "ls-remote", "--get-url", "--", remote).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// action is the step as --dry-run and --json list it.
func (p setupFrom) action() string {
	if p.clone {
		return fmt.Sprintf("clone %s into %s", redactURL(p.url), p.root)
	}
	return fmt.Sprintf("git pull --ff-only in %s (a clone of %s)", p.root, redactURL(p.url))
}

func (p *setupFrom) apply() error {
	if p.clone {
		if _, err := gitRemote("clone", "--quiet", "--", p.url, p.root); err != nil {
			return fmt.Errorf("git clone %s failed: %v", redactURL(p.url), err)
		}
		p.changed = true
		return nil
	}
	before := gitHead(p.root)
	if _, err := gitRemote("-C", p.root, "pull", "--ff-only", "--quiet"); err != nil {
		// One line from git and one fix, not git's whole hint block.
		return fmt.Errorf("git pull --ff-only in %s failed, so setup changed nothing: %s\nFix: git -C %s pull --rebase, then run tackroom setup --from %s again", p.shown, gitErrorLine(err.Error()), p.shown, redactURL(p.url))
	}
	p.changed = gitHead(p.root) != before
	return nil
}

// sameGitURL compares two remote URLs as the same repository: ssh, scp-style
// and https forms of one host and path match, and so do a trailing slash or
// ".git", credentials, a port, the host's case, and a local path's absolute
// form.
func sameGitURL(a string, b string) bool {
	return a != "" && b != "" && normalizeGitURL(a) == normalizeGitURL(b)
}

func normalizeGitURL(u string) string {
	u = strings.TrimSpace(u)
	if info, err := os.Stat(u); err == nil && info.IsDir() {
		if abs, err := filepath.Abs(u); err == nil {
			u = abs
		}
	}
	host, path := "", u
	switch scheme, rest, ok := strings.Cut(u, "://"); {
	case ok && scheme == "file":
		path = rest
	case ok:
		host, path, _ = strings.Cut(rest, "/")
	case !strings.HasPrefix(u, "/") && strings.Contains(u, ":") && !strings.Contains(strings.SplitN(u, ":", 2)[0], "/"):
		// scp-style user@host:path
		host, path, _ = strings.Cut(u, ":")
	}
	if host != "" {
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		host, _, _ = strings.Cut(host, ":")
		host = strings.ToLower(host) + "/"
		path = strings.TrimPrefix(path, "/")
	}
	return host + strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
}
