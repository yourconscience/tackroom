package app

import (
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Separate assets keep the Go server small and make the browser surface easy to
// review without introducing a JavaScript build or runtime dependency.
//
//go:embed web/index.html web/style.css web/app.js
var configWebAssets embed.FS

type configWebServer struct {
	doc          *configDocument
	secureCookie bool
	origin       string
	token        string
	csrf         string
}

var configSyncMu sync.Mutex

func runConfigServe(opts configServeOptions) error {
	doc, _, err := openConfigDocument(configCommandOptions{ConfigPath: opts.ConfigPath})
	if err != nil {
		return err
	}
	if err := validateLoopbackAddr(opts.Addr); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", opts.Addr, err)
	}
	defer listener.Close()
	token, err := resolveServerToken(opts.TokenFile)
	if err != nil {
		return err
	}
	csrf, err := randomToken(24)
	if err != nil {
		return err
	}
	origin := "http://" + listener.Addr().String()
	if host, _, splitErr := net.SplitHostPort(listener.Addr().String()); splitErr == nil {
		origin = "http://" + net.JoinHostPort(host, portString(listener.Addr()))
	}
	server := &configWebServer{doc: doc, secureCookie: opts.SecureCookie, origin: origin, token: token, csrf: csrf}
	startURL := origin + "/?token=" + url.QueryEscape(token)
	remote := os.Getenv("SSH_CONNECTION") != ""
	sshHost := resolveSSHHost(opts.SSHHost, os.Getenv)
	announceView(os.Stdout, startURL, opts, remote, sshHost)
	httpServer := &http.Server{Handler: server.handler(), ReadHeaderTimeout: 5 * time.Second}
	return httpServer.Serve(listener)
}

func portString(addr net.Addr) string {
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return ""
	}
	return port
}

func validateLoopbackAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("view requires an explicit loopback address, got %q", addr)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("view refuses non-loopback address %q", addr)
	}
	return nil
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// resolveServerToken returns a stable session token from tokenFile when set,
// minting and persisting one (0600) on first use so a restarted persistent
// `view` service keeps the same access URL. Without a token file it falls back
// to a fresh per-process token.
func resolveServerToken(tokenFile string) (string, error) {
	if tokenFile == "" {
		return randomToken(32)
	}
	switch data, err := os.ReadFile(tokenFile); {
	case err == nil:
		if tok := strings.TrimSpace(string(data)); tok != "" {
			return tok, nil
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("read token file %s: %w", tokenFile, err)
	}
	tok, err := randomToken(32)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(tokenFile, []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write token file %s: %w", tokenFile, err)
	}
	return tok, nil
}

func (s *configWebServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.securityHeaders(w)
		switch r.URL.Path {
		case "/":
			s.handleIndex(w, r)
		case "/style.css", "/app.js":
			s.handleAsset(w, r)
		case "/api/state":
			s.handleState(w, r)
		case "/api/config/validate":
			s.handleValidate(w, r)
		case "/api/config/raw":
			s.handleRaw(w, r)
		case "/api/config":
			s.handlePatch(w, r)
		case "/api/sync/preview":
			s.handleSyncPreview(w, r)
		case "/api/sync/apply":
			s.handleSyncApply(w, r)
		case "/api/status":
			s.handleStatus(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func (s *configWebServer) securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (s *configWebServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if token := r.URL.Query().Get("token"); token != "" {
		if token != s.token {
			writeAPIError(w, http.StatusUnauthorized, "invalid_config", "invalid startup token")
			return
		}
		s.setSessionCookies(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.authenticated(r) {
		writeAPIError(w, http.StatusUnauthorized, "invalid_config", "open the tokenized startup URL")
		return
	}
	data, err := configWebAssets.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "asset unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *configWebServer) handleAsset(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		writeAPIError(w, http.StatusUnauthorized, "invalid_config", "session required")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	data, err := configWebAssets.ReadFile("web/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if name == "style.css" {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	_, _ = w.Write(data)
}

func (s *configWebServer) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie("tackroom_session")
	return err == nil && cookie.Value == s.token
}

func (s *configWebServer) setSessionCookies(w http.ResponseWriter) {
	secure := s.secureCookie
	http.SetCookie(w, &http.Cookie{Name: "tackroom_session", Value: s.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure})
	http.SetCookie(w, &http.Cookie{Name: "tackroom_csrf", Value: s.csrf, Path: "/", HttpOnly: false, SameSite: http.SameSiteStrictMode, Secure: secure})
}

func (s *configWebServer) authorizeAPI(w http.ResponseWriter, r *http.Request, mutation bool) bool {
	if !s.authenticated(r) {
		writeAPIError(w, http.StatusUnauthorized, "invalid_config", "session required")
		return false
	}
	hasOriginEvidence := r.Header.Get("Origin") != "" || r.Header.Get("Referer") != ""
	if (mutation || hasOriginEvidence) && !s.requestOriginAllowed(r) {
		writeAPIError(w, http.StatusForbidden, "invalid_config", "origin is not allowed")
		return false
	}
	if mutation {
		csrf, err := r.Cookie("tackroom_csrf")
		if err != nil || csrf.Value == "" || r.Header.Get("X-Tackroom-CSRF") != csrf.Value || csrf.Value != s.csrf {
			writeAPIError(w, http.StatusForbidden, "invalid_config", "CSRF header is required")
			return false
		}
	}
	return true
}

func (s *configWebServer) requestOriginAllowed(r *http.Request) bool {
	raw := r.Header.Get("Origin")
	allowPath := false
	if raw == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		raw = r.Header.Get("Referer")
		allowPath = true
	}
	origin, err := url.Parse(raw)
	if err != nil || origin.User != nil || origin.Host == "" || (!allowPath && origin.Path != "") {
		return false
	}
	scheme := "http"
	if s.secureCookie {
		scheme = "https"
	}
	return origin.Scheme == scheme && origin.Host == r.Host
}

func (s *configWebServer) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !s.authorizeAPI(w, r, false) {
		return
	}
	if err := s.doc.reload(); err != nil {
		writeCandidateError(w, err)
		return
	}
	layer := configLayer(r.URL.Query().Get("layer"))
	if layer == "" {
		layer = configLayerShared
	}
	if layer != configLayerShared && layer != configLayerLocal && layer != configLayerEffective {
		writeAPIError(w, http.StatusBadRequest, "invalid_config", "layer must be shared, local, or effective")
		return
	}
	s.doc.mu.Lock()
	defer s.doc.mu.Unlock()
	cfg := maskConfigSecrets(s.doc.typedLocked(layer))
	response := map[string]interface{}{
		"paths":        map[string]string{"shared": s.doc.sharedPath, "local": s.doc.localPath},
		"active_layer": layer,
		"typed_config": cfg,
		"effective_ui": s.doc.effective.UI,
		"raw_yaml":     string(s.doc.bytesLocked(layer)),
		"revision":     s.doc.revisionLocked(layer),
		"read_only":    layer == configLayerEffective,
	}
	writeJSON(w, http.StatusOK, response)
}

type configCandidateRequest struct {
	Layer            string            `json:"layer"`
	ExpectedRevision string            `json:"expected_revision,omitempty"`
	RawYAML          string            `json:"raw_yaml,omitempty"`
	Operations       []configOperation `json:"operations,omitempty"`
}

func (s *configWebServer) decodeCandidate(r *http.Request) (configCandidateRequest, []byte, error) {
	var req configCandidateRequest
	if err := decodeJSONBody(r, &req); err != nil {
		return req, nil, err
	}
	layer := configLayer(req.Layer)
	if layer == "" {
		layer = configLayerShared
	}
	if layer == configLayerEffective {
		return req, nil, errReadOnlyLayer
	}
	req.Layer = string(layer)
	if req.RawYAML != "" {
		return req, []byte(req.RawYAML), nil
	}
	raw, err := s.doc.operationsRaw(layer, req.Operations)
	return req, raw, err
}

func (s *configWebServer) handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorizeAPI(w, r, true) {
		return
	}
	req, raw, err := s.decodeCandidate(r)
	if err != nil {
		writeCandidateError(w, err)
		return
	}
	if _, err := s.doc.validateRaw(configLayer(req.Layer), raw); err != nil {
		writeCandidateError(w, err)
		return
	}
	before := s.doc.bytes(configLayer(req.Layer))
	writeJSON(w, http.StatusOK, map[string]interface{}{"valid": true, "diff": unifiedConfigDiff(s.doc.path(configLayer(req.Layer)), before, raw), "raw_yaml": string(raw), "revision": s.doc.revision(configLayer(req.Layer))})
}

func (s *configWebServer) handleRaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut || !s.authorizeAPI(w, r, true) {
		return
	}
	req, raw, err := s.decodeCandidate(r)
	if err != nil {
		writeCandidateError(w, err)
		return
	}
	saved, err := s.doc.saveRaw(configLayer(req.Layer), req.ExpectedRevision, raw)
	if err != nil {
		writeCandidateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saveResponse(saved))
}

func (s *configWebServer) handlePatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch || !s.authorizeAPI(w, r, true) {
		return
	}
	var req configCandidateRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeCandidateError(w, err)
		return
	}
	layer := configLayer(req.Layer)
	if layer == "" {
		layer = configLayerShared
	}
	raw, err := s.doc.operationsRaw(layer, req.Operations)
	if err != nil {
		writeCandidateError(w, err)
		return
	}
	saved, err := s.doc.saveRaw(layer, req.ExpectedRevision, raw)
	if err != nil {
		writeCandidateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saveResponse(saved))
}

func saveResponse(saved configSave) map[string]interface{} {
	return map[string]interface{}{"layer": saved.Layer, "revision": saved.Revision, "diff": saved.Diff, "raw_yaml": string(redactYAMLSecrets(saved.After))}
}

func writeCandidateError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "invalid_config"
	if errors.Is(err, errStaleRevision) {
		status = http.StatusConflict
		code = "stale_revision"
	}
	if errors.Is(err, errReadOnlyLayer) {
		status = http.StatusBadRequest
		code = "invalid_config"
	}
	writeAPIError(w, status, code, err.Error())
}

func decodeJSONBody(r *http.Request, dst interface{}) error {
	limited := io.LimitReader(r.Body, 2<<20)
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]interface{}{"error": map[string]string{"code": code, "message": message}})
}

type syncPlan struct {
	RepoRoot    string         `json:"repo_root"`
	Repo        repoLinkReport `json:"repo"`
	Reports     []agentReport  `json:"reports"`
	Destructive []string       `json:"destructive"`
	Summary     []string       `json:"summary"`
	Digest      string         `json:"digest"`
}

// summarizeSyncPlan renders one line per agent for the view UI: what sync
// will change, or what blocks it, so "up to date" only appears when it is.
func summarizeSyncPlan(reports []agentReport) []string {
	var lines []string
	for _, report := range reports {
		if !report.Detected {
			lines = append(lines, report.Name+": not installed, skipped")
			continue
		}
		var parts []string
		for _, group := range []struct {
			label string
			items []string
		}{
			{"add skill", report.Adds}, {"add role", report.AddsAgent}, {"add MCP", report.AddsMCP}, {"add hook", report.AddsHook},
			{"update skill", report.Updates}, {"overwrite role", report.UpdatesAgent}, {"update MCP", report.UpdatesMCP},
			{"update hook", report.UpdatesHook}, {"update package", report.UpdatesPackage},
			{"remove skill", report.Removes}, {"remove role", report.RemovesAgent}, {"remove package", report.RemovesPackage},
		} {
			for _, item := range group.items {
				parts = append(parts, group.label+" "+item)
			}
		}
		for _, item := range report.Conflicts {
			parts = append(parts, "conflict "+item+" (not managed by tackroom)")
		}
		if report.RootState != "" && report.RootState != stateSynced {
			parts = append(parts, "root instructions "+report.RootState)
		}
		switch {
		case len(parts) > 0:
			lines = append(lines, report.Name+": "+strings.Join(parts, ", "))
		case report.Synced:
			lines = append(lines, report.Name+": up to date")
		default:
			lines = append(lines, report.Name+": needs sync")
		}
	}
	return lines
}

func buildConfigSyncPlan(doc *configDocument) (syncPlan, config, string, error) {
	snapshot, err := doc.syncSnapshot()
	if err != nil {
		return syncPlan{}, config{}, "", err
	}
	cfg := snapshot.cfg
	selected, err := selectAgents(cfg, "")
	if err != nil {
		return syncPlan{}, config{}, "", err
	}
	repo, err := inspectRepoLink(snapshot.repoRoot, snapshot.home)
	if err != nil {
		return syncPlan{}, config{}, "", err
	}
	expected, err := expectedSkills(snapshot.repoRoot, snapshot.home, cfg)
	if err != nil {
		return syncPlan{}, config{}, "", err
	}
	reports, err := inspectAgents(selected, expected, snapshot.repoRoot, snapshot.home, cfg)
	if err != nil {
		return syncPlan{}, config{}, "", err
	}
	plan := syncPlan{RepoRoot: snapshot.repoRoot, Repo: repo, Reports: reports, Summary: summarizeSyncPlan(reports)}
	for _, report := range reports {
		for _, item := range report.Removes {
			plan.Destructive = append(plan.Destructive, report.Name+": remove "+item)
		}
		for _, item := range report.RemovesAgent {
			plan.Destructive = append(plan.Destructive, report.Name+": remove role "+item)
		}
		for _, item := range report.UpdatesAgent {
			plan.Destructive = append(plan.Destructive, report.Name+": overwrite role "+item)
		}
	}
	planData, _ := json.Marshal(struct {
		Repo    repoLinkReport
		Reports []agentReport
	}{plan.Repo, plan.Reports})
	digest := sha256.Sum256(planData)
	plan.Digest = hex.EncodeToString(digest[:])
	return plan, cfg, snapshot.revision, nil
}

func (s *configWebServer) handleSyncPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorizeAPI(w, r, true) {
		return
	}
	if err := s.doc.reload(); err != nil {
		writeCandidateError(w, err)
		return
	}
	plan, _, revision, err := buildConfigSyncPlan(s.doc)
	if err != nil {
		writeCandidateError(w, err)
		return
	}
	planData, _ := json.Marshal(plan)
	writeJSON(w, http.StatusOK, map[string]interface{}{"plan": json.RawMessage(planData), "revision": revision, "digest": plan.Digest})
}

type syncApplyRequest struct {
	ExpectedRevision string   `json:"expected_revision"`
	PlanDigest       string   `json:"plan_digest"`
	Confirmed        []string `json:"confirmed_destructive,omitempty"`
}

func (s *configWebServer) handleSyncApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorizeAPI(w, r, true) {
		return
	}
	var req syncApplyRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeCandidateError(w, err)
		return
	}
	configSyncMu.Lock()
	defer configSyncMu.Unlock()
	if err := s.doc.reload(); err != nil {
		writeCandidateError(w, err)
		return
	}
	plan, cfg, revision, err := buildConfigSyncPlan(s.doc)
	if err != nil {
		writeCandidateError(w, err)
		return
	}
	if revision != req.ExpectedRevision {
		writeAPIError(w, http.StatusConflict, "stale_revision", "canonical config changed; preview again")
		return
	}
	if plan.Digest != req.PlanDigest {
		writeAPIError(w, http.StatusConflict, "sync_plan_changed", "sync plan changed; preview again")
		return
	}
	if !sameStrings(plan.Destructive, req.Confirmed) {
		writeAPIError(w, http.StatusConflict, "invalid_config", "confirm every destructive sync item before applying")
		return
	}
	if err := runSync(runOptions{ConfigPath: s.doc.sharedPath, ConfigOverride: &cfg, Stdout: io.Discard, Stdin: strings.NewReader("n\n")}); err != nil {
		writeCandidateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"applied": true, "revision": revision})
}

func (s *configWebServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !s.authorizeAPI(w, r, false) {
		return
	}
	if err := s.doc.reload(); err != nil {
		writeCandidateError(w, err)
		return
	}
	plan, _, revision, err := buildConfigSyncPlan(s.doc)
	if err != nil {
		writeCandidateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"repo": plan.Repo, "reports": plan.Reports, "revision": revision})
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]struct{}, len(right))
	for _, item := range right {
		seen[item] = struct{}{}
	}
	for _, item := range left {
		if _, ok := seen[item]; !ok {
			return false
		}
	}
	return true
}

func maskConfigSecrets(cfg config) config {
	copyCfg := cfg
	copyCfg.MCPServers = append([]mcpServerConfig(nil), cfg.MCPServers...)
	for i := range copyCfg.MCPServers {
		if cfg.MCPServers[i].Env == nil {
			continue
		}
		copyCfg.MCPServers[i].Env = make(map[string]string, len(cfg.MCPServers[i].Env))
		for key := range cfg.MCPServers[i].Env {
			copyCfg.MCPServers[i].Env[key] = "***"
		}
	}
	return copyCfg
}

func redactYAMLSecrets(data []byte) []byte {
	var node yaml.Node
	if yamlErr := yaml.Unmarshal(data, &node); yamlErr != nil || !containsYAMLEnv(rootMapping(&node)) {
		return data
	}
	redactYAMLNode(rootMapping(&node), false)
	out, err := yaml.Marshal(&node)
	if err != nil {
		return data
	}
	return out
}

func containsYAMLEnv(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "env" && node.Content[i+1].Kind == yaml.MappingNode {
				return true
			}
			if containsYAMLEnv(node.Content[i+1]) {
				return true
			}
		}
		return false
	}
	for _, child := range node.Content {
		if containsYAMLEnv(child) {
			return true
		}
	}
	return false
}

func redactYAMLNode(node *yaml.Node, inEnv bool) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			value := node.Content[i+1]
			if key == "env" && value.Kind == yaml.MappingNode {
				for j := 1; j < len(value.Content); j += 2 {
					value.Content[j].Value = "***"
					value.Content[j].Tag = "!!str"
				}
				continue
			}
			redactYAMLNode(value, inEnv || key == "env")
		}
		return
	}
	for _, child := range node.Content {
		redactYAMLNode(child, inEnv)
	}
}
