package app

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// remoteMCP says how a target writes a remote (streamable HTTP) MCP server.
// Targets without one do not get remote servers.
type remoteMCP struct {
	urlKey string                 // "url", or "httpUrl" for Gemini CLI
	fields map[string]interface{} // fixed fields, e.g. Claude Code's "type": "http"
	// envRef renders a ${NAME} reference in a header the way the agent
	// expands it; nil keeps ${NAME}.
	envRef func(name string) string
	// codexHeaders writes Codex's bearer_token_env_var / env_http_headers /
	// http_headers instead of a plain headers table (TOML targets only).
	codexHeaders bool
}

var envReferencePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// hasEnvReference reports whether a header value names an environment
// variable anywhere, as in "Bearer ${TOKEN}".
func hasEnvReference(value string) bool {
	return envReferencePattern.MatchString(value)
}

// headersSatisfied is envSatisfied for headers: a value that references a
// variable is satisfied by any non-empty native value, others must match.
func headersSatisfied(canonical map[string]string, native map[string]string) bool {
	for key, expected := range canonical {
		actual, ok := native[key]
		if !ok {
			return false
		}
		if hasEnvReference(expected) {
			if strings.TrimSpace(actual) == "" {
				return false
			}
			continue
		}
		if actual != expected {
			return false
		}
	}
	return true
}

func remoteHeaderValue(value string, remote *remoteMCP) string {
	if remote == nil || remote.envRef == nil {
		return value
	}
	return envReferencePattern.ReplaceAllStringFunc(value, func(match string) string {
		return remote.envRef(envReferencePattern.FindStringSubmatch(match)[1])
	})
}

func stringMapValue(v interface{}) map[string]string {
	raw, ok := asMap(v)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		if s, ok := value.(string); ok {
			out[key] = s
		}
	}
	return out
}

func matchRemoteMCPMap(entry map[string]interface{}, server mcpServerConfig, remote *remoteMCP) bool {
	if remote == nil {
		return false
	}
	if url, _ := entry[remote.urlKey].(string); url != server.URL {
		return false
	}
	if _, stdio := entry["command"]; stdio {
		return false
	}
	for key, value := range remote.fields {
		if entry[key] != value {
			return false
		}
	}
	return headersSatisfied(server.Headers, stringMapValue(entry["headers"]))
}

func applyRemoteMCPMap(entry map[string]interface{}, server mcpServerConfig, remote *remoteMCP) {
	delete(entry, "command")
	delete(entry, "args")
	delete(entry, "env")
	entry[remote.urlKey] = server.URL
	for key, value := range remote.fields {
		entry[key] = value
	}
	if len(server.Headers) == 0 {
		return
	}
	headers, _ := asMap(entry["headers"])
	if headers == nil {
		headers = map[string]interface{}{}
	}
	for key, value := range server.Headers {
		// Never swap a working header value for a reference; see mergeManagedEnv.
		if existing, ok := headers[key].(string); ok && strings.TrimSpace(existing) != "" && hasEnvReference(value) {
			continue
		}
		headers[key] = remoteHeaderValue(value, remote)
	}
	entry["headers"] = headers
}

// remoteFromMap reads a native remote entry for `mcp import`.
func remoteFromMap(name string, entry map[string]interface{}) (mcpServerConfig, bool) {
	for _, key := range []string{"url", "httpUrl"} {
		if url, _ := entry[key].(string); strings.TrimSpace(url) != "" {
			return mcpServerConfig{Name: name, Enabled: true, URL: url, Headers: stringMapValue(entry["headers"])}, true
		}
	}
	return mcpServerConfig{}, false
}

// Codex keeps header values out of config.toml when they come from the
// environment: bearer_token_env_var for "Authorization: Bearer ${X}",
// env_http_headers for a header that is exactly ${X}, http_headers for
// literals. Grok Build expands ${X} itself and takes plain headers.

type codexRemoteHeaders struct {
	bearerEnv string
	envVars   map[string]string
	literal   map[string]string
}

func splitCodexHeaders(headers map[string]string) (codexRemoteHeaders, error) {
	out := codexRemoteHeaders{envVars: map[string]string{}, literal: map[string]string{}}
	for key, value := range headers {
		match := envReferencePattern.FindStringSubmatchIndex(value)
		switch {
		case match == nil:
			out.literal[key] = value
		case strings.EqualFold(key, "Authorization") && strings.TrimSpace(value[:match[0]]) == "Bearer" && match[1] == len(value):
			out.bearerEnv = value[match[2]:match[3]]
		case match[0] == 0 && match[1] == len(value):
			out.envVars[key] = value[match[2]:match[3]]
		default:
			return out, fmt.Errorf("codex cannot read header %s = %q from the environment; use \"Bearer ${NAME}\" for Authorization or a header that is exactly ${NAME}", key, value)
		}
	}
	return out, nil
}

func renderTOMLStringMap(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%q = %q", key, values[key]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func renderRemoteTOMLSection(server mcpServerConfig, codexHeaders bool) (string, error) {
	lines := []string{fmt.Sprintf("[mcp_servers.%s]", server.Name), fmt.Sprintf("url = %q", server.URL)}
	if !codexHeaders {
		if len(server.Headers) > 0 {
			lines = append(lines, "headers = "+renderTOMLStringMap(server.Headers))
		}
		return strings.Join(lines, "\n") + "\n\n", nil
	}
	split, err := splitCodexHeaders(server.Headers)
	if err != nil {
		return "", err
	}
	if split.bearerEnv != "" {
		lines = append(lines, fmt.Sprintf("bearer_token_env_var = %q", split.bearerEnv))
	}
	if len(split.envVars) > 0 {
		lines = append(lines, "env_http_headers = "+renderTOMLStringMap(split.envVars))
	}
	if len(split.literal) > 0 {
		lines = append(lines, "http_headers = "+renderTOMLStringMap(split.literal))
	}
	return strings.Join(lines, "\n") + "\n\n", nil
}

// matchRemoteTOMLEntry checks a Codex or Grok remote entry. A header that
// references a variable is satisfied by any non-empty native source for it,
// including a real value the user put there.
func matchRemoteTOMLEntry(entry map[string]interface{}, server mcpServerConfig, codexHeaders bool) bool {
	if url, _ := entry["url"].(string); url != server.URL {
		return false
	}
	if _, stdio := entry["command"]; stdio {
		return false
	}
	if !codexHeaders {
		return headersSatisfied(server.Headers, stringMapValue(entry["headers"]))
	}
	literal := stringMapValue(entry["http_headers"])
	envVars := stringMapValue(entry["env_http_headers"])
	bearer, _ := entry["bearer_token_env_var"].(string)
	for key, value := range server.Headers {
		if !hasEnvReference(value) {
			if literal[key] != value {
				return false
			}
			continue
		}
		satisfied := strings.TrimSpace(literal[key]) != "" || strings.TrimSpace(envVars[key]) != "" ||
			(strings.EqualFold(key, "Authorization") && strings.TrimSpace(bearer) != "")
		if !satisfied {
			return false
		}
	}
	return true
}

// keepNativeRemoteSecrets copies real header values the agent already has
// over canonical references, so a sync never replaces them.
func keepNativeRemoteSecrets(server mcpServerConfig, entry map[string]interface{}, codexHeaders bool) mcpServerConfig {
	key := "headers"
	if codexHeaders {
		key = "http_headers"
	}
	native := stringMapValue(entry[key])
	if len(native) == 0 || len(server.Headers) == 0 {
		return server
	}
	headers := make(map[string]string, len(server.Headers))
	for name, value := range server.Headers {
		if existing := native[name]; hasEnvReference(value) && strings.TrimSpace(existing) != "" && !hasEnvReference(existing) {
			value = existing
		}
		headers[name] = value
	}
	server.Headers = headers
	return server
}
