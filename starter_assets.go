package tackroom

import "embed"

// StarterAssets contains the public files that tackroom setup scaffolds into a
// user-owned canonical configuration root. The CLI must not read these paths
// from the source checkout at runtime; release binaries carry them here.
//
// This file lives at the repository root on purpose: go:embed can only reach
// files at or below its own directory, so the manifest cannot move into
// internal/. Keep the embed list in sync with the public starter inventory.
//
//go:embed .gitignore AGENTS.md tackroom.yaml tackroom.lock plugin.json agents/*.md skills/tackroom skills/grilling memory/hooks memory/lib memory/tools
var StarterAssets embed.FS
