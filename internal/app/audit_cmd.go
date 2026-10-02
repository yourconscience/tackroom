package app

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Severity levels for `tackroom audit`. CRITICAL is the only level that fails
// the command (non-zero exit).
const (
	auditCritical = "CRITICAL"
	auditWarn     = "WARN"
	auditInfo     = "INFO"
)

// auditFinding is a single reported issue. path is relative to the skill
// directory (or the lock file name for external-skill findings).
type auditFinding struct {
	severity string
	skill    string
	path     string
	desc     string
}

// Heuristics for the standalone audit. These are intentionally string/regex
// based (PoC): precision over recall, and no CRITICAL fires without a strong
// combined signal.
var (
	// Scripts that read a credential path (broader than reCredentialPaths so it
	// also covers ~/.aws/credentials, id_rsa, and .env files).
	reScriptCredential = regexp.MustCompile(`(?i)(\$HOME|~)/\.(ssh|aws|gnupg)\b|/\.aws/credentials\b|\bid_rsa\b|\.netrc\b|(?:^|[\s'"=(])\.env(\.[a-z0-9]+)?\b`)
	// Any outbound network call.
	reNetworkCall = regexp.MustCompile(`(?i)\b(curl|wget|nc|ncat|telnet)\b|https?://|\brequests\.(get|post|put|patch|delete)\b|\burllib\b|\bhttp\.client\b|\bnet/http\b|\bfetch\(|\baxios\b|\bsocket\.(socket|create_connection)\b|\bXMLHttpRequest\b`)
	// SKILL.md instruction verbs that move data outward.
	reExfilVerb = regexp.MustCompile(`(?i)\b(exfiltrat\w*|upload|send|transmit|forward|leak|beacon|post(\s+the)?)\b`)
	// Sensitive-data nouns that make an exfil instruction critical.
	reSensitiveData = regexp.MustCompile(`(?i)\b(secret|secrets|credential|credentials|token|tokens|password|passwords|api[ _-]?keys?|private key|ssh key|\.env|env var|environment variable|\.aws|\.ssh|history file)\b`)
	// Any http(s) URL.
	reAuditURL = regexp.MustCompile(`https?://[^\s)"'<>\]]+`)
	// HTML comments (hidden-instruction vector).
	reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	// Imperative verbs that make a hidden HTML comment suspicious.
	reImperative = regexp.MustCompile(`(?i)\b(ignore|disregard|send|upload|post|exfiltrat\w*|run|execute|delete|fetch|download|curl|wget|export|read|cat|leak|transmit|do not|always|never)\b`)
)

// auditDocHosts are hosts treated as documentation/reference links, not
// findings. Entries ending in "." match as a prefix (e.g. "docs.").
var auditDocHosts = []string{
	"github.com", "raw.githubusercontent.com", "gist.github.com",
	"docs.", "developer.", "readthedocs.io", "readthedocs.org",
	"pkg.go.dev", "go.dev", "golang.org", "stackoverflow.com",
	"wikipedia.org", "npmjs.com", "pypi.org", "crates.io",
	"anthropic.com", "openai.com", "mozilla.org", "developer.mozilla.org",
	"w3.org", "apache.org", "gnu.org", "json.org", "rfc-editor.org",
	"ietf.org", "example.com", "example.org", "localhost", "127.0.0.1",
}

var auditScriptExtensions = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true,
	".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true,
	".rb": true, ".pl": true,
}

func runAudit(opts runOptions) error {
	repoRoot, home, cfg, _, err := loadContext(opts)
	if err != nil {
		return err
	}

	fmt.Println("tackroom audit")
	fmt.Printf("repo: %s\n\n", repoRoot)

	findings := auditRepo(repoRoot, home, cfg)
	crit, _, _ := printAuditFindings(os.Stdout, findings)
	if crit > 0 {
		return fmt.Errorf("%d critical finding(s)", crit)
	}
	return nil
}

// auditRepo scans local skills, external-skill pinning, and the cached content
// of configured external skills, returning sorted findings. It reads only the
// filesystem so it is directly testable.
func auditRepo(repoRoot, home string, cfg config) []auditFinding {
	var findings []auditFinding

	skillsDir := filepath.Join(repoRoot, "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dir := filepath.Join(skillsDir, e.Name())
			if !hasFile(filepath.Join(dir, "SKILL.md")) {
				continue
			}
			findings = append(findings, auditLocalSkill(e.Name(), dir)...)
		}
	}

	lock, _ := readLockFile(repoRoot)
	cacheRoot := externalCacheDir(home)
	for _, src := range cfg.ExternalSkills {
		name := repoName(src.URL)
		if lockEntryFor(lock, src) == nil {
			findings = append(findings, auditFinding{
				auditWarn, name, lockFileName,
				"external skill not pinned in " + lockFileName,
			})
		}
		findings = append(findings, auditExternalSkill(name, src, cacheRoot)...)
	}

	sortAuditFindings(findings)
	return findings
}

// auditExternalSkill content-scans a configured external skill's cached clone
// with the same file-walk (and severities) used for local skills. A missing
// cache is reported as INFO rather than silently skipped.
func auditExternalSkill(name string, src externalSkillSource, cacheRoot string) []auditFinding {
	cachePath := filepath.Join(cacheRoot, name)
	if !hasDir(cachePath) {
		return []auditFinding{{
			auditInfo, name, filepath.Join("external", name),
			"external skill not in cache; run tackroom sync to fetch and audit",
		}}
	}
	if len(src.SkillDirs) > 0 {
		var findings []auditFinding
		allowed := skillAllowlist(src)
		found := make(map[string]bool)
		for _, relDir := range src.SkillDirs {
			clean, err := cleanExternalSkillDir(relDir)
			if err != nil {
				findings = append(findings, auditFinding{auditWarn, name, relDir, err.Error()})
				continue
			}
			skillDir := filepath.Join(cachePath, filepath.FromSlash(clean))
			if !hasDir(skillDir) {
				findings = append(findings, auditFinding{auditInfo, name, filepath.Join("external", name, clean), "selected external skill directory not in cache; run tackroom sync"})
				continue
			}
			candidates, err := discoverSkillsAtExternalRoot(src, name, skillDir, "")
			if err != nil {
				findings = append(findings, auditFinding{auditWarn, name, filepath.Join("external", name, clean), err.Error()})
				continue
			}
			for _, skill := range candidates {
				findingPath := filepath.Join("external", name, clean)
				if skill.Path != skillDir {
					findingPath = filepath.Join(findingPath, skill.Name)
				}
				if allowed != nil && !allowed[skill.Name] {
					findings = append(findings, auditFinding{auditInfo, name, findingPath, "unselected external skill skipped"})
					continue
				}
				found[skill.Name] = true
				findings = append(findings, auditLocalSkill(name, skill.Path)...)
			}
		}
		for skill := range allowed {
			if !found[skill] {
				findings = append(findings, auditFinding{auditInfo, name, filepath.Join("external", name, skill), "allowlisted external skill not in configured skill directories; run tackroom sync"})
			}
		}
		return findings
	}
	scanDir := filepath.Join(cachePath, src.SkillDir)
	if !hasDir(scanDir) {
		scanDir = cachePath
	}
	// A source whose skill_dir itself contains SKILL.md is a single-skill
	// source. Its validated allowlist names the repository, not a child dir.
	if hasFile(filepath.Join(scanDir, "SKILL.md")) {
		return auditLocalSkill(name, scanDir)
	}
	// The skills: allowlist is an explicit user choice of what gets installed;
	// only allowlisted subdirectories are scanned, and unselected siblings are
	// reported as skipped instead of failing the audit for content never used.
	if len(src.Skills) == 0 {
		return auditLocalSkill(name, scanDir)
	}
	var findings []auditFinding
	for _, sub := range src.Skills {
		subDir := filepath.Join(scanDir, sub)
		if !hasDir(subDir) {
			findings = append(findings, auditFinding{
				auditInfo, name, filepath.Join("external", name, sub),
				"allowlisted external skill not in cache; run tackroom sync",
			})
			continue
		}
		findings = append(findings, auditLocalSkill(name, subDir)...)
	}
	entries, err := os.ReadDir(scanDir)
	if err == nil {
		allowed := make(map[string]bool, len(src.Skills))
		for _, s := range src.Skills {
			allowed[s] = true
		}
		for _, e := range entries {
			if e.IsDir() && !allowed[e.Name()] && !strings.HasPrefix(e.Name(), ".") {
				findings = append(findings, auditFinding{
					auditInfo, name, e.Name(),
					"skipped: not in skills allowlist",
				})
			}
		}
	}
	return findings
}

// auditLocalSkill scans one skill directory tree.
func auditLocalSkill(name, dir string) []auditFinding {
	var files []string
	var findings []auditFinding
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Surface IO/permission failures instead of silently doing a partial
			// scan, but keep walking the rest of the tree.
			rel := auditRel(dir, path)
			findings = append(findings, auditFinding{
				auditWarn, name, rel,
				"unreadable during audit: " + err.Error(),
			})
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, path)
		return nil
	})

	hasBuildSource := false
	for _, f := range files {
		base := strings.ToLower(filepath.Base(f))
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".go" || ext == ".rs" || ext == ".c" || ext == ".cpp" ||
			base == "makefile" || base == "build.sh" || base == "go.mod" {
			hasBuildSource = true
			break
		}
	}

	for _, f := range files {
		rel := auditRel(dir, f)
		ext := strings.ToLower(filepath.Ext(f))
		switch {
		case filepath.Base(f) == "SKILL.md":
			findings = append(findings, auditSkillMarkdown(name, rel, f)...)
		case auditScriptExtensions[ext] || isExecutableText(f):
			findings = append(findings, auditScriptFile(name, rel, f)...)
		case ext == ".go":
			findings = append(findings, auditGoFile(name, rel, f)...)
		default:
			if fb := auditBinaryFile(name, rel, f, hasBuildSource); fb != nil {
				findings = append(findings, *fb)
			}
		}
	}
	return findings
}

// auditSkillMarkdown inspects SKILL.md instruction text (frontmatter stripped).
func auditSkillMarkdown(skill, rel, path string) []auditFinding {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(data)
	body := stripFrontmatter(content)
	var findings []auditFinding

	// Apply the shared risky-pattern set (prompt-injection, hidden-from-user,
	// pipe/base64, credential paths) to instruction text, matching the doctor
	// external-skill audit's WARN classification.
	for _, pattern := range skillAuditPatterns {
		if pattern.re.MatchString(content) {
			findings = append(findings, auditFinding{
				auditWarn, skill, rel,
				"SKILL.md matches risky pattern: " + pattern.name,
			})
		}
	}

	for _, comment := range reHTMLComment.FindAllString(body, -1) {
		if reImperative.MatchString(comment) {
			findings = append(findings, auditFinding{
				auditWarn, skill, rel,
				"HTML comment in SKILL.md contains imperative instructions",
			})
			break
		}
	}

	seenHost := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		urls := reAuditURL.FindAllString(line, -1)
		if len(urls) > 0 && reExfilVerb.MatchString(line) && reSensitiveData.MatchString(line) {
			findings = append(findings, auditFinding{
				auditCritical, skill, rel,
				"SKILL.md instructs exfiltrating sensitive data to an external URL",
			})
			continue
		}
		for _, u := range urls {
			host := urlHost(u)
			if host == "" || isDocHost(host) || seenHost[host] {
				continue
			}
			seenHost[host] = true
			findings = append(findings, auditFinding{
				auditWarn, skill, rel,
				"SKILL.md links to non-documentation host " + host,
			})
		}
	}
	return findings
}

// auditScriptFile inspects a bundled script for shell/exfil patterns.
func auditScriptFile(skill, rel, path string) []auditFinding {
	text, ok := readAuditFile(path)
	if !ok {
		if f := oversizedFinding(skill, rel, path); f != nil {
			return []auditFinding{*f}
		}
		return nil
	}
	var findings []auditFinding

	if rePipeToShell.MatchString(text) {
		findings = append(findings, auditFinding{
			auditCritical, skill, rel, "curl/wget output piped directly into a shell",
		})
	}
	if reBase64ToShell.MatchString(text) {
		findings = append(findings, auditFinding{
			auditCritical, skill, rel, "base64-decoded payload piped into a shell",
		})
	}
	findings = append(findings, credNetworkFindings(skill, rel, text)...)
	return findings
}

// auditGoFile inspects a bundled Go helper for the credential+network patterns.
// Go source is text (not caught by the binary check) and reNetworkCall already
// matches net/http, so a credential-harvesting helper must be scanned too.
func auditGoFile(skill, rel, path string) []auditFinding {
	text, ok := readAuditFile(path)
	if !ok {
		if f := oversizedFinding(skill, rel, path); f != nil {
			return []auditFinding{*f}
		}
		return nil
	}
	return credNetworkFindings(skill, rel, text)
}

// oversizedFinding reports a WARN for audit-relevant files that exceed the
// size limit: silently skipping them would let a skill evade scanning by
// padding a script past skillAuditMaxFileSize.
func oversizedFinding(skill, rel, path string) *auditFinding {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= skillAuditMaxFileSize {
		return nil
	}
	return &auditFinding{
		auditWarn, skill, rel,
		"file exceeds audit size limit and was not scanned",
	}
}

// credNetworkFindings reports a CRITICAL when a file both reads a credential
// path and makes a network call, or an INFO when it only makes a network call.
func credNetworkFindings(skill, rel, text string) []auditFinding {
	cred := reScriptCredential.MatchString(text)
	net := reNetworkCall.MatchString(text)
	switch {
	case cred && net:
		return []auditFinding{{
			auditCritical, skill, rel,
			"reads a credential path and makes a network call in the same file",
		}}
	case net:
		return []auditFinding{{
			auditInfo, skill, rel, "file makes a network call",
		}}
	}
	return nil
}

// readAuditFile returns file text, skipping files that are unreadable or larger
// than skillAuditMaxFileSize.
func readAuditFile(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > skillAuditMaxFileSize {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// auditBinaryFile flags committed executable binaries that lack accompanying
// build scripts or source in the skill tree.
func auditBinaryFile(skill, rel, path string, hasBuildSource bool) *auditFinding {
	if hasBuildSource || !isExecutableBinary(path) {
		return nil
	}
	return &auditFinding{
		auditWarn, skill, rel,
		"bundled executable binary without build script or source",
	}
}

func isExecutableBinary(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	buf = buf[:n]
	if hasExecutableMagic(buf) {
		return true
	}
	execBit := info.Mode()&0o111 != 0
	return execBit && bytes.IndexByte(buf, 0) >= 0
}

// isExecutableText identifies extensionless Unix helper scripts without
// treating executable binaries as text.
func isExecutableText(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	buf = buf[:n]
	return !hasExecutableMagic(buf) && bytes.IndexByte(buf, 0) < 0
}

func hasExecutableMagic(b []byte) bool {
	if len(b) >= 4 {
		if b[0] == 0x7f && b[1] == 'E' && b[2] == 'L' && b[3] == 'F' {
			return true // ELF
		}
		switch binary.BigEndian.Uint32(b[:4]) {
		case 0xFEEDFACE, 0xFEEDFACF, 0xCEFAEDFE, 0xCFFAEDFE, 0xCAFEBABE, 0xBEBAFECA:
			return true // Mach-O (thin + fat, both byte orders)
		}
	}
	if len(b) >= 2 && b[0] == 'M' && b[1] == 'Z' {
		return true // PE/COFF
	}
	return false
}

func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return content
	}
	nl := strings.IndexByte(content, '\n')
	rest := content[nl+1:]
	loc := regexp.MustCompile(`(?m)^---\s*$`).FindStringIndex(rest)
	if loc == nil {
		return content
	}
	return rest[loc[1]:]
}

// auditRel returns path relative to dir, falling back to the base name (or "."
// when path is empty, e.g. a walk error at the root).
func auditRel(dir, path string) string {
	if path == "" {
		return "."
	}
	if rel, err := filepath.Rel(dir, path); err == nil {
		return rel
	}
	return filepath.Base(path)
}

func urlHost(raw string) string {
	raw = strings.TrimRight(raw, ".,);]\"'>")
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func isDocHost(host string) bool {
	for _, d := range auditDocHosts {
		if strings.HasSuffix(d, ".") {
			if strings.HasPrefix(host, d) {
				return true
			}
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func auditSeverityRank(sev string) int {
	switch sev {
	case auditCritical:
		return 0
	case auditWarn:
		return 1
	default:
		return 2
	}
}

func sortAuditFindings(findings []auditFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if ra, rb := auditSeverityRank(a.severity), auditSeverityRank(b.severity); ra != rb {
			return ra < rb
		}
		if a.skill != b.skill {
			return a.skill < b.skill
		}
		if a.path != b.path {
			return a.path < b.path
		}
		return a.desc < b.desc
	})
}

func printAuditFindings(w io.Writer, findings []auditFinding) (crit, warn, info int) {
	for _, f := range findings {
		fmt.Fprintf(w, "%s %s %s: %s\n", f.severity, f.skill, f.path, f.desc)
		switch f.severity {
		case auditCritical:
			crit++
		case auditWarn:
			warn++
		case auditInfo:
			info++
		}
	}
	fmt.Fprintf(w, "\n%d finding(s): %d critical, %d warn, %d info\n", len(findings), crit, warn, info)
	return crit, warn, info
}
