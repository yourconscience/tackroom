// Command stats appends a snapshot of tackroom's public adoption numbers to a
// Markdown file: stars and npm downloads next to comparable tools, repo views,
// clones and referrers, release downloads and outside issues and PRs.
//
// GitHub numbers come through `gh api`, so traffic needs a gh login with push
// access to the repo. npm numbers come from the public downloads API.
//
//	go -C tools/stats run . -out "$KNOWLEDGE_DIR/research/tackroom-adoption.md"
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// peer is a tool compared with tackroom: a GitHub repo and, when it ships on
// npm, its package.
type peer struct{ label, repo, npm string }

var peers = []peer{
	{"tackroom", "yourconscience/tackroom", "tackroom"},
	{"skillshare", "runkids/skillshare", ""},
	{"ruler", "intellectronica/ruler", "@intellectronica/ruler"},
	{"rulesync", "dyoshikawa/rulesync", "rulesync"},
	{"openskills", "numman-ali/openskills", "openskills"},
	{"HarnessKit", "RealZST/HarnessKit", ""},
}

func main() {
	out := flag.String("out", "", "Markdown file to append the snapshot to (default: print it)")
	flag.Parse()
	snap, err := snapshot(time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "stats:", err)
		os.Exit(1)
	}
	if *out == "" {
		fmt.Print(snap)
		return
	}
	if err := appendSnapshot(*out, snap); err != nil {
		fmt.Fprintln(os.Stderr, "stats:", err)
		os.Exit(1)
	}
	fmt.Println("appended to", *out)
}

func snapshot(now time.Time) (string, error) {
	own := peers[0].repo
	var b strings.Builder
	fmt.Fprintf(&b, "\n## %s\n\n", now.Format("2006-01-02"))

	header, rule, stars, npm := "|", "|---", "| Stars", "| npm downloads, last week"
	for _, p := range peers {
		header += " | " + p.label
		rule += "|---"
		var repo struct {
			Stars int `json:"stargazers_count"`
		}
		if err := ghJSON("repos/"+p.repo, &repo); err != nil {
			return "", err
		}
		stars += " | " + fmt.Sprint(repo.Stars)
		npm += " | " + npmWeek(p.npm)
	}
	fmt.Fprintf(&b, "%s |\n%s|\n%s |\n%s |\n\n", header, rule, stars, npm)

	var views, clones struct{ Count, Uniques int }
	if err := ghJSON("repos/"+own+"/traffic/views", &views); err != nil {
		return "", err
	}
	if err := ghJSON("repos/"+own+"/traffic/clones", &clones); err != nil {
		return "", err
	}
	var referrers []struct {
		Referrer string
		Uniques  int
	}
	if err := ghJSON("repos/"+own+"/traffic/popular/referrers", &referrers); err != nil {
		return "", err
	}
	var refs []string
	for i, r := range referrers {
		if i == 5 {
			break
		}
		refs = append(refs, fmt.Sprintf("%s %d", r.Referrer, r.Uniques))
	}
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Downloads int `json:"download_count"`
		}
	}
	if err := ghJSON("repos/"+own+"/releases/latest", &release); err != nil {
		return "", err
	}
	downloads := 0
	for _, a := range release.Assets {
		downloads += a.Downloads
	}
	since := now.AddDate(0, 0, -7).Format("2006-01-02")
	owner, _, _ := strings.Cut(own, "/")
	var search struct {
		Items []struct {
			Number int
			User   struct{ Login, Type string }
			PR     *struct{} `json:"pull_request"`
		}
	}
	q := fmt.Sprintf("repo:%s created:>=%s -author:%s", own, since, owner)
	if err := ghJSON("search/issues?per_page=100&q="+urlQuery(q), &search); err != nil {
		return "", err
	}
	var outside []string
	for _, it := range search.Items {
		if it.User.Type == "Bot" {
			continue
		}
		kind := "issue"
		if it.PR != nil {
			kind = "PR"
		}
		outside = append(outside, fmt.Sprintf("%s #%d by %s", kind, it.Number, it.User.Login))
	}

	fmt.Fprintf(&b, "- Views, last 14 days: %d (%d unique)\n", views.Count, views.Uniques)
	fmt.Fprintf(&b, "- Clones, last 14 days: %d (%d unique)\n", clones.Count, clones.Uniques)
	fmt.Fprintf(&b, "- Referrers, unique visitors: %s\n", orNone(strings.Join(refs, ", ")))
	fmt.Fprintf(&b, "- Latest release %s: %d asset downloads\n", release.TagName, downloads)
	fmt.Fprintf(&b, "- Outside issues and PRs since %s: %s\n", since, orNone(strings.Join(outside, ", ")))
	return b.String(), nil
}

func appendSnapshot(path, snap string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		snap = "# tackroom adoption snapshots\n\nAppended weekly by tools/stats in the tackroom repo. Views, clones and referrers cover the 14 days before each date.\n" + snap
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(snap); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ghJSON runs `gh api path` and decodes its output. A scheduled run has a
// short PATH, so gh is also looked for where Homebrew puts it.
func ghJSON(path string, v any) error {
	gh, err := exec.LookPath("gh")
	if err != nil {
		for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
			if _, statErr := os.Stat(filepath.Join(dir, "gh")); statErr == nil {
				gh, err = filepath.Join(dir, "gh"), nil
				break
			}
		}
	}
	if err != nil {
		return fmt.Errorf("gh not found: %w", err)
	}
	cmd := exec.Command(gh, "api", path)
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("gh api %s: %w", path, err)
	}
	return json.Unmarshal(data, v)
}

// npmWeek is a package's downloads over the last week, or n/a.
func npmWeek(pkg string) string {
	if pkg == "" {
		return "n/a"
	}
	client := http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("https://api.npmjs.org/downloads/point/last-week/" + pkg)
	if err != nil {
		return "error"
	}
	defer resp.Body.Close()
	var body struct{ Downloads int }
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil {
		return "error"
	}
	return fmt.Sprint(body.Downloads)
}

func urlQuery(s string) string {
	return strings.NewReplacer("%", "%25", " ", "+", ":", "%3A", ">", "%3E", "=", "%3D", "/", "%2F").Replace(s)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
