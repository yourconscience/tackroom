package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveServerTokenEphemeralByDefault(t *testing.T) {
	first, err := resolveServerToken("")
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolveServerToken("")
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first == second {
		t.Fatalf("per-process tokens should be non-empty and distinct: %q %q", first, second)
	}
}

func TestResolveServerTokenStableFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	created, err := resolveServerToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if created == "" {
		t.Fatal("first use should mint a token")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode = %o, want 600", perm)
	}
	reused, err := resolveServerToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if reused != created {
		t.Fatalf("token not stable across restarts: %q != %q", reused, created)
	}
}

func TestParseViewFlagsTokenFile(t *testing.T) {
	opts, err := parseViewFlags([]string{"--token-file", "/tmp/tackroom.token"})
	if err != nil {
		t.Fatalf("parseViewFlags error: %v", err)
	}
	if opts.TokenFile != "/tmp/tackroom.token" {
		t.Fatalf("TokenFile = %q, want /tmp/tackroom.token", opts.TokenFile)
	}
}
