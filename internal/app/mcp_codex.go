package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

func patchCodexMCPServer(target mcpTarget, server mcpServerConfig, home string) error {
	configPath := target.configPath(home)
	data, err := os.ReadFile(configPath)
	content := ""
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", configPath, err)
		}
	} else {
		content = string(data)
		entry, _, err := decodeCodexMCPEntry(configPath, data, server.Name)
		if err != nil && !errors.Is(err, errCodexEntryRepairable) {
			return err
		}
		server.Env = mergeManagedEnv(tomlEntryEnv(entry), server.Env)
		if server.URL != "" {
			server = keepNativeRemoteSecrets(server, entry, target.remote.codexHeaders)
		}
	}
	header := fmt.Sprintf("[mcp_servers.%s]", server.Name)
	section := renderCodexMCPSection(server)
	if server.URL != "" {
		if section, err = renderRemoteTOMLSection(server, target.remote.codexHeaders); err != nil {
			return err
		}
	}
	updated := upsertTOMLSectionIncludingDescendants(content, header, keepNativeCodexKeys(content, server.Name, section))
	if updated == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(configPath), err)
	}
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}

var codexManagedKeys = map[string]struct{}{
	"command": {}, "args": {}, "env": {},
	"url": {}, "bearer_token_env_var": {}, "env_http_headers": {}, "http_headers": {}, "headers": {},
}

// keepNativeCodexKeys carries over Codex keys tackroom does not manage (env_vars, startup_timeout_sec, tools.*) so a rewrite cannot drop them.
func keepNativeCodexKeys(content string, name string, section string) string {
	entry, ok, err := decodeCodexMCPEntry("", []byte(content), name)
	if !ok || (err != nil && !errors.Is(err, errCodexEntryRepairable)) {
		return section
	}
	extras := map[string]interface{}{}
	for key, value := range entry {
		if _, managed := codexManagedKeys[key]; managed {
			continue
		}
		// The upsert leaves [[...]] descendants in place, so re-emitting them would duplicate them.
		if _, tables := value.([]map[string]interface{}); tables {
			continue
		}
		extras[key] = value
	}
	if len(extras) == 0 {
		return section
	}
	var encoded bytes.Buffer
	enc := toml.NewEncoder(&encoded)
	enc.Indent = ""
	if err := enc.Encode(map[string]interface{}{name: extras}); err != nil {
		return section
	}
	var out strings.Builder
	out.WriteString(strings.TrimRight(section, "\n") + "\n")
	for _, line := range strings.Split(strings.TrimRight(encoded.String(), "\n"), "\n") {
		switch {
		case line == "["+name+"]":
			continue
		case strings.HasPrefix(line, "[["):
			line = "[[mcp_servers." + line[2:]
		case strings.HasPrefix(line, "["):
			line = "[mcp_servers." + line[1:]
		}
		if strings.HasPrefix(line, "[") {
			out.WriteString("\n")
		}
		out.WriteString(line + "\n")
	}
	out.WriteString("\n")
	return out.String()
}
