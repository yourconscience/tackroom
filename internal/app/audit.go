package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Shared risky-pattern regexes, reused by the doctor external-skill audit and
// the standalone `tackroom audit` command.
var (
	rePipeToShell     = regexp.MustCompile(`(?i)\b(curl|wget)\b[^|\n]*\|\s*(sudo\s+)?(ba|z)?sh\b`)
	reBase64ToShell   = regexp.MustCompile(`(?i)\bbase64\b\s+(-d|-D|--decode)\b[^\n]*\|\s*(ba|z)?sh\b`)
	reCredentialPaths = regexp.MustCompile(`(\$HOME|~)/\.(ssh|aws|gnupg|netrc)\b`)
)

// skillAuditPatterns flags supply-chain and prompt-injection risks in skill
// content. Matches are warnings for human review, not verdicts.
var skillAuditPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"pipe-to-shell", rePipeToShell},
	{"base64-to-shell", reBase64ToShell},
	{"prompt-injection", regexp.MustCompile(`(?i)\b(ignore|disregard)\s+(all\s+)?(previous|prior|above)\s+(instructions|context|rules)\b`)},
	{"hidden-from-user", regexp.MustCompile(`(?i)\bdo not (tell|inform|mention|reveal)( this)?( to)? the user\b`)},
	{"credential-paths", reCredentialPaths},
}

var skillAuditExtensions = map[string]bool{
	".md": true, ".sh": true, ".bash": true, ".zsh": true,
	".py": true, ".js": true, ".mjs": true, ".ts": true,
}

const skillAuditMaxFileSize = 1 << 20 // 1 MiB

// auditSkillTree scans one skill directory and returns findings as
// "relpath: pattern" strings.
func auditSkillTree(root string) ([]string, error) {
	var findings []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !skillAuditExtensions[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > skillAuditMaxFileSize {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for _, pattern := range skillAuditPatterns {
			if pattern.re.Match(data) {
				findings = append(findings, fmt.Sprintf("%s: %s", rel, pattern.name))
			}
		}
		return nil
	})
	return findings, err
}

func checkExternalSkillAudit(cfg config, home string) checkResult {
	const checkName = "external skill audit"
	if len(cfg.ExternalSkills) == 0 {
		return checkResult{checkName, checkStatusPass, "none configured"}
	}
	skills, err := discoverExternalSkills(cfg.ExternalSkills, home)
	if err != nil {
		return checkResult{checkName, checkStatusWarn, fmt.Sprintf("cannot discover external skills: %v", err)}
	}
	var findings []string
	names := make([]string, 0, len(skills))
	for name := range skills {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		hits, err := auditSkillTree(skills[name])
		if err != nil {
			return checkResult{checkName, checkStatusWarn, fmt.Sprintf("scan %s: %v", name, err)}
		}
		for _, hit := range hits {
			findings = append(findings, fmt.Sprintf("%s/%s", name, hit))
		}
	}
	if len(findings) > 0 {
		return checkResult{checkName, checkStatusWarn, "review: " + strings.Join(findings, "; ")}
	}
	return checkResult{checkName, checkStatusPass, fmt.Sprintf("%d skills scanned, no risky patterns", len(skills))}
}

func checkExternalSkillLock(repoRoot string, cfg config, home string) checkResult {
	const checkName = "external skill lock"
	if len(cfg.ExternalSkills) == 0 {
		return checkResult{checkName, checkStatusPass, "none configured"}
	}
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return checkResult{checkName, checkStatusWarn, err.Error()}
	}
	cacheRoot := externalCacheDir(home)
	var issues []string
	pinned := 0
	for _, src := range cfg.ExternalSkills {
		name := repoName(src.URL)
		pin := lockEntryFor(lock, src)
		if pin == nil {
			issues = append(issues, fmt.Sprintf("%s unpinned (run tackroom sync)", name))
			continue
		}
		head := externalSkillCommitFull(filepath.Join(cacheRoot, name))
		if head == "" {
			issues = append(issues, fmt.Sprintf("%s not cloned", name))
			continue
		}
		if head != pin.Commit {
			issues = append(issues, fmt.Sprintf("%s cache at %s but lock pins %s", name, shortCommit(head), shortCommit(pin.Commit)))
			continue
		}
		pinned++
	}
	if len(issues) > 0 {
		return checkResult{checkName, checkStatusWarn, strings.Join(issues, "; ")}
	}
	return checkResult{checkName, checkStatusPass, fmt.Sprintf("%d sources pinned and in sync", pinned)}
}
