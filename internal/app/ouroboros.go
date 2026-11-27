package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yourconscience/tackroom/internal/agentrole"
)

// Ouroboros keeps its whole configuration in one settings document that the
// running app writes too. tackroom edits it only under Ouroboros's own lock
// file, rewrites only the keys it changes, and leaves the bytes of every other
// key as they were. OUROBOROS_SUBAGENTS and OUROBOROS_SKILL_SOURCES are stored
// as JSON strings holding a JSON document; MCP_SERVERS is a list of objects.
const (
	ouroborosSubagentsKey    = "OUROBOROS_SUBAGENTS"
	ouroborosSourcesKey      = "OUROBOROS_SKILL_SOURCES"
	ouroborosSkillsFolderKey = "OUROBOROS_SKILLS_REPO_PATH"
	ouroborosMCPKey          = "MCP_SERVERS"
	ouroborosRulesKey        = "OUROBOROS_SAFETY_OWNER_RULES"
	ouroborosRowPrefix       = "tackroom."
	ouroborosMaxRows         = 50
	ouroborosMaxRulesBytes   = 64000
	ouroborosMaxMCPID        = 24
	ouroborosLocalSourceID   = "my-folder"
	ouroborosFolderItem      = "config " + ouroborosSkillsFolderKey
)

var (
	ouroborosIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	ouroborosIDUnsafe    = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
	ouroborosMCPIDUnsafe = regexp.MustCompile(`[^A-Za-z0-9_]+`)
	ouroborosEfforts     = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}
)

// ouroborosSettingsPath follows Ouroboros's launch roots: an explicit settings
// path, else <data dir>/settings.json, else <app root>/data/settings.json.
func ouroborosSettingsPath(home string) string {
	if path := strings.TrimSpace(os.Getenv("OUROBOROS_SETTINGS_PATH")); path != "" {
		return path
	}
	data := strings.TrimSpace(os.Getenv("OUROBOROS_DATA_DIR"))
	if data == "" {
		root := strings.TrimSpace(os.Getenv("OUROBOROS_APP_ROOT"))
		if root == "" {
			root = filepath.Join(home, "Ouroboros")
		}
		data = filepath.Join(root, "data")
	}
	return filepath.Join(data, "settings.json")
}

// detectOuroboros reports an Ouroboros install: it has no CLI on PATH, so its
// settings document is the sign.
func detectOuroboros(home string) bool {
	info, err := os.Stat(ouroborosSettingsPath(home))
	return err == nil && info.Mode().IsRegular()
}

// jsonObject is a JSON object that keeps its key order and the original bytes
// of every value it does not change.
type jsonObject struct {
	keys    []string
	values  map[string]json.RawMessage
	changed map[string]bool
}

func parseJSONObject(data []byte) (*jsonObject, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	obj := &jsonObject{values: map[string]json.RawMessage{}, changed: map[string]bool{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if _, seen := obj.values[key]; !seen {
			obj.keys = append(obj.keys, key)
		}
		obj.values[key] = raw
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("data after the JSON object")
	}
	return obj, nil
}

func (o *jsonObject) raw(key string) (json.RawMessage, bool) {
	raw, ok := o.values[key]
	return raw, ok
}

func (o *jsonObject) str(key string) string {
	var s string
	_ = json.Unmarshal(o.values[key], &s)
	return s
}

func (o *jsonObject) set(key string, value any) error {
	data, err := marshalJSONNoEscape(value)
	if err != nil {
		return err
	}
	if old, ok := o.values[key]; ok && bytes.Equal(old, data) {
		return nil
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key], o.changed[key] = data, true
	return nil
}

func (o *jsonObject) delete(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
	o.changed[key] = true
}

// MarshalJSON writes the object compactly, as Ouroboros stores nested rows.
func (o *jsonObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := marshalJSONNoEscape(key)
		b.Write(name)
		b.WriteByte(':')
		if err := json.Compact(&b, o.values[key]); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// document writes the top level as Ouroboros does (json.dumps, indent=2, no
// trailing newline); values it did not change keep their bytes.
func (o *jsonObject) document() ([]byte, error) {
	if len(o.keys) == 0 {
		return []byte("{}"), nil
	}
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, key := range o.keys {
		if i > 0 {
			b.WriteString(",\n")
		}
		name, _ := marshalJSONNoEscape(key)
		b.WriteString("  ")
		b.Write(name)
		b.WriteString(": ")
		if !o.changed[key] {
			b.Write(o.values[key])
		} else if err := json.Indent(&b, o.values[key], "  ", "  "); err != nil {
			return nil, err
		}
	}
	b.WriteString("\n}")
	return b.Bytes(), nil
}

func marshalJSONNoEscape(value any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// embeddedJSON returns the document a string-valued setting holds (an object
// or list stored directly is read as it is).
func embeddedJSON(raw json.RawMessage) []byte {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []byte(text)
	}
	return raw
}

func readOuroborosSettings(home string) (*jsonObject, error) {
	path := ouroborosSettingsPath(home)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s does not exist; open Ouroboros once so it writes its settings, then sync again", path)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	obj, err := parseJSONObject(data)
	if err != nil {
		return nil, fmt.Errorf("%s is not readable JSON (%v); tackroom leaves it alone", path, err)
	}
	return obj, nil
}

// updateOuroborosSettings applies edit to the settings document under
// Ouroboros's lock file and replaces the file atomically when edit changed it.
func updateOuroborosSettings(home string, edit func(*jsonObject) error) error {
	path := ouroborosSettingsPath(home)
	if os.Getenv("OUROBOROS_SETTINGS_SHA256") != "" {
		return fmt.Errorf("%s is pinned by OUROBOROS_SETTINGS_SHA256; tackroom does not write a pinned settings file", path)
	}
	unlock, err := lockOuroborosSettings(path)
	if err != nil {
		return err
	}
	defer unlock()
	doc, err := readOuroborosSettings(home)
	if err != nil {
		return err
	}
	if err := edit(doc); err != nil {
		return err
	}
	if len(doc.changed) == 0 {
		return nil
	}
	data, err := doc.document()
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return writeFileAtomic(path, data)
}

// lockOuroborosSettings takes settings.json.lock the way Ouroboros's writers
// do: exclusive create, a lock older than 10s is stale, give up after 2s.
func lockOuroborosSettings(path string) (func(), error) {
	lock := path + ".lock"
	deadline := time.Now().Add(2 * time.Second)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			return func() { f.Close(); os.Remove(lock) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if info, err := os.Stat(lock); err == nil && time.Since(info.ModTime()) > 10*time.Second {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s is held by another writer; try again once Ouroboros has saved its settings", lock)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeFileAtomic(path string, data []byte) error {
	mode := fs.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// --- skills: Ouroboros reads them from its "My folder" setting ---

func ouroborosFolderState(doc *jsonObject, repoRoot, home string) (folder string, reads bool) {
	folder = strings.TrimSpace(doc.str(ouroborosSkillsFolderKey))
	if folder == "" {
		return "", false
	}
	path := expandPath(folder, home)
	return folder, sameResolvedPath(path, repoRoot) || sameResolvedPath(path, filepath.Join(repoRoot, "skills"))
}

// patchOuroborosSkillsFolder points an unset My folder at the config root. An
// owner's own folder is never replaced; status reports it instead.
func patchOuroborosSkillsFolder(home, repoRoot string, _ config) (bool, error) {
	changed := false
	err := updateOuroborosSettings(home, func(doc *jsonObject) error {
		if folder, _ := ouroborosFolderState(doc, repoRoot, home); folder != "" {
			return nil
		}
		changed = true
		return doc.set(ouroborosSkillsFolderKey, configSkillsDirValue(home, repoRoot))
	})
	return changed, err
}

func inspectOuroborosAgent(agent agentConfig, expected map[string]string, agentsSkillRoot string, cfg config, home string) (agentReport, error) {
	report := agentReport{Name: agent.Name, SkillRoot: agent.SkillRoot, AgentRoot: agent.AgentRoot, ExpectedSkills: expected, Detected: isDetected(agent)}
	if !report.Detected {
		return report, nil
	}
	repoRoot := filepath.Dir(agentsSkillRoot)
	doc, err := readOuroborosSettings(home)
	if err != nil {
		return agentReport{}, err
	}
	switch folder, reads := ouroborosFolderState(doc, repoRoot, home); {
	case reads:
		report.Managed = append(report.Managed, sortedKeys(expected)...)
	case folder == "":
		report.Missing = append(report.Missing, ouroborosFolderItem)
		report.Adds = append(report.Adds, ouroborosFolderItem)
	default:
		addSoftConflict(&report, fmt.Sprintf("Ouroboros reads skills from %s (Settings › Skills › My folder), not %s; point it at the config root to see tackroom's skills", folder, displayPath(repoRoot, home)))
	}
	if err := augmentMCPReport(&report, agent, cfg, home); err != nil {
		return agentReport{}, err
	}
	if err := inspectOuroborosRoles(&report, doc, repoRoot, agent); err != nil {
		return agentReport{}, err
	}
	if err := augmentSettingsReport(&report, agent, home); err != nil {
		return agentReport{}, err
	}
	sortReportLists(&report)
	report.Synced = isReportSynced(report)
	return report, nil
}

// addSoftConflict records a conflict that leaves only its own surface alone;
// the rest of the agent still syncs (as a role conflict does).
func addSoftConflict(report *agentReport, msg string) {
	report.Conflicts = append(report.Conflicts, msg)
	report.RoleConflicts = append(report.RoleConflicts, msg)
}

// --- roles: rows of OUROBOROS_SUBAGENTS whose id starts with "tackroom." ---

// ouroborosRow is one tackroom row in the canonical shape and key order of
// Ouroboros's serializer: every row is an agent_session, which always carries
// credential_profile_id and access, and never delivery or minted_from.
type ouroborosRow struct {
	SubagentID     string         `json:"subagent_id"`
	RecommendedUse string         `json:"recommended_use"`
	Route          ouroborosRoute `json:"route"`
	Effort         string         `json:"effort,omitempty"`
	Access         string         `json:"access"`
	Enabled        *bool          `json:"enabled,omitempty"`
	ReviewEligible bool           `json:"review_eligible,omitempty"`
}

type ouroborosRoute struct {
	Kind                string `json:"kind"`
	TargetID            string `json:"target_id"`
	CredentialProfileID string `json:"credential_profile_id"`
}

// ouroborosRowFacts are the fields of any stored row that name its engine.
type ouroborosRowFacts struct {
	SubagentID           string         `json:"subagent_id"`
	Route                ouroborosRoute `json:"route"`
	Effort               string         `json:"effort"`
	Access               string         `json:"access"`
	ProcessingPreference string         `json:"processing_preference"`
	Enabled              *bool          `json:"enabled"`
	ReviewEligible       bool           `json:"review_eligible"`
	MintedFrom           string         `json:"minted_from"`
}

// engine is Ouroboros's engine handle (target, effort, session access, account
// pin, processing): two unmarked rows with one handle are refused on save.
func (r ouroborosRowFacts) engine() string {
	access := ""
	if r.Route.Kind == "agent_session" && r.Access != "" && r.Access != "full" {
		access = r.Access
	}
	processing := r.ProcessingPreference
	if processing == "standard" {
		processing = ""
	}
	return strings.Join([]string{r.Route.Kind, r.Route.TargetID, r.Effort, access, r.Route.CredentialProfileID, processing}, "|")
}

type ouroborosRoster struct {
	enabled bool
	items   []json.RawMessage
	facts   []ouroborosRowFacts
}

func readOuroborosRoster(doc *jsonObject) (ouroborosRoster, error) {
	raw, ok := doc.raw(ouroborosSubagentsKey)
	if !ok || bytes.Equal(raw, []byte(`""`)) || bytes.Equal(raw, []byte("null")) {
		return ouroborosRoster{}, errors.New("ouroboros has no saved subagent list yet; save one in Settings › Subagents, then sync again")
	}
	obj, err := parseJSONObject(embeddedJSON(raw))
	if err != nil {
		return ouroborosRoster{}, fmt.Errorf("%s is not a JSON object", ouroborosSubagentsKey)
	}
	var roster ouroborosRoster
	for _, key := range obj.keys {
		if key != "enabled" && key != "items" {
			return ouroborosRoster{}, fmt.Errorf("%s has an unknown top-level key %q", ouroborosSubagentsKey, key)
		}
	}
	enabled, _ := obj.raw("enabled")
	items, _ := obj.raw("items")
	if json.Unmarshal(enabled, &roster.enabled) != nil || json.Unmarshal(items, &roster.items) != nil || roster.items == nil {
		return ouroborosRoster{}, fmt.Errorf("%s needs a boolean enabled and an items array", ouroborosSubagentsKey)
	}
	for i, item := range roster.items {
		var facts ouroborosRowFacts
		if err := json.Unmarshal(item, &facts); err != nil {
			return ouroborosRoster{}, fmt.Errorf("%s items[%d] is not a subagent row", ouroborosSubagentsKey, i)
		}
		roster.facts = append(roster.facts, facts)
	}
	return roster, nil
}

// ouroborosPlan is the roster sync would leave: foreign rows as they are and
// one tackroom row per engine the roles need.
type ouroborosPlan struct {
	rows      map[string][]byte // tackroom row id -> canonical bytes
	order     []string
	served    []string // "role (row id)" for roles an owner row already runs
	conflicts []string
}

func planOuroborosRoster(roster ouroborosRoster, roles []agentrole.Role, agent agentConfig) (ouroborosPlan, error) {
	plan := ouroborosPlan{rows: map[string][]byte{}}
	foreign := map[string]string{}
	ours := map[string]ouroborosRowFacts{}
	foreignCount := 0
	for _, facts := range roster.facts {
		if strings.HasPrefix(facts.SubagentID, ouroborosRowPrefix) {
			ours[facts.SubagentID] = facts
			continue
		}
		foreignCount++
		// A minted row may share its engine with another row; Ouroboros allows it.
		if facts.MintedFrom == "" {
			foreign[facts.engine()] = facts.SubagentID
		}
	}
	groups := map[string][]agentrole.Role{}
	rowEngine := map[string]ouroborosRow{}
	for _, role := range roles {
		row, problem := ouroborosRoleRow(role, agent)
		if problem != "" {
			plan.conflicts = append(plan.conflicts, problem)
			continue
		}
		facts := ouroborosRowFacts{Route: row.Route, Effort: row.Effort, Access: row.Access}
		if id, ok := foreign[facts.engine()]; ok {
			plan.served = append(plan.served, fmt.Sprintf("%s (row %s)", role.Name, id))
			continue
		}
		if other, ok := rowEngine[row.SubagentID]; ok && (other.Route != row.Route || other.Effort != row.Effort || other.Access != row.Access) {
			plan.conflicts = append(plan.conflicts, fmt.Sprintf("roles %s and %s run different engines that both map to Ouroboros row id %s; set ouroboros.route on one of them", groups[row.SubagentID][0].Name, role.Name, row.SubagentID))
			continue
		}
		if _, ok := groups[row.SubagentID]; !ok {
			plan.order = append(plan.order, row.SubagentID)
			rowEngine[row.SubagentID] = row
		}
		groups[row.SubagentID] = append(groups[row.SubagentID], role)
	}
	if foreignCount+len(plan.order) > ouroborosMaxRows {
		return ouroborosPlan{}, fmt.Errorf("%s would hold %d rows; Ouroboros allows %d", ouroborosSubagentsKey, foreignCount+len(plan.order), ouroborosMaxRows)
	}
	for _, id := range plan.order {
		row := rowEngine[id]
		uses := make([]string, 0, len(groups[id]))
		for _, role := range groups[id] {
			uses = append(uses, fmt.Sprintf("%s: %s", role.Name, strings.TrimSpace(role.Description)))
		}
		row.RecommendedUse = strings.Join(uses, "\n")
		// The owner's switch and review mark on a tackroom row survive sync.
		if prior, ok := ours[id]; ok {
			if prior.Enabled != nil && !*prior.Enabled {
				row.Enabled = prior.Enabled
			}
			row.ReviewEligible = prior.ReviewEligible
		}
		data, err := marshalJSONNoEscape(row)
		if err != nil {
			return ouroborosPlan{}, err
		}
		plan.rows[id] = data
	}
	return plan, nil
}

// ouroborosRoleRow maps a role to its engine row (recommended_use is filled
// in by the caller), or explains why the role cannot run in Ouroboros.
func ouroborosRoleRow(role agentrole.Role, agent agentConfig) (ouroborosRow, string) {
	target := strings.TrimSpace(role.Ouroboros.Route)
	if target == "" {
		model := strings.TrimSpace(role.Model)
		if model == "" {
			model = strings.TrimSpace(agent.RoleModel)
		}
		switch lower := strings.ToLower(model); {
		case model == "":
			target = "claude"
		case lower == "haiku" || lower == "sonnet" || lower == "opus":
			target = "claude=" + lower
		case strings.Contains(model, "="):
			target = model
		default:
			return ouroborosRow{}, fmt.Sprintf("role %s sets model %q, which names no Ouroboros harness; add `ouroboros: {route: harness=model}` to agents/%s.md", role.Name, model, role.Name)
		}
	}
	harnessName, model, hasModel := strings.Cut(target, "=")
	if strings.ContainsAny(target, " \t\r\n:") || strings.Count(target, "=") > 1 || !ouroborosIDPattern.MatchString(harnessName) || hasModel && model == "" {
		return ouroborosRow{}, fmt.Sprintf("role %s: Ouroboros route %q must be harness[=model] with no spaces or ':'", role.Name, target)
	}
	effort := strings.ToLower(strings.TrimSpace(role.Effort))
	// Cursor and Agy model slugs carry their own tier; an unknown level (auto) is not storable.
	if !stringInSlice(effort, ouroborosEfforts) || harnessName == "cursor" || harnessName == "agy" {
		effort = ""
	}
	row := ouroborosRow{
		Route:  ouroborosRoute{Kind: "agent_session", TargetID: target},
		Effort: effort,
		Access: ouroborosAccess(role.Tools),
	}
	id := ouroborosRowPrefix + strings.Trim(ouroborosIDUnsafe.ReplaceAllString(target, "-"), "-")
	for _, facet := range []string{effort, strings.TrimPrefix(row.Access, "full")} {
		if facet != "" {
			id += "." + facet
		}
	}
	if !ouroborosIDPattern.MatchString(id) {
		return ouroborosRow{}, fmt.Sprintf("role %s: Ouroboros row id %q is too long; shorten its route", role.Name, id)
	}
	row.SubagentID = id
	return row, ""
}

// ouroborosAccess caps a session row to the role's tools: unset tools are
// every tool (full); a shell or file edits are writes inside the workspace
// sandbox; anything else reads only.
func ouroborosAccess(tools []string) string {
	if tools == nil {
		return "full"
	}
	for _, tool := range tools {
		switch strings.ToLower(strings.TrimSpace(tool)) {
		case "bash", "shell", "write", "edit", "multiedit", "notebookedit":
			return "workspace_write"
		}
	}
	return "readonly"
}

func inspectOuroborosRoles(report *agentReport, doc *jsonObject, repoRoot string, agent agentConfig) error {
	roles, err := agentrole.Load(repoRoot)
	if err != nil {
		return err
	}
	roster, err := readOuroborosRoster(doc)
	if err != nil && len(roles) == 0 {
		return nil
	}
	if err == nil {
		var plan ouroborosPlan
		if plan, err = planOuroborosRoster(roster, roles, agent); err == nil {
			diffOuroborosRoster(report, roster, plan)
			return nil
		}
	}
	addSoftConflict(report, fmt.Sprintf("roles were not written to Ouroboros: %v", err))
	return nil
}

func diffOuroborosRoster(report *agentReport, roster ouroborosRoster, plan ouroborosPlan) {
	for _, msg := range plan.conflicts {
		addSoftConflict(report, msg)
	}
	report.ManagedAgent = append(report.ManagedAgent, plan.served...)
	current := map[string][]byte{}
	for i, facts := range roster.facts {
		if !strings.HasPrefix(facts.SubagentID, ouroborosRowPrefix) {
			continue
		}
		var compact bytes.Buffer
		_ = json.Compact(&compact, roster.items[i])
		current[facts.SubagentID] = compact.Bytes()
		if _, ok := plan.rows[facts.SubagentID]; !ok {
			report.RemovesAgent = append(report.RemovesAgent, facts.SubagentID)
		}
	}
	for _, id := range plan.order {
		switch have, ok := current[id]; {
		case !ok:
			report.MissingAgent = append(report.MissingAgent, id)
			report.AddsAgent = append(report.AddsAgent, id)
		case !bytes.Equal(have, plan.rows[id]):
			report.DriftedAgent = append(report.DriftedAgent, id)
			report.UpdatesAgent = append(report.UpdatesAgent, id)
		default:
			report.ManagedAgent = append(report.ManagedAgent, id)
		}
	}
}

// applyOuroborosRoles rewrites the tackroom rows of OUROBOROS_SUBAGENTS in
// place, appends new ones and drops the stale rows the report still removes.
// Foreign rows, their order and the list-level switch are left as they are.
func applyOuroborosRoles(report agentReport, repoRoot, home string, agent agentConfig) error {
	if len(report.AddsAgent)+len(report.UpdatesAgent)+len(report.RemovesAgent) == 0 {
		return nil
	}
	return updateOuroborosSettings(home, func(doc *jsonObject) error {
		roster, err := readOuroborosRoster(doc)
		if err != nil {
			return err
		}
		roles, err := agentrole.Load(repoRoot)
		if err != nil {
			return err
		}
		plan, err := planOuroborosRoster(roster, roles, agent)
		if err != nil {
			return err
		}
		items := make([]json.RawMessage, 0, len(roster.items)+len(plan.order))
		written := map[string]bool{}
		for i, facts := range roster.facts {
			id := facts.SubagentID
			switch row, wanted := plan.rows[id]; {
			case !strings.HasPrefix(id, ouroborosRowPrefix):
				items = append(items, roster.items[i])
			case wanted && !written[id]:
				items, written[id] = append(items, row), true
			case !wanted && !stringInSlice(id, report.RemovesAgent):
				items = append(items, roster.items[i]) // removal declined
			}
		}
		for _, id := range plan.order {
			if !written[id] {
				items = append(items, plan.rows[id])
			}
		}
		value, err := marshalJSONNoEscape(struct {
			Enabled bool              `json:"enabled"`
			Items   []json.RawMessage `json:"items"`
		}{roster.enabled, items})
		if err != nil {
			return err
		}
		return doc.set(ouroborosSubagentsKey, string(value))
	})
}

// --- MCP: rows of MCP_SERVERS keyed by Ouroboros's canonical server id ---

func ouroborosMCPID(name string) (string, error) {
	id := strings.ToLower(strings.Trim(ouroborosMCPIDUnsafe.ReplaceAllString(strings.TrimSpace(name), "_"), "_"))
	if id == "" || len(id) > ouroborosMaxMCPID {
		return "", fmt.Errorf("ouroboros MCP server id for %q must be 1-%d letters, digits or _", name, ouroborosMaxMCPID)
	}
	return id, nil
}

// ouroborosMCPRows returns MCP_SERVERS (a list, or a string holding one) and
// the index of the row that claims id, or -1.
func ouroborosMCPRows(doc *jsonObject, id string) ([]json.RawMessage, int, *jsonObject, error) {
	var rows []json.RawMessage
	if raw, ok := doc.raw(ouroborosMCPKey); ok && json.Unmarshal(embeddedJSON(raw), &rows) != nil {
		return nil, -1, nil, fmt.Errorf("%s in %s is not a list", ouroborosMCPKey, "settings.json")
	}
	for i, raw := range rows {
		row, err := parseJSONObject(raw)
		if err != nil {
			continue
		}
		claim := row.str("id")
		for _, key := range []string{"slug", "name"} {
			if claim == "" {
				claim = row.str(key)
			}
		}
		if rowID, _ := ouroborosMCPID(claim); rowID == id {
			return rows, i, row, nil
		}
	}
	return rows, -1, nil, nil
}

func readOuroborosMCPRow(home, name string) (*jsonObject, error) {
	id, err := ouroborosMCPID(name)
	if err != nil {
		return nil, err
	}
	doc, err := readOuroborosSettings(home)
	if err != nil {
		return nil, err
	}
	_, _, row, err := ouroborosMCPRows(doc, id)
	return row, err
}

func inspectOuroborosMCPServer(_ mcpTarget, server mcpServerConfig, home string) (string, error) {
	row, err := readOuroborosMCPRow(home, server.Name)
	if err != nil || row == nil {
		return stateMissing, err
	}
	var native struct {
		Enabled   bool              `json:"enabled"`
		Transport string            `json:"transport"`
		Command   string            `json:"command"`
		Args      []string          `json:"args"`
		Env       map[string]string `json:"env"`
		Skill     string            `json:"skill"`
	}
	data, _ := row.MarshalJSON()
	if json.Unmarshal(data, &native) != nil || !native.Enabled || native.Transport != "stdio" || native.Skill != "" ||
		native.Command != server.Command || !stringSlicesEqual(native.Args, server.Args) || !maps.Equal(native.Env, server.Env) {
		return stateDrifted, nil
	}
	for _, key := range []string{"url", "auth_token", "headers", "env_from_settings"} {
		if _, ok := row.raw(key); ok {
			return stateDrifted, nil
		}
	}
	return stateSynced, nil
}

// patchOuroborosMCPServer writes one enabled stdio row. Fields tackroom does
// not manage (allowed_tools, cwd, task_session ...) stay; args and env are
// replaced whole. A row bound to an Ouroboros skill is never taken over.
func patchOuroborosMCPServer(_ mcpTarget, server mcpServerConfig, home string) error {
	id, err := ouroborosMCPID(server.Name)
	if err != nil {
		return err
	}
	if envHasReference(server.Env) {
		return fmt.Errorf("MCP server %s: Ouroboros takes literal env values only, not ${VAR}", server.Name)
	}
	return updateOuroborosSettings(home, func(doc *jsonObject) error {
		rows, index, row, err := ouroborosMCPRows(doc, id)
		if err != nil {
			return err
		}
		if row == nil {
			row = &jsonObject{values: map[string]json.RawMessage{}, changed: map[string]bool{}}
			if err := row.set("id", id); err != nil {
				return err
			}
			_ = row.set("name", server.Name)
			rows, index = append(rows, nil), len(rows)
		} else if row.str("skill") != "" {
			return fmt.Errorf("MCP server %s: Ouroboros row %s belongs to skill %s; rename the server in tackroom", server.Name, id, row.str("skill"))
		}
		args := server.Args
		if args == nil {
			args = []string{}
		}
		for _, field := range []struct {
			key   string
			value any
		}{{"enabled", true}, {"transport", "stdio"}, {"command", server.Command}, {"args", args}} {
			if err := row.set(field.key, field.value); err != nil {
				return err
			}
		}
		for _, key := range []string{"url", "auth_token", "headers", "env_from_settings"} {
			row.delete(key)
		}
		if len(server.Env) == 0 {
			row.delete("env")
		} else if err := row.set("env", server.Env); err != nil {
			return err
		}
		data, err := row.MarshalJSON()
		if err != nil {
			return err
		}
		rows[index] = data
		return doc.set(ouroborosMCPKey, rows)
	})
}

func removeOuroborosMCPServer(_ mcpTarget, name string, home string) error {
	id, err := ouroborosMCPID(name)
	if err != nil {
		return err
	}
	return updateOuroborosSettings(home, func(doc *jsonObject) error {
		rows, index, row, err := ouroborosMCPRows(doc, id)
		if err != nil || row == nil || row.str("skill") != "" {
			return err
		}
		return doc.set(ouroborosMCPKey, append(rows[:index:index], rows[index+1:]...))
	})
}

func readOuroborosMCPServer(_ mcpTarget, name string, home string) (mcpServerConfig, error) {
	row, err := readOuroborosMCPRow(home, name)
	if err != nil {
		return mcpServerConfig{}, err
	}
	if row == nil || row.str("transport") != "stdio" || row.str("command") == "" {
		return mcpServerConfig{}, fmt.Errorf("MCP server %q is not a stdio server in Ouroboros", name)
	}
	server := mcpServerConfig{Name: name, Enabled: true, Command: row.str("command")}
	if raw, ok := row.raw("args"); ok {
		_ = json.Unmarshal(raw, &server.Args)
	}
	if raw, ok := row.raw("env"); ok {
		_ = json.Unmarshal(raw, &server.Env)
	}
	return server, nil
}

func envHasReference(env map[string]string) bool {
	for _, value := range env {
		if hasEnvReference(value) {
			return true
		}
	}
	return false
}

// --- owner-only surfaces: written only by `tackroom sync --ouroboros-rules --yes` ---

// runOuroborosRules previews, and with --yes writes, the two settings every
// Safety check and skill install reads: the owner rules (from the config
// root's AGENTS.md) and trust in My folder when it is the config root.
func runOuroborosRules(opts runOptions) error {
	repoRoot, home, _, _, err := loadContext(opts)
	if err != nil {
		return err
	}
	out := setupStreams(opts).out
	rulesPath := filepath.Join(repoRoot, "AGENTS.md")
	rules, err := os.ReadFile(rulesPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", rulesPath, err)
	}
	if len(rules) > ouroborosMaxRulesBytes {
		return fmt.Errorf("%s is %d bytes; Ouroboros owner rules hold at most %d", rulesPath, len(rules), ouroborosMaxRulesBytes)
	}
	doc, err := readOuroborosSettings(home)
	if err != nil {
		return err
	}
	pending := 0
	if before := doc.str(ouroborosRulesKey); before != string(rules) {
		pending++
		fmt.Fprintf(out, "%s (Ouroboros Settings › Safety › Your rules) from %s:\n%s\n", ouroborosRulesKey, displayPath(rulesPath, home),
			unifiedConfigDiff(ouroborosRulesKey, []byte(before), rules))
	}
	trust, note := ouroborosTrustFolder(doc, repoRoot, home)
	if note != "" {
		fmt.Fprintln(out, note)
	}
	if trust != nil {
		pending++
		fmt.Fprintf(out, "%s: source %s (%s) trust review -> trusted: its skills install without review\n", ouroborosSourcesKey, ouroborosLocalSourceID, displayPath(repoRoot, home))
	}
	switch {
	case pending == 0:
		fmt.Fprintln(out, "Ouroboros owner rules and My folder trust are up to date")
		return nil
	case opts.DryRun || !opts.AssumeYes:
		fmt.Fprintln(out, "nothing written; rerun `tackroom sync --ouroboros-rules --yes` to write these changes")
		return nil
	}
	err = updateOuroborosSettings(home, func(doc *jsonObject) error {
		if err := doc.set(ouroborosRulesKey, string(rules)); err != nil {
			return err
		}
		if trust, _ := ouroborosTrustFolder(doc, repoRoot, home); trust != nil {
			return doc.set(ouroborosSourcesKey, string(trust))
		}
		return nil
	})
	if err == nil {
		fmt.Fprintf(out, "wrote %d Ouroboros owner setting(s) to %s\n", pending, displayPath(ouroborosSettingsPath(home), home))
	}
	return err
}

// ouroborosTrustFolder returns the skill source list with My folder trusted,
// nil when nothing would change, and a note when trust is not offered. My
// folder follows OUROBOROS_SKILLS_REPO_PATH, so its row is written with that
// path (added if the saved list predates the folder), as Ouroboros saves it.
func ouroborosTrustFolder(doc *jsonObject, repoRoot, home string) ([]byte, string) {
	folder, reads := ouroborosFolderState(doc, repoRoot, home)
	if !reads {
		return nil, fmt.Sprintf("My folder trust: My folder is %q, not the config root; left alone", folder)
	}
	raw, ok := doc.raw(ouroborosSourcesKey)
	if !ok {
		return nil, "My folder trust: Ouroboros has not saved its skill sources yet; open Settings › Skills once"
	}
	var rows []json.RawMessage
	if json.Unmarshal(embeddedJSON(raw), &rows) != nil {
		return nil, fmt.Sprintf("My folder trust: %s is not a list; left alone", ouroborosSourcesKey)
	}
	path := expandPath(folder, home)
	index := len(rows)
	row, _ := parseJSONObject([]byte(`{"id":"my-folder","name":"My folder","kind":"local"}`))
	for i, data := range rows {
		if existing, err := parseJSONObject(data); err == nil && existing.str("id") == ouroborosLocalSourceID {
			index, row = i, existing
			break
		}
	}
	if index < len(rows) && row.str("trust") == "trusted" && row.str("path") == path {
		return nil, ""
	}
	_ = row.set("path", path)
	_ = row.set("trust", "trusted")
	_ = row.set("confirmed", true)
	data, err := row.MarshalJSON()
	if err != nil {
		return nil, err.Error()
	}
	if index == len(rows) {
		rows = append(rows, nil)
	}
	rows[index] = data
	value, _ := marshalJSONNoEscape(rows)
	return value, ""
}

// checkOuroborosSettings reports the state sync cannot fix by itself.
func checkOuroborosSettings(repoRoot, home string, cfg config) checkResult {
	const name = "ouroboros settings"
	if !isAgentDetected(cfg, agentOuroboros) {
		return checkResult{name, checkStatusPass, agentOuroboros + " not detected, skipped"}
	}
	doc, err := readOuroborosSettings(home)
	if err != nil {
		return checkResult{name, checkStatusFail, err.Error()}
	}
	var notes []string
	if raw, ok := doc.raw("MCP_ENABLED"); ok && string(raw) == "false" {
		notes = append(notes, "MCP is switched off in Ouroboros (MCP_ENABLED), so synced servers stay unused")
	}
	var skipped []string
	for _, server := range cfg.MCPServers {
		if server.Enabled && server.URL == "" && envHasReference(server.Env) && (len(server.Agents) == 0 || stringInSlice(agentOuroboros, server.Agents)) {
			skipped = append(skipped, server.Name)
		}
	}
	if len(skipped) > 0 {
		sort.Strings(skipped)
		notes = append(notes, "MCP servers with ${VAR} env are not synced to Ouroboros: "+strings.Join(skipped, ", "))
	}
	if _, err := readOuroborosRoster(doc); err != nil {
		notes = append(notes, err.Error())
	}
	if len(notes) > 0 {
		return checkResult{name, checkStatusWarn, strings.Join(notes, "; ")}
	}
	rules := "not set"
	if doc.str(ouroborosRulesKey) != "" {
		rules = "set"
	}
	return checkResult{name, checkStatusPass, "owner rules " + rules + "; tackroom writes them only with `tackroom sync --ouroboros-rules --yes`"}
}
