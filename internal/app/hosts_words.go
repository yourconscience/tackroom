package app

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The words both views draw for a machine: its health, a one-line summary,
// the relation of its config commit and a command that fixes the first
// problem, worked out once here as crons_words.go does for jobs.

var hostVersionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// annotateHost fills RelationText, Health, Summary and Fix. version is this
// machine's tackroom; config is a --config path a local fix must carry;
// localUncommitted says this machine has not committed its tackroom.yaml.
//
// fail: unreachable, no tackroom, status errored (not set up, unreadable
// config), or a tackroom a major version behind this machine's. warn: config
// not committed, out of sync, behind or diverged, uncommitted changes, or an
// older tackroom than this machine's. ok otherwise.
func annotateHost(h *hostJSON, version, config string, localUncommitted bool) {
	h.RelationText = hostRelationText(*h)
	older, majorOlder := "", false
	if versionOlder(h.Version, version) {
		older = fmt.Sprintf("Runs tackroom %s, this machine %s", h.Version, version)
		if majorOlder = versionMajor(h.Version) < versionMajor(version); majorOlder {
			older += "; upgrade it before it syncs"
		}
	}
	// The probe found tackroom off the non-login PATH, so name it in full.
	bin := "tackroom"
	if h.Host != "local" && !h.onPath {
		bin = shellJoin([]string{h.bin})
	}
	fix := func(verb string) string {
		if h.Host == "local" {
			if config != "" {
				return "tackroom " + verb + " --config " + shellJoin([]string{config})
			}
			return "tackroom " + verb
		}
		return "ssh " + h.Host + " " + bin + " " + verb
	}
	// The host's config root shares history with this machine's: it is a
	// clone of the same repo, so it sets up from what this machine pushed.
	sameRepo := h.Host != "local" && h.Relation != "" && h.Relation != "unknown"

	var parts []string
	switch {
	case !h.Reachable:
		h.Health, h.Summary = "fail", "Unreachable: "+h.Error
		return
	case h.bin == "":
		h.Health, h.Summary = "fail", h.Error
		return
	case h.Error != "":
		h.Health = "fail"
		if strings.Contains(h.Error, "run tackroom setup") {
			missing := strings.TrimPrefix(strings.TrimSuffix(h.Error, "; run tackroom setup"), "canonical config ")
			if dir, ok := strings.CutSuffix(missing, "/tackroom.yaml not found"); ok {
				missing = "no tackroom.yaml in " + dir
			}
			parts = append(parts, "Not set up: "+missing)
			switch {
			case sameRepo && localUncommitted:
				// The fix is on this machine's row: commit tackroom.yaml first.
				parts = append(parts, "This machine has not committed its tackroom.yaml for it to pull")
			case sameRepo:
				h.Fix = "ssh " + h.Host + " " + shellJoin([]string{"git -C " + shellJoin([]string{h.ConfigRoot}) + " pull --ff-only && " + bin + " setup"})
			default:
				h.Fix = fix("setup")
			}
		} else {
			parts = append(parts, "Status failed: "+h.Error)
		}
		if older != "" {
			parts = append(parts, older)
		}
		if majorOlder {
			// Setting up or syncing with that tackroom is the harm; no command here.
			h.Fix = ""
		}
		h.Summary = strings.Join(parts, ". ")
		return
	}

	if h.ConfigUncommitted {
		parts = append(parts, "tackroom.yaml is not committed, so other machines cannot get it")
		if h.Host == "local" {
			files := "tackroom.yaml"
			if h.hasLock {
				files += " tackroom.lock"
			}
			h.Fix = "cd " + commandPath(h.ConfigRoot) + " && git add " + files + ` && git commit -m "Add tackroom config" && git push`
		}
	}
	switch h.Relation {
	case "behind":
		parts = append(parts, plural(h.Behind, "commit")+" behind this machine")
		h.Fix = fix("pull")
	case "diverged":
		parts = append(parts, "Config diverged from this machine")
	}
	if !h.Synced {
		names := make([]string, len(h.OutOfSync))
		for i, a := range h.OutOfSync {
			names[i] = a.Name
		}
		switch {
		case len(names) > 0:
			parts = append(parts, "Out of sync: "+strings.Join(names, ", "))
		case h.repoLink != "" && h.repoLink != stateSynced:
			parts = append(parts, "Config root link is "+h.repoLink)
		default:
			parts = append(parts, "Out of sync")
		}
		if h.Fix == "" {
			h.Fix = fix("sync")
		}
	}
	if h.Dirty > 0 {
		parts = append(parts, plural(h.Dirty, "uncommitted change")+" in the config root")
	}
	if older != "" {
		parts = append(parts, older)
	}
	if majorOlder {
		h.Health, h.Fix = "fail", ""
		h.Summary = strings.Join(parts, ". ")
		return
	}
	if len(parts) > 0 {
		h.Health, h.Summary = "warn", strings.Join(parts, ". ")
		return
	}
	h.Health, h.Summary = "ok", "In sync"
	if h.Host != "local" && h.RelationText != "" {
		h.Summary += ", " + h.RelationText
	}
}

// hostRelationText puts Relation in words; the local host is this machine.
func hostRelationText(h hostJSON) string {
	if h.Host == "local" {
		return ""
	}
	switch h.Relation {
	case "same":
		return "same config commit as this machine"
	case "behind":
		return plural(h.Behind, "commit") + " behind this machine"
	case "ahead":
		return plural(h.Ahead, "commit") + " ahead of this machine"
	case "diverged":
		return fmt.Sprintf("diverged from this machine: %d behind, %d ahead", h.Behind, h.Ahead)
	case "unknown":
		return "config commit unknown to this machine"
	}
	return ""
}

// hostPseudoVersion matches the version `go install` stamps on a build of an
// untagged commit (1.8.2-0.20261114055220-550cae22e7eb): a dev build, whose
// numbers come from the last v1 tag, not from the code it runs.
var hostPseudoVersion = regexp.MustCompile(`-0\.\d{14}-[0-9a-f]{12}`)

// versionOlder reports whether release version v is older than than. A dev
// build on either side is not compared.
func versionOlder(v, than string) bool {
	if hostPseudoVersion.MatchString(v) || hostPseudoVersion.MatchString(than) {
		return false
	}
	a, b := hostVersionPattern.FindStringSubmatch(v), hostVersionPattern.FindStringSubmatch(than)
	if a == nil || b == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if x != y {
			return x < y
		}
	}
	return false
}

// versionMajor is a release version's major number, or -1.
func versionMajor(v string) int {
	m := hostVersionPattern.FindStringSubmatch(v)
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// commandPath writes a path under the home directory as ~/..., for a command
// the user copies; anything a shell would need quoted stays quoted in full.
func commandPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		if rest := strings.TrimPrefix(p, home+"/"); shellJoin([]string{rest}) == rest {
			return "~/" + rest
		}
	}
	return shellJoin([]string{p})
}
