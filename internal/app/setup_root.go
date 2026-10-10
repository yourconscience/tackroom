package app

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type rootInstructionSource struct {
	Agent string
	Path  string
	Data  []byte
}

// rootInstructionSources lists the detected agents' own instructions files
// (for example ~/.claude/CLAUDE.md) that setup would import, deduplicated by
// content. It returns nothing once the config root already has AGENTS.md.
func rootInstructionSources(repoRoot string, detected []agentConfig, home string) []rootInstructionSource {
	if _, err := os.Stat(filepath.Join(repoRoot, "AGENTS.md")); err == nil {
		return nil
	}
	var sources []rootInstructionSource
	for _, agent := range detected {
		h := harnessFor(agent.Name)
		if h == nil || h.RootInstructions == nil {
			continue
		}
		path := h.RootInstructions.Path(home)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue // missing, or already a link tackroom manages
		}
		data, err := os.ReadFile(path)
		if err != nil || len(bytes.TrimSpace(data)) == 0 {
			continue
		}
		duplicate := false
		for _, src := range sources {
			if bytes.Equal(bytes.TrimSpace(src.Data), bytes.TrimSpace(data)) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			sources = append(sources, rootInstructionSource{Agent: agent.Name, Path: path, Data: data})
		}
	}
	return sources
}

// importRootInstructions creates the shared AGENTS.md from the instructions
// the user already has. One source is copied as is; several distinct ones are
// kept in full under a heading each, for the user to merge. With nothing to
// import, no AGENTS.md is created and every agent keeps its own file.
// Sync then backs up the originals and links them to the shared file.
func importRootInstructions(repoRoot string, detected []agentConfig, home string, streams setupIO) error {
	sources := rootInstructionSources(repoRoot, detected, home)
	if len(sources) == 0 {
		return nil
	}
	var content []byte
	if len(sources) == 1 {
		content = sources[0].Data
	} else {
		var b strings.Builder
		b.WriteString("<!-- Imported by tackroom setup from several agents' instructions. Merge them into one set when convenient. -->\n")
		for _, src := range sources {
			fmt.Fprintf(&b, "\n## From %s (%s)\n\n", src.Agent, displayPath(src.Path, home))
			b.Write(bytes.TrimRight(src.Data, "\n"))
			b.WriteString("\n")
		}
		content = []byte(b.String())
	}
	target := filepath.Join(repoRoot, "AGENTS.md")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", repoRoot, err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("write %s: %w", target, err)
	}
	fmt.Fprintf(streams.out, "root instructions: imported %d file(s) into %s\n", len(sources), target)
	for _, src := range sources {
		fmt.Fprintf(streams.out, "  %s (%s)\n", displayPath(src.Path, home), src.Agent)
	}
	fmt.Fprintln(streams.out)
	return nil
}

func displayPath(path string, home string) string {
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return path
}
