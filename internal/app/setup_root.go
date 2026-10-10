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
		if linkMatchesExpected(path, filepath.Join(repoRoot, "AGENTS.md")) {
			continue // already linked to the shared file
		}
		// Follow links too: an instructions file symlinked into dotfiles is
		// still the user's content, and sync will relink it.
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
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
	// O_EXCL: never truncate an AGENTS.md that appeared since the check.
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		fmt.Fprintf(streams.out, "root instructions: %s already exists; nothing imported\n\n", target)
		return nil
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}
	if _, err := file.Write(content); err != nil {
		file.Close()
		return fmt.Errorf("write %s: %w", target, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	fmt.Fprintf(streams.out, "root instructions: imported %d file(s) into %s\n", len(sources), target)
	for _, src := range sources {
		fmt.Fprintf(streams.out, "  %s (%s)\n", displayPath(src.Path, home), src.Agent)
	}
	fmt.Fprintln(streams.out)
	return nil
}

// linkMatchesExpected reports whether path is a symlink to expected.
func linkMatchesExpected(path string, expected string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := os.Readlink(path)
	return err == nil && linkMatches(path, target, expected)
}

func displayPath(path string, home string) string {
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return path
}
