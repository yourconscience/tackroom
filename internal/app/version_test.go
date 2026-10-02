package app

import (
	"strings"
	"testing"
)

func TestVersionCommandPrintsInjectedVersion(t *testing.T) {
	old := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = old })
	for _, arg := range []string{"version", "--version"} {
		stdout, _, err := captureCLIOutput(t, func() error { return Run([]string{arg}) })
		if err != nil {
			t.Fatalf("%s: %v", arg, err)
		}
		if strings.TrimSpace(stdout) != "tackroom 1.2.3" {
			t.Fatalf("%s printed %q, want %q", arg, stdout, "tackroom 1.2.3")
		}
	}
}

func TestVersionFallsBackWithoutInjectedVersion(t *testing.T) {
	old := Version
	Version = ""
	t.Cleanup(func() { Version = old })
	if got := versionString(); got == "" {
		t.Fatal("versionString returned an empty string")
	}
}
