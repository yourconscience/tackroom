package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestFprintStripsEscapesWhenNotATerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	var buf bytes.Buffer
	if err := Fprint(&buf, Bold.Render("tackroom")+" "+Mark("ok")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "tackroom ✓" {
		t.Fatalf("Fprint to a non-terminal = %q, want plain text", got)
	}
}

func TestFprintKeepsColorOnATerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")
	var buf bytes.Buffer
	if err := Fprint(&buf, Red.Render("x")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[31m") {
		t.Fatalf("expected red on a forced terminal, got %q", buf.String())
	}
}

// Any non-empty NO_COLOR disables color (https://no-color.org), including
// values strconv.ParseBool rejects.
func TestFprintHonorsAnyNoColorValue(t *testing.T) {
	for _, value := range []string{"1", "yes"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CLICOLOR_FORCE", "1")
			t.Setenv("NO_COLOR", value)
			var buf bytes.Buffer
			if err := Fprint(&buf, Red.Render("x")+Green.Render("y")); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(buf.String(), "\x1b[3") {
				t.Fatalf("NO_COLOR=%s still printed color: %q", value, buf.String())
			}
		})
	}
}
