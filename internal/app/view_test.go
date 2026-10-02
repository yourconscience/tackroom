package app

import (
	"strings"
	"testing"
)

func TestParseViewFlags(t *testing.T) {
	opts, err := parseViewFlags([]string{"--no-open", "--secure-cookie", "--addr", "127.0.0.1:9000", "--ssh-host", "kirill@box"})
	if err != nil {
		t.Fatalf("parseViewFlags error: %v", err)
	}
	if !opts.NoOpen {
		t.Fatal("--no-open not parsed")
	}
	if !opts.SecureCookie {
		t.Fatal("--secure-cookie not parsed")
	}
	if opts.Addr != "127.0.0.1:9000" {
		t.Fatalf("Addr = %q, want 127.0.0.1:9000", opts.Addr)
	}
	if opts.SSHHost != "kirill@box" {
		t.Fatalf("SSHHost = %q, want kirill@box", opts.SSHHost)
	}
}

func TestParseViewFlagsDefaults(t *testing.T) {
	opts, err := parseViewFlags(nil)
	if err != nil {
		t.Fatalf("parseViewFlags error: %v", err)
	}
	if opts.Addr != "127.0.0.1:8765" {
		t.Fatalf("default Addr = %q, want 127.0.0.1:8765", opts.Addr)
	}
	if opts.NoOpen || opts.SecureCookie || opts.SSHHost != "" || opts.ConfigPath != "" {
		t.Fatalf("unexpected non-default view options: %+v", opts)
	}
}

func TestParseViewFlagsRejectsPositional(t *testing.T) {
	if _, err := parseViewFlags([]string{"serve"}); err == nil {
		t.Fatal("expected error for positional argument")
	}
}

func TestRunViewLegacyHarnessKitFlagsGiveRenameGuidance(t *testing.T) {
	for _, flag := range []string{"--port", "--host", "--no-token", "--name"} {
		t.Run(flag, func(t *testing.T) {
			// Legacy HarnessKit args must not launch anything; they point at inspect.
			for _, args := range [][]string{{flag, "7070"}, {flag + "=x"}} {
				err := runView(args)
				if err == nil {
					t.Fatalf("runView(%v) = nil, want rename guidance error", args)
				}
				if !strings.Contains(err.Error(), "tackroom inspect") || !strings.Contains(err.Error(), flag) {
					t.Fatalf("runView(%v) error = %q, want guidance naming %s and inspect", args, err, flag)
				}
			}
		})
	}
}

func TestFirstLegacyInspectFlag(t *testing.T) {
	if got := firstLegacyInspectFlag([]string{"--no-open", "--addr", "127.0.0.1:8765"}); got != "" {
		t.Fatalf("firstLegacyInspectFlag on config-UI flags = %q, want empty", got)
	}
	if got := firstLegacyInspectFlag([]string{"--no-open", "--port", "7070"}); got != "--port" {
		t.Fatalf("firstLegacyInspectFlag = %q, want --port", got)
	}
	if got := firstLegacyInspectFlag([]string{"--host=0.0.0.0"}); got != "--host" {
		t.Fatalf("firstLegacyInspectFlag equals form = %q, want --host", got)
	}
}

func TestPortFromURL(t *testing.T) {
	if got := portFromURL("http://127.0.0.1:8765/?token=x"); got != "8765" {
		t.Fatalf("portFromURL = %q, want 8765", got)
	}
	if got := portFromURL("http://example.com/path"); got != "" {
		t.Fatalf("portFromURL without port = %q, want empty", got)
	}
}

func TestResolveSSHHost(t *testing.T) {
	explicit := func(string) string { return "" }
	if got := resolveSSHHost("user@host", explicit); got != "user@host" {
		t.Fatalf("explicit host = %q", got)
	}

	env := func(k string) string {
		switch k {
		case "SSH_CONNECTION":
			return "10.0.0.5 51234 10.0.0.9 22"
		case "USER":
			return "kirill"
		}
		return ""
	}
	if got := resolveSSHHost("", env); got != "kirill@10.0.0.9" {
		t.Fatalf("derived host = %q, want kirill@10.0.0.9", got)
	}

	none := func(string) string { return "" }
	if got := resolveSSHHost("", none); got != "" {
		t.Fatalf("no SSH session host = %q, want empty", got)
	}
}

func TestTunnelCommand(t *testing.T) {
	cmd, ok := tunnelCommand("http://127.0.0.1:8765/?token=x", "user@host")
	if !ok || cmd != "ssh -L 8765:localhost:8765 user@host" {
		t.Fatalf("tunnelCommand = %q, %v", cmd, ok)
	}
	if _, ok := tunnelCommand("http://127.0.0.1:8765/", ""); ok {
		t.Fatal("tunnelCommand should fail without a host")
	}
}

func TestAnnounceViewLocalOpensBrowser(t *testing.T) {
	orig := openInBrowser
	t.Cleanup(func() { openInBrowser = orig })
	var opened string
	openInBrowser = func(url string) error { opened = url; return nil }

	var b strings.Builder
	url := "http://127.0.0.1:8765/?token=abc"
	announceView(&b, url, configServeOptions{}, false, "")

	out := b.String()
	if !strings.Contains(out, url) {
		t.Fatalf("URL not printed on its own: %q", out)
	}
	if opened != url {
		t.Fatalf("browser not opened, got %q", opened)
	}
	if !strings.Contains(out, "default browser") {
		t.Fatalf("missing browser notice: %q", out)
	}
	if !strings.Contains(out, "config UI") {
		t.Fatalf("banner should name the config UI: %q", out)
	}
}

func TestAnnounceViewNoOpen(t *testing.T) {
	orig := openInBrowser
	t.Cleanup(func() { openInBrowser = orig })
	called := false
	openInBrowser = func(string) error { called = true; return nil }

	var b strings.Builder
	announceView(&b, "http://127.0.0.1:8765/?token=abc", configServeOptions{NoOpen: true}, false, "")

	if called {
		t.Fatal("--no-open must not open a browser")
	}
	if !strings.Contains(b.String(), "http://127.0.0.1:8765/?token=abc") {
		t.Fatal("URL should still be printed with --no-open")
	}
}

func TestAnnounceViewRemoteShowsTunnelNotBrowser(t *testing.T) {
	orig := openInBrowser
	t.Cleanup(func() { openInBrowser = orig })
	called := false
	openInBrowser = func(string) error { called = true; return nil }

	var b strings.Builder
	announceView(&b, "http://127.0.0.1:8765/?token=abc", configServeOptions{}, true, "kirill@10.0.0.9")

	out := b.String()
	if called {
		t.Fatal("remote host must not open a local browser")
	}
	if !strings.Contains(out, "ssh -L 8765:localhost:8765 kirill@10.0.0.9") {
		t.Fatalf("missing tunnel command: %q", out)
	}
}
