package app

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	openAISkillsURL        = "https://api.openai.com/v1/skills"
	publishMaxFiles        = 500
	publishMaxUncompressed = 25 * 1024 * 1024 // 25 MB
	publishMaxZipBytes     = 50 * 1024 * 1024 // 50 MB
)

const (
	publishActionCreate = "create"
	publishActionUpdate = "update"
	publishActionSkip   = "skip"
)

// skillRegistry abstracts the remote skill registry so publish is testable
// without live network. createSkill uploads a brand-new skill and returns its
// id and first version; addVersion uploads a new version of an existing skill;
// setDefaultVersion pins which version the registry serves by default.
type skillRegistry interface {
	createSkill(name string, zipData []byte) (skillID string, version string, err error)
	addVersion(skillID string, zipData []byte) (version string, err error)
	setDefaultVersion(skillID string, version string) error
}

// registryFactory builds a registry for one target. It is a field so tests can
// inject a fake; the default builds an httpSkillRegistry from the target's key.
type registryFactory func(target publishTarget) (skillRegistry, error)

type publishOptions struct {
	ConfigPath string
	Target     string
	Skills     string
	DryRun     bool
	JSON       bool
	AssumeYes  bool
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Factory    registryFactory
}

type publishItem struct {
	Skill            string `json:"skill"`
	Action           string `json:"action"`
	SkillID          string `json:"skill_id,omitempty"`
	Version          string `json:"version,omitempty"`
	Files            int    `json:"files"`
	UncompressedByte int    `json:"uncompressed_bytes"`
	ZipBytes         int    `json:"zip_bytes"`
	ContentHash      string `json:"content_hash"`

	zipData []byte // not serialized; carried between plan and apply
}

type publishTargetResult struct {
	Target string        `json:"target"`
	Items  []publishItem `json:"items"`
}

type publishReport struct {
	DryRun  bool                  `json:"dry_run"`
	Targets []publishTargetResult `json:"targets"`
}

func runPublishCommand(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts publishOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	fs.StringVar(&opts.Target, "target", "", "Limit to a single publish target by name")
	fs.StringVar(&opts.Skills, "skills", "", "Comma-separated skill names to limit this run")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "Show what would be published without uploading or writing the lock")
	fs.BoolVar(&opts.JSON, "json", false, "Emit the plan/result as JSON")
	fs.BoolVar(&opts.AssumeYes, "yes", false, "Skip the confirmation prompt")
	fs.BoolVar(&opts.AssumeYes, "y", false, "Skip the confirmation prompt (alias for --yes)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("publish does not accept positional arguments")
	}
	return runPublish(opts)
}

func runPublish(opts publishOptions) error {
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Factory == nil {
		opts.Factory = defaultRegistryFactory
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	configPath, err := resolveConfigPath(opts.ConfigPath, home)
	if err != nil {
		return err
	}
	repoRoot := filepath.Dir(configPath)
	if err := checkConfigRoot(repoRoot); err != nil {
		return err
	}
	cfg, err := loadConfig(repoRoot, home, configPath)
	if err != nil {
		return err
	}

	targets, err := selectPublishTargets(cfg, opts.Target)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Fprintln(opts.Stdout, "no enabled publish targets")
		return nil
	}

	expected, err := expectedSkills(repoRoot, home, cfg)
	if err != nil {
		return err
	}
	lock, err := readLockFile(repoRoot)
	if err != nil {
		return err
	}

	skillFilter := parseCommaSet(opts.Skills)

	report := publishReport{DryRun: opts.DryRun}
	for _, target := range targets {
		items, err := planTarget(target, expected, lock, skillFilter)
		if err != nil {
			return err
		}
		report.Targets = append(report.Targets, publishTargetResult{Target: target.Name, Items: items})
	}

	if opts.DryRun {
		return emitPublishReport(opts, report)
	}

	// Anything to upload?
	pending := 0
	for _, tr := range report.Targets {
		for _, it := range tr.Items {
			if it.Action != publishActionSkip {
				pending++
			}
		}
	}
	if pending == 0 {
		return emitPublishReport(opts, report)
	}

	// A real publish sends skill content to an external registry, so always
	// warn (even with --yes) and keep the warning/plan/prompt on stderr so
	// --json stdout stays parseable.
	printPublishWarning(opts.Stderr)
	if !opts.AssumeYes {
		printPublishPlanText(opts.Stderr, report)
		ok, err := confirmPublish(opts)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(opts.Stderr, "publish aborted")
			return nil
		}
	}

	for ti := range report.Targets {
		target := targets[ti]
		var reg skillRegistry
		for ii := range report.Targets[ti].Items {
			item := &report.Targets[ti].Items[ii]
			if item.Action == publishActionSkip {
				continue
			}
			if reg == nil {
				reg, err = opts.Factory(target)
				if err != nil {
					return err
				}
			}
			if err := applyPublishItem(target, reg, item); err != nil {
				return fmt.Errorf("publish %s/%s: %w", target.Name, item.Skill, err)
			}
			setPublishedLockEntry(&lock, publishedLockEntry{
				Target:      target.Name,
				Skill:       item.Skill,
				SkillID:     item.SkillID,
				Version:     item.Version,
				ContentHash: item.ContentHash,
				PublishedAt: time.Now().UTC().Format(time.RFC3339),
			})
			// Persist after each successful upload so a later failure in the
			// same run cannot orphan an already-published skill/version.
			if err := writeLockFile(repoRoot, lock); err != nil {
				return err
			}
		}
	}
	return emitPublishReport(opts, report)
}

func selectPublishTargets(cfg config, only string) ([]publishTarget, error) {
	only = strings.TrimSpace(only)
	var out []publishTarget
	for _, t := range cfg.PublishTargets {
		if only != "" {
			if t.Name != only {
				continue
			}
			if !t.Enabled {
				return nil, fmt.Errorf("publish target %q is not enabled", t.Name)
			}
			out = append(out, t)
			return out, nil
		}
		if t.Enabled {
			out = append(out, t)
		}
	}
	if only != "" {
		return nil, fmt.Errorf("unknown publish target %q", only)
	}
	return out, nil
}

func planTarget(target publishTarget, expected map[string]string, lock lockFile, skillFilter map[string]struct{}) ([]publishItem, error) {
	var items []publishItem
	for _, skill := range target.Skills {
		if skill == "" {
			continue
		}
		if skillFilter != nil {
			if _, ok := skillFilter[skill]; !ok {
				continue
			}
		}
		dir, ok := expected[skill]
		if !ok {
			return nil, fmt.Errorf("publish target %s lists skill %q, which is not a known skill", target.Name, skill)
		}
		if err := validateSkillForPublish(dir, skill); err != nil {
			return nil, fmt.Errorf("publish target %s skill %q: %w", target.Name, skill, err)
		}
		zipData, hash, files, uncompressed, err := buildSkillBundle(dir, skill)
		if err != nil {
			return nil, fmt.Errorf("publish target %s skill %q: %w", target.Name, skill, err)
		}
		item := publishItem{
			Skill:            skill,
			Files:            files,
			UncompressedByte: uncompressed,
			ZipBytes:         len(zipData),
			ContentHash:      hash,
			zipData:          zipData,
		}
		if prev, ok := findPublishedLockEntry(lock, target.Name, skill); ok {
			item.SkillID = prev.SkillID
			item.Version = prev.Version
			if prev.ContentHash == hash {
				item.Action = publishActionSkip
			} else {
				item.Action = publishActionUpdate
			}
		} else {
			item.Action = publishActionCreate
		}
		items = append(items, item)
	}
	return items, nil
}

func applyPublishItem(target publishTarget, reg skillRegistry, item *publishItem) error {
	switch item.Action {
	case publishActionCreate:
		id, version, err := reg.createSkill(item.Skill, item.zipData)
		if err != nil {
			return err
		}
		item.SkillID = id
		item.Version = version
	case publishActionUpdate:
		if strings.TrimSpace(item.SkillID) == "" {
			return errors.New("cannot add version without a recorded skill_id")
		}
		version, err := reg.addVersion(item.SkillID, item.zipData)
		if err != nil {
			return err
		}
		item.Version = version
	default:
		return nil
	}
	if target.VersionStrategy == publishStrategySetDefault && item.SkillID != "" && item.Version != "" {
		if err := reg.setDefaultVersion(item.SkillID, item.Version); err != nil {
			return err
		}
	}
	return nil
}

// validateSkillForPublish runs the same SKILL.md front-matter checks as local
// discovery so a skill that fails validation never reaches the registry.
func validateSkillForPublish(dir string, skill string) error {
	skillMD := filepath.Join(dir, "SKILL.md")
	if !hasFile(skillMD) {
		return errors.New("missing SKILL.md")
	}
	fm, err := parseSkillSpecFrontmatter(skillMD)
	if err != nil {
		return fmt.Errorf("SKILL.md: %w", err)
	}
	if problems := validateSkillSpecFields(fm, skill); len(problems) > 0 {
		return fmt.Errorf("SKILL.md: %s", strings.Join(problems, "; "))
	}
	return nil
}

// buildSkillBundle zips dir under a single top-level folder named skill and
// returns the zip bytes, a content hash, the file count, and the uncompressed
// size. The hash is over file paths and contents only (not zip metadata), so it
// is stable across runs and is the idempotency key stored in the lock.
func buildSkillBundle(dir string, skill string) (zipData []byte, contentHash string, files int, uncompressed int, err error) {
	type bundleFile struct {
		rel  string
		abs  string
		size int
	}
	var collected []bundleFile
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if isBundleExcludedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.Name() == ".DS_Store" {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		collected = append(collected, bundleFile{rel: filepath.ToSlash(rel), abs: path, size: int(info.Size())})
		return nil
	})
	if walkErr != nil {
		return nil, "", 0, 0, walkErr
	}
	if len(collected) == 0 {
		return nil, "", 0, 0, errors.New("skill directory is empty")
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].rel < collected[j].rel })

	for _, f := range collected {
		uncompressed += f.size
	}
	if len(collected) > publishMaxFiles {
		return nil, "", 0, 0, fmt.Errorf("bundle has %d files, exceeds limit of %d", len(collected), publishMaxFiles)
	}
	if uncompressed > publishMaxUncompressed {
		return nil, "", 0, 0, fmt.Errorf("bundle is %d bytes uncompressed, exceeds limit of %d", uncompressed, publishMaxUncompressed)
	}

	hasher := sha256.New()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range collected {
		data, readErr := os.ReadFile(f.abs)
		if readErr != nil {
			return nil, "", 0, 0, readErr
		}
		// hash: length-prefixed path and content, so no path/content boundary is ambiguous.
		fmt.Fprintf(hasher, "%d:%s\n%d:", len(f.rel), f.rel, len(data))
		hasher.Write(data)
		w, createErr := zw.Create(skill + "/" + f.rel)
		if createErr != nil {
			return nil, "", 0, 0, createErr
		}
		if _, writeErr := w.Write(data); writeErr != nil {
			return nil, "", 0, 0, writeErr
		}
	}
	if closeErr := zw.Close(); closeErr != nil {
		return nil, "", 0, 0, closeErr
	}
	if buf.Len() > publishMaxZipBytes {
		return nil, "", 0, 0, fmt.Errorf("bundle zip is %d bytes, exceeds limit of %d", buf.Len(), publishMaxZipBytes)
	}
	return buf.Bytes(), "sha256:" + hex.EncodeToString(hasher.Sum(nil)), len(collected), uncompressed, nil
}

func isBundleExcludedDir(name string) bool {
	return name == ".git"
}

func findPublishedLockEntry(lock lockFile, target string, skill string) (publishedLockEntry, bool) {
	for _, e := range lock.PublishedSkills {
		if e.Target == target && e.Skill == skill {
			return e, true
		}
	}
	return publishedLockEntry{}, false
}

func setPublishedLockEntry(lock *lockFile, entry publishedLockEntry) {
	for i := range lock.PublishedSkills {
		if lock.PublishedSkills[i].Target == entry.Target && lock.PublishedSkills[i].Skill == entry.Skill {
			lock.PublishedSkills[i] = entry
			return
		}
	}
	lock.PublishedSkills = append(lock.PublishedSkills, entry)
	sort.Slice(lock.PublishedSkills, func(i, j int) bool {
		if lock.PublishedSkills[i].Target != lock.PublishedSkills[j].Target {
			return lock.PublishedSkills[i].Target < lock.PublishedSkills[j].Target
		}
		return lock.PublishedSkills[i].Skill < lock.PublishedSkills[j].Skill
	})
}

func parseCommaSet(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	set := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			set[p] = struct{}{}
		}
	}
	return set
}

func printPublishWarning(w io.Writer) {
	fmt.Fprintln(w, "About to upload skills to a remote registry (US-only data residency, no Zero Data Retention).")
	fmt.Fprintln(w, "Do not publish skills that carry secrets or private vault content.")
}

func confirmPublish(opts publishOptions) (bool, error) {
	fmt.Fprint(opts.Stderr, "Proceed? [y/N]: ")
	reader := bufio.NewReader(opts.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func emitPublishReport(opts publishOptions, report publishReport) error {
	if opts.JSON {
		enc := json.NewEncoder(opts.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	printPublishPlanText(opts.Stdout, report)
	return nil
}

func printPublishPlanText(w io.Writer, report publishReport) {
	verb := "published"
	if report.DryRun {
		verb = "planned (dry-run)"
	}
	for _, tr := range report.Targets {
		fmt.Fprintf(w, "target %s:\n", tr.Target)
		if len(tr.Items) == 0 {
			fmt.Fprintln(w, "  (no skills)")
			continue
		}
		for _, it := range tr.Items {
			switch it.Action {
			case publishActionSkip:
				fmt.Fprintf(w, "  skip    %s (up to date, %s)\n", it.Skill, it.ContentHash)
			default:
				ver := it.Version
				if ver == "" {
					ver = "?"
				}
				fmt.Fprintf(w, "  %-7s %s (%d files, %d bytes) -> %s v%s\n", it.Action, it.Skill, it.Files, it.UncompressedByte, verb, ver)
			}
		}
	}
}

// ---- default HTTP registry (OpenAI /v1/skills) ----

func defaultRegistryFactory(target publishTarget) (skillRegistry, error) {
	keyEnv := target.APIKeyEnv
	if keyEnv == "" {
		keyEnv = publishDefaultAPIKeyEnv
	}
	key := strings.TrimSpace(os.Getenv(keyEnv))
	if key == "" {
		return nil, fmt.Errorf("publish target %s: environment variable %s is not set", target.Name, keyEnv)
	}
	return &httpSkillRegistry{apiKey: key, client: http.DefaultClient, baseURL: openAISkillsURL}, nil
}

type httpSkillRegistry struct {
	apiKey  string
	client  *http.Client
	baseURL string
}

func (r *httpSkillRegistry) createSkill(name string, zipData []byte) (string, string, error) {
	body, err := r.postZip(r.baseURL, name, zipData)
	if err != nil {
		return "", "", err
	}
	id, version := extractSkillID(body), extractVersion(body)
	if id == "" || version == "" {
		return "", "", fmt.Errorf("registry response missing skill_id or version")
	}
	return id, version, nil
}

func (r *httpSkillRegistry) addVersion(skillID string, zipData []byte) (string, error) {
	body, err := r.postZip(r.baseURL+"/"+skillID+"/versions", skillID, zipData)
	if err != nil {
		return "", err
	}
	version := extractVersion(body)
	if version == "" {
		return "", fmt.Errorf("registry response missing version")
	}
	return version, nil
}

func (r *httpSkillRegistry) setDefaultVersion(skillID string, version string) error {
	v, err := strconv.Atoi(version)
	if err != nil {
		return fmt.Errorf("set default version: %q is not an integer version", version)
	}
	payload, _ := json.Marshal(map[string]int{"default_version": v})
	req, err := http.NewRequest(http.MethodPost, r.baseURL+"/"+skillID, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")
	_, err = r.do(req)
	return err
}

func (r *httpSkillRegistry) postZip(url string, name string, zipData []byte) (map[string]any, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("files", name+".zip")
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(zipData); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return r.do(req)
}

func (r *httpSkillRegistry) do(req *http.Request) (map[string]any, error) {
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("registry returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var body map[string]any
	if len(data) > 0 {
		_ = json.Unmarshal(data, &body)
	}
	return body, nil
}

func extractSkillID(body map[string]any) string {
	for _, key := range []string{"id", "skill_id"} {
		if v, ok := body[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func extractVersion(body map[string]any) string {
	for _, key := range []string{"version", "default_version", "latest_version"} {
		if v, ok := body[key]; ok {
			switch t := v.(type) {
			case string:
				if t != "" {
					return t
				}
			case float64:
				if t == float64(int64(t)) {
					return strconv.FormatInt(int64(t), 10)
				}
				return strconv.FormatFloat(t, 'f', -1, 64)
			}
		}
	}
	return ""
}
