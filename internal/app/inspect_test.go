package app

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestHKServeArgs(t *testing.T) {
	if got := hkServeArgs(nil); !reflect.DeepEqual(got, []string{"serve"}) {
		t.Fatalf("hkServeArgs(nil) = %v, want [serve]", got)
	}
	got := hkServeArgs([]string{"--port", "8080"})
	want := []string{"serve", "--port", "8080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hkServeArgs passthrough = %v, want %v", got, want)
	}
}

func TestRunInspectMissingBinary(t *testing.T) {
	orig := hkLookPath
	t.Cleanup(func() { hkLookPath = orig })
	hkLookPath = func(string) (string, error) { return "", errors.New("not found") }

	err := runInspect(nil)
	if err == nil {
		t.Fatal("expected error when hk binary is missing")
	}
	if !strings.Contains(err.Error(), "HarnessKit") || !strings.Contains(err.Error(), "github.com/RealZST/HarnessKit") {
		t.Fatalf("error should guide install, got: %v", err)
	}
	if !strings.Contains(err.Error(), "tackroom inspect") {
		t.Fatalf("install hint should point at the inspect command, got: %v", err)
	}
}

func TestParseInspectArgs(t *testing.T) {
	opts, passthrough, err := parseInspectArgs([]string{"--no-open", "--ssh-host", "kirill@box", "--port", "8080", "--no-token"})
	if err != nil {
		t.Fatalf("parseInspectArgs error: %v", err)
	}
	if !opts.NoOpen {
		t.Fatal("--no-open not parsed")
	}
	if opts.SSHHost != "kirill@box" {
		t.Fatalf("SSHHost = %q, want kirill@box", opts.SSHHost)
	}
	if want := []string{"--port", "8080", "--no-token"}; !reflect.DeepEqual(passthrough, want) {
		t.Fatalf("passthrough = %v, want %v", passthrough, want)
	}
}

func TestParseInspectArgsSSHHostEquals(t *testing.T) {
	opts, passthrough, err := parseInspectArgs([]string{"--ssh-host=user@1.2.3.4"})
	if err != nil {
		t.Fatalf("parseInspectArgs error: %v", err)
	}
	if opts.SSHHost != "user@1.2.3.4" {
		t.Fatalf("SSHHost = %q", opts.SSHHost)
	}
	if len(passthrough) != 0 {
		t.Fatalf("passthrough = %v, want empty", passthrough)
	}
}

func TestParseInspectArgsSSHHostMissingValue(t *testing.T) {
	if _, _, err := parseInspectArgs([]string{"--ssh-host"}); err == nil {
		t.Fatal("expected error for --ssh-host without a value")
	}
}

func TestExtractServeURL(t *testing.T) {
	line := "HarnessKit Web UI [host] running at http://127.0.0.1:7070/?token=abc123"
	url, ok := extractServeURL(line)
	if !ok || url != "http://127.0.0.1:7070/?token=abc123" {
		t.Fatalf("extractServeURL = %q, %v", url, ok)
	}
	if _, ok := extractServeURL("Auth token: abc123"); ok {
		t.Fatal("extractServeURL should not match a non-URL line")
	}
}

func TestHKBannerNoise(t *testing.T) {
	if !hkBannerNoise("Access via SSH tunnel: ssh -L 7070:localhost:7070 your-server") {
		t.Fatal("tunnel line should be suppressible")
	}
	if !hkBannerNoise("Auth token: abc123") {
		t.Fatal("token line should be suppressible")
	}
	if hkBannerNoise("some runtime log line") {
		t.Fatal("ordinary lines must not be suppressed")
	}
}

func TestAnnounceInspectLocalOpensBrowser(t *testing.T) {
	orig := openInBrowser
	t.Cleanup(func() { openInBrowser = orig })
	var opened string
	openInBrowser = func(url string) error { opened = url; return nil }

	var b strings.Builder
	url := "http://127.0.0.1:7070/?token=abc"
	announceInspect(&b, url, inspectOptions{}, false, "")

	out := b.String()
	if !strings.Contains(out, url) {
		t.Fatalf("URL not printed on its own: %q", out)
	}
	if opened != url {
		t.Fatalf("browser not opened, got %q", opened)
	}
	if !strings.Contains(out, "HarnessKit inspector") {
		t.Fatalf("banner should name the HarnessKit inspector: %q", out)
	}
}

func TestAnnounceInspectRemoteShowsTunnelNotBrowser(t *testing.T) {
	orig := openInBrowser
	t.Cleanup(func() { openInBrowser = orig })
	called := false
	openInBrowser = func(string) error { called = true; return nil }

	var b strings.Builder
	announceInspect(&b, "http://127.0.0.1:7070/?token=abc", inspectOptions{}, true, "kirill@10.0.0.9")

	out := b.String()
	if called {
		t.Fatal("remote host must not open a local browser")
	}
	if !strings.Contains(out, "ssh -L 7070:localhost:7070 kirill@10.0.0.9") {
		t.Fatalf("missing tunnel command: %q", out)
	}
}
