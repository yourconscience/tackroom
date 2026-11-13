package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// kimiHome resolves Kimi Code CLI's data root, honoring $KIMI_CODE_HOME
// (https://github.com/MoonshotAI/kimi-code/blob/main/docs/en/configuration/data-locations.md).
func kimiHome(home string) string {
	if env := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME")); env != "" {
		return env
	}
	return filepath.Join(home, ".kimi-code")
}

func kimiConfigPath(home string) string {
	return filepath.Join(kimiHome(home), "config.toml")
}

// kimiHookEvent maps canonical (Claude Code) events to Kimi Code CLI's hook
// events, which share their names. Notification is left out: Kimi fires it
// for background task changes, not for permission prompts.
func kimiHookEvent(event string) (string, bool) {
	switch event {
	case "SessionStart", "SessionEnd", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "SubagentStop", "PreCompact":
		return event, true
	}
	return "", false
}

type kimiHookRule struct {
	Event   string `toml:"event"`
	Command string `toml:"command"`
	Timeout int64  `toml:"timeout"`
}

type kimiHookDoc struct {
	Hooks []kimiHookRule `toml:"hooks"`
}

func readKimiConfig(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

func kimiHookRules(path string, content string) ([]kimiHookRule, error) {
	var doc kimiHookDoc
	if _, err := toml.Decode(content, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return doc.Hooks, nil
}

func inspectKimiHook(hook hookConfig, home string) (string, error) {
	event, ok := kimiHookEvent(hook.Event)
	if !ok {
		return stateUnsupported, nil
	}
	path := kimiConfigPath(home)
	content, err := readKimiConfig(path)
	if err != nil {
		return stateMissing, err
	}
	rules, err := kimiHookRules(path, content)
	if err != nil {
		return stateMissing, err
	}
	state := stateMissing
	for _, rule := range rules {
		if !hookCommandMatches(rule.Command, hook.Command) {
			continue
		}
		if rule.Event == event && rule.Timeout == int64(hook.Timeout) {
			return stateSynced, nil
		}
		state = stateDrifted
	}
	return state, nil
}

func patchKimiHook(hook hookConfig, home string) error {
	event, ok := kimiHookEvent(hook.Event)
	if !ok {
		return nil
	}
	path := kimiConfigPath(home)
	content, err := readKimiConfig(path)
	if err != nil {
		return err
	}
	if _, err := kimiHookRules(path, content); err != nil {
		return err
	}
	content = removeKimiHookBlocks(content, func(command string) bool { return hookCommandMatches(command, hook.Command) })
	block := fmt.Sprintf("[[hooks]]\nevent = %q\ncommand = %q\n", event, hook.Command)
	if hook.Timeout > 0 {
		block += fmt.Sprintf("timeout = %d\n", hook.Timeout)
	}
	if content != "" && !strings.HasSuffix(content, "\n\n") {
		content = strings.TrimRight(content, "\n") + "\n\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content+block), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// removeKimiHookBlocks drops each [[hooks]] table whose command matches.
func removeKimiHookBlocks(content string, match func(command string) bool) string {
	var out, block strings.Builder
	inBlock := false
	flush := func() {
		if !inBlock {
			return
		}
		var doc kimiHookDoc
		if _, err := toml.Decode(block.String(), &doc); err != nil || len(doc.Hooks) != 1 || !match(doc.Hooks[0].Command) {
			out.WriteString(block.String())
		}
		block.Reset()
		inBlock = false
	}
	for _, line := range strings.SplitAfter(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			flush()
			inBlock = trimmed == "[[hooks]]"
		}
		if inBlock {
			block.WriteString(line)
		} else {
			out.WriteString(line)
		}
	}
	flush()
	return out.String()
}

// readKimiNativeHooks lists the [[hooks]] rules in config.toml for `hook list`.
func readKimiNativeHooks(agent string, path string) ([]nativeHookEntry, error) {
	content, err := readKimiConfig(path)
	if err != nil {
		return nil, err
	}
	rules, err := kimiHookRules(path, content)
	if err != nil {
		return nil, err
	}
	var entries []nativeHookEntry
	for _, rule := range rules {
		if strings.TrimSpace(rule.Command) != "" {
			entries = append(entries, nativeHookEntry{Agent: agent, Event: rule.Event, Command: rule.Command, ConfigPath: path})
		}
	}
	return entries, nil
}

func removeKimiNativeHookEntries(path string, entries []nativeHookEntry) (bool, error) {
	content, err := readKimiConfig(path)
	if err != nil {
		return false, err
	}
	updated := removeKimiHookBlocks(content, func(command string) bool {
		for _, entry := range entries {
			if hookCommandMatches(command, entry.Command) {
				return true
			}
		}
		return false
	})
	if updated == content {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
