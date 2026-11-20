package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Severities of rule findings. Only CRITICAL on an external item blocks.
const (
	sevCritical = "CRITICAL"
	sevHigh     = "HIGH"
	sevMedium   = "MEDIUM"
	sevLow      = "LOW"
)

type auditRule struct {
	id       string
	severity string
	re       *regexp.Regexp
	mask     bool              // evidence shown to the user is masked (secrets)
	skip     func(string) bool // true when the match is a placeholder, not a finding
}

const secretRef = `\$\{?[A-Za-z_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL)[A-Za-z_]*\}?|(?:~|\$HOME)/\.(?:ssh|aws|gnupg)\b|(?:^|[\s'"/@])\.env\b|\bid_rsa\b|\.netrc\b`

var auditRules = []auditRule{
	{id: "prompt-injection", severity: sevCritical, re: regexp.MustCompile(`(?i)\b(?:ignore|disregard|forget|override)\b[^.\n]{0,30}\b(?:previous|prior|above|earlier|system)\s+(?:instructions|prompts?|rules|messages|context)\b|\b(?:do not|don't|never)\s+(?:tell|inform|mention|reveal|disclose)(?:\s+\w+){0,3}\s+(?:to\s+)?the\s+user\b|\bwithout\s+(?:telling|informing|notifying)\s+the\s+user\b`)},
	{id: "exfiltration", severity: sevCritical, re: regexp.MustCompile(`(?i)\b(?:curl|wget)\b[^\n]*\s(?:-d|--data(?:-binary|-raw|-urlencode)?|-F|--form|-T|--upload-file|--post-data|--post-file|--body-data)[= ]\s*["']?@?[^\n]*(?:` + secretRef + `)|\b(?:printenv|env)\b[^\n|]*\|\s*(?:curl|wget|nc|ncat)\b`)},
	{id: "credential-read", severity: sevCritical, re: regexp.MustCompile(`(?i)\b(?:cat|less|more|head|tail|cp|scp|tar|zip|base64|read_text|readfile\w*)\b[^\n]*(?:(?:~|\$HOME|/Users/\w+|/home/\w+)/\.(?:ssh|aws|gnupg|netrc)\b|(?:^|[\s'"/])\.env(?:\.[a-z0-9]+)?\b)|\bsecurity\s+(?:find-(?:generic|internet)-password|dump-keychain)\b|\bsecret-tool\s+lookup\b`)},
	{id: "remote-exec", severity: sevCritical, re: regexp.MustCompile(`(?i)\b(?:curl|wget)\b[^|\n]*\|\s*(?:sudo\s+)?(?:ba|z)?sh\b|\bbase64\b\s+(?:-d|-D|--decode)\b[^\n]*\|\s*(?:ba|z)?sh\b|\b(?:ba|z)?sh\s+<\(\s*(?:curl|wget)\b`)},
	{id: "destructive", severity: sevHigh, re: regexp.MustCompile(`\brm\s+(?:-[a-zA-Z]+\s+)*-[a-zA-Z]*[rR][a-zA-Z]*\s+(?:-[a-zA-Z]+\s+)*(?:/|~|\$HOME|/\*|~/\*)(?:\s|$|["'])|(?:^\s*(?:\$\s+)?|[;&|]\s*)sudo\s+\S`)},
	{id: "agent-config-write", severity: sevHigh, re: regexp.MustCompile(`(?:>>?|\btee(?:\s+-a)?|\bcp|\bmv|\bsed\s+-i\S*)\s[^\n]*?(?:~|\$HOME)/\.(?:claude|codex|pi|omp|gemini|agents)/(?:[\w.-]+/)*(?:settings\.json|config\.toml|hooks\.json|CLAUDE\.md|AGENTS\.md|SOUL\.md)\b|\bopen\([^)\n]*\.(?:claude|codex|gemini)/[^)\n]*(?:settings\.json|CLAUDE\.md|AGENTS\.md)[^)\n]*["'](?:w|a)\+?["']`)},
	{id: "hardcoded-secret", severity: sevHigh, mask: true, re: regexp.MustCompile(`\b(?:sk-ant-[A-Za-z0-9_-]{20,}|sk-[A-Za-z0-9_-]{24,}|ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{40,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,})`), skip: isPlaceholderSecret},
	{id: "unpinned-runtime", severity: sevMedium, re: regexp.MustCompile(`\bnpx\s+(?:-y|--yes)\s+(?:@[\w.-]+/)?[\w.-]+(?:@latest)?(?:\s|$|["'])|\b(?:npx|pnpm\s+dlx|bunx)\s+(?:-y\s+|--yes\s+)?(?:@[\w.-]+/)?[\w.-]+@latest\b|\buvx\s+(?:--[\w-]+(?:[= ]\S+)?\s+)*[A-Za-z][\w.-]*(?:\s|$|["'])`)},
	{id: "suspicious-url", severity: sevMedium, re: regexp.MustCompile(`https?://(?:\d{1,3}\.){3}\d{1,3}\b`), skip: privateIPURL},
}

var rePrivateIP = regexp.MustCompile(`^https?://(?:127\.|10\.|192\.168\.|0\.0\.0\.0)`)

func privateIPURL(m string) bool { return rePrivateIP.MatchString(m) }

func isPlaceholderSecret(m string) bool {
	l := strings.ToLower(m)
	return strings.Contains(l, "example") || strings.Contains(l, "xxxx") || strings.Contains(l, "your") ||
		strings.Contains(l, "****") || strings.Contains(l, "...")
}

type ruleFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Lowered  bool   `json:"lowered,omitempty"`
	File     string `json:"file"`
	Match    string `json:"match"`
	Evidence string `json:"evidence"`
	Accepted bool   `json:"accepted,omitempty"`
}

func lowerSeverity(sev string) string {
	switch sev {
	case sevCritical:
		return sevHigh
	case sevHigh:
		return sevMedium
	default:
		return sevLow
	}
}

// severityRank orders findings so a duplicate with a higher severity replaces
// a stored lower one.
func severityRank(sev string) int {
	switch sev {
	case sevCritical:
		return 4
	case sevHigh:
		return 3
	case sevMedium:
		return 2
	case sevLow:
		return 1
	default:
		return 0
	}
}

func auditEvidence(rule, file, match string) string {
	norm := strings.Join(strings.Fields(match), " ")
	sum := sha256.Sum256([]byte(rule + "|" + file + "|" + norm))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func maskSecret(s string) string {
	if len(s) <= 6 {
		return "****"
	}
	return s[:4] + "****"
}

func shorten(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 90 {
		return s[:90] + "..."
	}
	return s
}

func newRuleFinding(rule auditRule, file, match string, lowered bool) ruleFinding {
	f := ruleFinding{Rule: rule.id, Severity: rule.severity, File: file, Evidence: auditEvidence(rule.id, file, match)}
	if lowered {
		f.Severity = lowerSeverity(rule.severity)
		f.Lowered = true
	}
	f.Match = shorten(match)
	if rule.mask {
		f.Match = maskSecret(match)
	}
	return f
}

func isMarkdownFile(file string) bool {
	ext := strings.ToLower(filepath.Ext(file))
	return ext == ".md" || ext == ".markdown"
}

// proseLines marks the lines of a Markdown file outside a code fence, where a
// match is lowered one by one, when it is framed.
func proseLines(file string, lines []string) []bool {
	out := make([]bool, len(lines))
	if !isMarkdownFile(file) {
		return out
	}
	inFence := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		out[i] = !inFence
	}
	return out
}

// reFraming is a word that turns the clause after it into a warning or an
// example: "do not run ...", "for example a page that says ...".
var reFraming = regexp.MustCompile(`(?i)\b(?:never|do not|don['’]t|avoid\w*|instead of|for example|such as|flag\w*|detect\w*|warn\w*)\b`)

// reClauseEnd ends a clause: a comma, semicolon or sentence end before a space.
var reClauseEnd = regexp.MustCompile(`[,;.!?]\s`)

// framed reports whether the match at line[start:end] only describes the
// pattern: it sits inside double quotation marks, or a framing word comes
// before it in the same clause.
func framed(line string, start, end int) bool {
	before, after := line[:start], line[end:]
	if strings.Count(before, `"`)%2 == 1 && strings.Contains(after, `"`) {
		return true
	}
	if strings.LastIndex(before, "“") > strings.LastIndex(before, "”") && strings.Contains(after, "”") {
		return true
	}
	if locs := reClauseEnd.FindAllStringIndex(before, -1); len(locs) > 0 {
		before = before[locs[len(locs)-1][1]:]
	}
	return reFraming.MatchString(before)
}

// ruleMatch returns the first match of rule on line that is not a placeholder.
// On a Markdown prose line it prefers a match that is not framed and reports
// lowered only when every match on the line is framed.
func ruleMatch(rule auditRule, line string, prose bool) (match string, lowered bool) {
	if !prose {
		m := rule.re.FindString(line)
		if m == "" || (rule.skip != nil && rule.skip(m)) {
			return "", false
		}
		return m, false
	}
	for _, loc := range rule.re.FindAllStringIndex(line, -1) {
		m := line[loc[0]:loc[1]]
		if rule.skip != nil && rule.skip(m) {
			continue
		}
		if !framed(line, loc[0], loc[1]) {
			return m, false
		}
		if match == "" {
			match, lowered = m, true
		}
	}
	return match, lowered
}

// loweredLines marks lines inside a Markdown code fence (for .md files) or a
// comment or docstring (for scripts). A match there is usually documentation.
func loweredLines(file string, lines []string) []bool {
	out := make([]bool, len(lines))
	if isMarkdownFile(file) {
		for i, prose := range proseLines(file, lines) {
			out[i] = !prose
		}
		return out
	}
	ext := strings.ToLower(filepath.Ext(file))
	if ext == ".py" {
		return pyLoweredLines(lines)
	}
	inBlock := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case inBlock:
			out[i] = true
			if strings.Contains(t, "*/") {
				inBlock = false
			}
		case strings.HasPrefix(t, "/*"):
			out[i] = true
			inBlock = !strings.Contains(t, "*/")
		case strings.HasPrefix(t, "//") && ext != ".sh":
			out[i] = true
		case strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "#!") && ext != ".js" && ext != ".mjs" && ext != ".ts" && ext != ".cjs":
			out[i] = true
		}
	}
	return out
}

// pyLoweredLines marks the lines of a Python file that are comments or
// docstrings. It reads the file as Python does: strings by their own delimiter
// with escapes, brackets and backslashes joining physical lines into logical
// ones. A docstring is a logical line that is one string literal and nothing
// else, and is the first statement of the file or follows a def or class
// header. Any other string is data the script can run: it is not lowered.
func pyLoweredLines(lines []string) []bool {
	out := make([]bool, len(lines))
	var (
		delim      string // delimiter of the string a line starts in, "" outside one
		depth      int    // open brackets
		joined     bool   // the previous line ended with a backslash outside a string
		start      = -1   // first physical line of the logical line being read
		strs       int    // string literals in it
		code       bool   // anything else in it
		last       byte   // its last character outside strings and comments
		prevHeader bool   // the logical line before it was a def or class header
		first      = true // no logical line has ended yet
	)
	for i, line := range lines {
		if delim == "" {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "#") {
				out[i] = !strings.HasPrefix(t, "#!")
				continue
			}
			if t == "" && start < 0 {
				continue
			}
		}
		if start < 0 {
			start, strs, code, last = i, 0, false, 0
		}
		joined = false
		for pos := 0; pos < len(line); {
			if delim != "" {
				end := pyStringEnd(line, pos, delim)
				if end < 0 {
					if len(delim) == 1 && !pyEndsWithBackslash(line) {
						delim = "" // an unterminated one-line string ends with its line
					}
					pos = len(line)
					continue
				}
				pos, delim = end+len(delim), ""
				continue
			}
			c := line[pos]
			switch {
			case c == ' ' || c == '\t' || c == '\r':
				pos++
			case c == '#':
				pos = len(line)
			case c == '"' || c == '\'':
				delim = line[pos : pos+1]
				if strings.HasPrefix(line[pos:], delim+delim+delim) {
					delim = line[pos : pos+3]
				}
				strs++
				pos += len(delim)
			case c == '\\' && strings.TrimSpace(line[pos+1:]) == "":
				joined = true
				pos = len(line)
			case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
				end := pos
				for end < len(line) && (line[end] == '_' || line[end] >= '0' && line[end] <= '9' || line[end] >= 'a' && line[end] <= 'z' || line[end] >= 'A' && line[end] <= 'Z') {
					end++
				}
				if end < len(line) && (line[end] == '"' || line[end] == '\'') && pyStringPrefix(line[pos:end]) {
					// An f- or t-string runs the code in its braces, so it is
					// never a docstring.
					code = code || strings.ContainsAny(line[pos:end], "fFtT")
					pos = end // r"...", b'...', f"""...""": part of the string
					continue
				}
				code, last, pos = true, line[end-1], end
			default:
				switch c {
				case '(', '[', '{':
					depth++
				case ')', ']', '}':
					if depth > 0 {
						depth--
					}
				}
				code, last = true, c
				pos++
			}
		}
		if delim != "" || depth > 0 || joined {
			continue // the logical line goes on
		}
		sole := strs == 1 && !code
		if sole && (first || prevHeader) {
			for k := start; k <= i; k++ {
				out[k] = true
			}
		}
		head := strings.TrimSpace(lines[start])
		prevHeader = !sole && last == ':' && (strings.HasPrefix(head, "def ") || strings.HasPrefix(head, "async def ") || strings.HasPrefix(head, "class "))
		first, start = false, -1
	}
	return out
}

// pyStringEnd returns the index of the first delim in line at or after pos that
// is not escaped by a backslash, or -1.
func pyStringEnd(line string, pos int, delim string) int {
	for j := pos; j < len(line); j++ {
		if line[j] == '\\' {
			j++
			continue
		}
		if strings.HasPrefix(line[j:], delim) {
			return j
		}
	}
	return -1
}

// pyEndsWithBackslash reports whether the line ends with an unescaped backslash.
func pyEndsWithBackslash(line string) bool {
	line = strings.TrimRight(line, "\r")
	n := 0
	for n < len(line) && line[len(line)-1-n] == '\\' {
		n++
	}
	return n%2 == 1
}

// pyStringPrefix reports whether word is a Python string prefix such as r, b, f or rb.
func pyStringPrefix(word string) bool {
	switch strings.ToLower(word) {
	case "r", "u", "b", "f", "t", "br", "rb", "fr", "rf", "tr", "rt":
		return true
	}
	return false
}

// hiddenLineBreak reports whether line holds a break that splitting on "\n"
// does not see: a carriage return before its end, which ends a Python line, or
// U+2028 or U+2029, which end a JavaScript comment. A file with such a break
// lowers nothing: the code after it may run, and it shifts how later lines are
// read.
func hiddenLineBreak(line string) bool {
	if i := strings.IndexByte(line, '\r'); i >= 0 && i < len(line)-1 {
		return true
	}
	return strings.ContainsRune(line, '\u2028') || strings.ContainsRune(line, '\u2029')
}

// scanText applies every rule line by line. file is the path relative to the
// item, used for lowering decisions and the evidence hash. A lowered match
// counts one level lower: a script comment, a Markdown code fence, or a framed
// match in Markdown prose.
func scanText(file, text string) []ruleFinding {
	var out []ruleFinding
	seen := map[string]int{}
	add := func(f ruleFinding) {
		key := f.Rule + f.Evidence
		if idx, ok := seen[key]; ok {
			if severityRank(f.Severity) > severityRank(out[idx].Severity) {
				out[idx] = f
			}
			return
		}
		seen[key] = len(out)
		out = append(out, f)
	}
	lines := strings.Split(text, "\n")
	lowered := loweredLines(file, lines)
	prose := proseLines(file, lines)
	breaks := false
	for _, line := range lines {
		breaks = breaks || hiddenLineBreak(line)
	}
	for i, line := range lines {
		// A line after one that ends with a backslash continues it in a shell
		// and in most languages, so it is never lowered.
		joined := i > 0 && strings.HasSuffix(strings.TrimSuffix(lines[i-1], "\r"), `\`)
		for _, rule := range auditRules {
			m, low := ruleMatch(rule, line, prose[i])
			if m == "" {
				continue
			}
			add(newRuleFinding(rule, file, m, (lowered[i] || low) && !breaks && !joined))
		}
	}
	// Unicode rules look at runes, not lines, and are never lowered: a hidden
	// character stays hidden inside a fence.
	var tags, hidden []string
	for pos, r := range text {
		switch {
		case r >= 0xE0000 && r <= 0xE007F:
			tags = append(tags, fmt.Sprintf("U+%X", r))
		case r == 0x200B || (r >= 0x202A && r <= 0x202E) || r == 0x2060 || (r >= 0x2066 && r <= 0x2069) || (r == 0xFEFF && pos > 0):
			hidden = append(hidden, fmt.Sprintf("U+%04X", r))
		}
	}
	if len(tags) > 0 {
		add(newRuleFinding(auditRule{id: "invisible-payload", severity: sevCritical}, file, strings.Join(tags, " "), false))
	}
	if len(hidden) > 0 {
		add(newRuleFinding(auditRule{id: "hidden-unicode", severity: sevHigh}, file, strings.Join(hidden, " "), false))
	}
	return out
}

var reSecretKeyName = regexp.MustCompile(`(?i)token|secret|password|api.?key|authorization|credential`)
var reEnvRef = regexp.MustCompile(`\$\{?\w|\$\(|\{\{|env:|\{env:|op://|keychain:|^<[^>]*>$`)

// scanMCPServer checks command, args, env and headers of one MCP entry.
func scanMCPServer(s mcpServerConfig) []ruleFinding {
	var out []ruleFinding
	seen := map[string]bool{}
	add := func(fs []ruleFinding) {
		for _, f := range fs {
			if !seen[f.Rule+f.Evidence] {
				seen[f.Rule+f.Evidence] = true
				out = append(out, f)
			}
		}
	}
	add(scanText("mcp."+s.Name+".command", strings.TrimSpace(s.Command+" "+strings.Join(s.Args, " "))))
	if s.URL != "" {
		add(scanText("mcp."+s.Name+".url", s.URL))
	}
	secretRule := auditRule{id: "plaintext-secret-mcp", severity: sevHigh, mask: true}
	for _, group := range []struct {
		name string
		m    map[string]string
	}{{"env", s.Env}, {"headers", s.Headers}} {
		keys := make([]string, 0, len(group.m))
		for k := range group.m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := strings.TrimSpace(group.m[k])
			if !reSecretKeyName.MatchString(k) || len(v) < 8 || reEnvRef.MatchString(v) {
				continue
			}
			f := newRuleFinding(secretRule, "mcp."+s.Name+"."+group.name, k+"="+v, false)
			f.Match = group.name + "." + k + "=" + maskSecret(v)
			add([]ruleFinding{f})
		}
	}
	return out
}

var auditTextExtensions = map[string]bool{
	".md": true, ".sh": true, ".bash": true, ".zsh": true, ".py": true, ".js": true,
	".mjs": true, ".cjs": true, ".ts": true, ".rb": true, ".pl": true,
}

// scanTree scans every text file under root, with paths relative to relBase.
func scanTree(root, relBase string) []ruleFinding {
	var out []ruleFinding
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !auditTextExtensions[strings.ToLower(filepath.Ext(d.Name()))] && !isExecutableText(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > skillAuditMaxFileSize {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || !utf8.Valid(data) {
			return nil
		}
		rel, _ := filepath.Rel(relBase, path)
		out = append(out, scanText(filepath.ToSlash(rel), string(data))...)
		return nil
	})
	return out
}

// auditItem is one synced thing: kind is skill, role, hook, mcp or plugin.
// External items come from outside the user's config root and are the only
// ones that can be blocked.
type auditItem struct {
	Kind     string
	Name     string
	External bool
	Findings []ruleFinding
}

func (i auditItem) ID() string { return i.Kind + ":" + i.Name }

func externalSourceItem(src externalSkillSource, home string) auditItem {
	name := sourceName(src)
	cachePath := filepath.Join(externalCacheDir(home), name)
	kind := "skill"
	if hasPluginManifest(cachePath) {
		kind = "plugin"
	}
	item := auditItem{Kind: kind, Name: name, External: true}
	if skills, err := discoverExternalSourceSkills(src, home); err == nil {
		for _, sk := range skills {
			item.Findings = append(item.Findings, scanTree(sk.Path, cachePath)...)
		}
	}
	if kind == "plugin" {
		if manifest, _, ok, err := parsePluginManifest(cachePath); err == nil && ok {
			if servers, _, err := discoverPluginMCPServers(cachePath, manifest.Schema, ""); err == nil {
				for _, s := range servers {
					item.Findings = append(item.Findings, scanMCPServer(s)...)
				}
			}
		}
		// Codex loads more of a plugin than the stdio servers tackroom writes:
		// every MCP transport, its own manifest overlay and hooks.
		for _, rel := range []string{"mcp.json", "plugin.json", ".codex-plugin/plugin.json", "hooks/hooks.json"} {
			if data, err := os.ReadFile(filepath.Join(cachePath, filepath.FromSlash(rel))); err == nil && len(data) <= skillAuditMaxFileSize && utf8.Valid(data) {
				item.Findings = append(item.Findings, scanText(rel, string(data))...)
			}
		}
	}
	return item
}

// collectAuditItems enumerates every synced item kind with its findings.
func collectAuditItems(repoRoot, home string, cfg config) []auditItem {
	var items []auditItem
	lock, _ := readLockFile(repoRoot)
	owned := map[string]bool{}
	for _, e := range lock.ExternalSkills {
		for _, n := range e.Materialized.Values() {
			owned[n] = true
		}
	}
	skillsDir := filepath.Join(repoRoot, "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range entries {
			dir := filepath.Join(skillsDir, e.Name())
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || owned[e.Name()] || !hasFile(filepath.Join(dir, "SKILL.md")) {
				continue
			}
			items = append(items, auditItem{Kind: "skill", Name: e.Name(), Findings: scanTree(dir, dir)})
		}
	}
	for _, src := range cfg.ExternalSkills {
		if hasDir(filepath.Join(externalCacheDir(home), sourceName(src), ".git")) {
			items = append(items, externalSourceItem(src, home))
		}
	}
	if entries, err := os.ReadDir(filepath.Join(repoRoot, "agents")); err == nil {
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(repoRoot, "agents", e.Name()))
			if err == nil {
				items = append(items, auditItem{Kind: "role", Name: strings.TrimSuffix(e.Name(), ".md"), Findings: scanText(e.Name(), string(data))})
			}
		}
	}
	for _, h := range cfg.Hooks {
		item := auditItem{Kind: "hook", Name: h.Name, Findings: scanText("command", h.Command)}
		for _, tok := range strings.Fields(h.Command) {
			if p := hookScriptPath(tok, repoRoot, home); p != "" {
				rel, _ := filepath.Rel(repoRoot, p)
				if data, err := os.ReadFile(p); err == nil && utf8.Valid(data) {
					item.Findings = append(item.Findings, scanText(filepath.ToSlash(rel), string(data))...)
				}
			}
		}
		items = append(items, item)
	}
	for _, s := range cfg.MCPServers {
		items = append(items, auditItem{Kind: "mcp", Name: s.Name, Findings: scanMCPServer(s)})
	}
	for _, p := range localMarketplacePlugins(repoRoot) {
		items = append(items, auditItem{Kind: "plugin", Name: p.name, Findings: scanTree(p.dir, p.dir)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID() < items[j].ID() })
	return items
}

// hookScriptPath maps a hook command token to a regular file inside the config
// root, or "" when it points elsewhere.
func hookScriptPath(tok, repoRoot, home string) string {
	tok = strings.Trim(tok, `"'`)
	for _, prefix := range []string{"~", "$HOME", "${HOME}"} {
		if strings.HasPrefix(tok, prefix+"/") {
			tok = filepath.Join(home, tok[len(prefix)+1:])
		}
	}
	if !filepath.IsAbs(tok) {
		tok = filepath.Join(repoRoot, tok)
	}
	if rest, err := filepath.Rel(filepath.Join(home, ".agents"), tok); err == nil && !strings.HasPrefix(rest, "..") {
		tok = filepath.Join(repoRoot, rest)
	}
	if rel, err := filepath.Rel(repoRoot, tok); err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	if info, err := os.Stat(tok); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return tok
}

type localPlugin struct{ name, dir string }

func localMarketplacePlugins(repoRoot string) []localPlugin {
	data, err := os.ReadFile(claudeMarketplacePath(repoRoot))
	if err != nil {
		return nil
	}
	var raw struct {
		Metadata struct {
			PluginRoot string `json:"pluginRoot"`
		} `json:"metadata"`
		Plugins []claudeMarketplaceEntry `json:"plugins"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	var out []localPlugin
	for _, e := range raw.Plugins {
		if dir, ok := relativePluginDir(e.Source, raw.Metadata.PluginRoot); ok && e.Name != "" {
			out = append(out, localPlugin{e.Name, filepath.Join(repoRoot, filepath.FromSlash(dir))})
		}
	}
	return out
}
