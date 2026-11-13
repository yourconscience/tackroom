// Command sitedocs assembles the published tackroom site: everything in
// docs/site, plus every docs/*.md as a rendered page (docs/<name>/) and as the
// raw markdown next to it (docs/<name>.md), and an llms.txt at the root.
//
// Local build, from anywhere inside the checkout (the output folder must be
// new or empty; pass an absolute path because go -C changes directory):
//
//	go -C tools/sitedocs run . -out /tmp/tackroom-site
//
// Preview with any static file server rooted at that folder.
package main

import (
	"bytes"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	mdhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

//go:embed template.html docs.css
var assets embed.FS

const (
	repoURL     = "https://github.com/yourconscience/tackroom"
	defaultBase = "https://yourconscience.github.io/tackroom"

	// The pages link tokens.css and fonts/ at the site root, where docs/site
	// serves them. Until docs/site has its own copies, the build copies these
	// sources there. Change the source here, in one place.
	tokensSource  = "docs/brand/tokens-a.css"
	fontsSource   = "internal/app/web/fonts"
	noticesSource = "internal/app/web/THIRD_PARTY_NOTICES.md"

	// The mark in the page header, served with the docs stylesheet.
	markSource = "docs/brand/logo-a.svg"
	assetsDir  = "assets/docs"
)

type doc struct {
	File    string // setup.md
	Dir     string // output folder relative to the site root: docs/setup
	Source  []byte
	Title   string
	Summary string
	Body    string

	root ast.Node
	ids  map[string]bool
	refs []ref
}

type ref struct {
	to   *doc
	frag string
}

type navItem struct {
	Title, Href string
	Current     bool
}

type page struct {
	Title, Summary                   string
	Root, DocsHref, RawHref, EditURL string
	RepoURL                          string
	Nav                              []navItem
	Content                          template.HTML
}

type builder struct {
	root   string
	base   string
	md     goldmark.Markdown
	byFile map[string]*doc // repo path (docs/setup.md) to doc
	index  *doc
	nav    []*doc // reading order, index excluded
}

func main() {
	out := flag.String("out", "", "output folder, must be new or empty (required)")
	repo := flag.String("repo", "", "checkout root; default: the nearest parent of the working directory containing docs/site")
	base := flag.String("base", defaultBase, "public site URL, used for the URLs in llms.txt")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "sitedocs: -out is required")
		os.Exit(2)
	}
	if err := run(*repo, *out, strings.TrimRight(*base, "/")); err != nil {
		fmt.Fprintln(os.Stderr, "sitedocs:", err)
		os.Exit(1)
	}
}

func run(repo, out, base string) error {
	root, err := findRoot(repo)
	if err != nil {
		return err
	}
	if entries, err := os.ReadDir(out); err == nil && len(entries) > 0 {
		return fmt.Errorf("output folder %s is not empty; remove it or pick a new one", out)
	}
	b := &builder{
		root: root,
		base: base,
		md: goldmark.New(
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithRendererOptions(mdhtml.WithUnsafe()),
		),
		byFile: map[string]*doc{},
	}
	if err := b.load(); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(root, "docs", "site"), out); err != nil {
		return err
	}
	if err := b.writePages(out); err != nil {
		return err
	}
	if err := b.writeAssets(out); err != nil {
		return err
	}
	if err := writeFile(out, "llms.txt", []byte(b.llms())); err != nil {
		return err
	}
	b.checkAnchors()
	fmt.Fprintf(os.Stderr, "sitedocs: wrote %d docs pages to %s\n", len(b.nav)+1, out)
	return nil
}

func findRoot(repo string) (string, error) {
	if repo != "" {
		return filepath.Abs(repo)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, "docs", "site")); err == nil && fi.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no docs/site found above the working directory; run inside the checkout or pass -repo")
		}
		dir = parent
	}
}

// load parses docs/*.md, gives every heading its GitHub-style id and decides
// the reading order.
func (b *builder) load() error {
	files, err := filepath.Glob(filepath.Join(b.root, "docs", "*.md"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	var docs []*doc
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		name := filepath.Base(f)
		d := &doc{File: name, Source: src, ids: map[string]bool{}}
		if name == "README.md" {
			d.Dir = "docs"
			b.index = d
		} else {
			d.Dir = "docs/" + strings.TrimSuffix(name, ".md")
			docs = append(docs, d)
		}
		b.byFile["docs/"+name] = d
		b.parse(d)
	}
	if b.index == nil {
		b.index = &doc{Dir: "docs", Title: "Documentation"}
	}
	// Alphabetical with setup first; README.md link order takes precedence.
	sort.SliceStable(docs, func(i, j int) bool {
		a, c := docs[i].File, docs[j].File
		if (a == "setup.md") != (c == "setup.md") {
			return a == "setup.md"
		}
		return a < c
	})
	seen := map[*doc]bool{}
	if b.index.root != nil {
		_ = ast.Walk(b.index.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if l, ok := n.(*ast.Link); ok && entering {
				if target, _, ok := splitDest(string(l.Destination)); ok {
					if t := b.byFile[target]; t != nil && t != b.index && !seen[t] {
						seen[t] = true
						b.nav = append(b.nav, t)
					}
				}
			}
			return ast.WalkContinue, nil
		})
	}
	for _, d := range docs {
		if !seen[d] {
			b.nav = append(b.nav, d)
		}
	}
	for _, d := range append([]*doc{b.index}, b.nav...) {
		if d.root != nil {
			b.rewriteLinks(d)
		}
	}
	return nil
}

func (b *builder) parse(d *doc) {
	d.root = b.md.Parser().Parse(text.NewReader(d.Source))
	slugs := newSlugger()
	d.Title = strings.TrimSuffix(d.File, ".md")
	titled := false
	_ = ast.Walk(d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			label := plainText(n, d.Source)
			id := slugs.next(label)
			n.SetAttributeString("id", []byte(id))
			d.ids[id] = true
			if n.Level == 1 && !titled {
				d.Title, titled = strings.TrimSpace(label), true
			}
		case *ast.Paragraph:
			if d.Summary == "" && n.Parent() == d.root {
				d.Summary = strings.Join(strings.Fields(plainText(n, d.Source)), " ")
			}
		}
		return ast.WalkContinue, nil
	})
}

// rewriteLinks points relative links at rendered pages or at GitHub.
func (b *builder) rewriteLinks(d *doc) {
	_ = ast.Walk(d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		l, ok := n.(*ast.Link)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		dest := string(l.Destination)
		if strings.HasPrefix(dest, "#") {
			d.refs = append(d.refs, ref{d, dest[1:]})
			return ast.WalkContinue, nil
		}
		target, frag, ok := splitDest(dest)
		if !ok {
			return ast.WalkContinue, nil
		}
		if t := b.byFile[target]; t != nil {
			d.refs = append(d.refs, ref{t, frag})
			dest = relDir(d.Dir, t.Dir)
		} else {
			dest = b.githubURL(target)
		}
		if frag != "" {
			dest += "#" + frag
		}
		l.Destination = []byte(dest)
		return ast.WalkContinue, nil
	})
}

// splitDest resolves a link written in docs/*.md to a repo path and fragment.
// ok is false for absolute URLs, mail links, and paths outside the repo.
func splitDest(dest string) (target, frag string, ok bool) {
	dest, frag, _ = strings.Cut(dest, "#")
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "", "", false
	}
	if strings.HasPrefix(u.Path, "/") {
		target = path.Clean(u.Path[1:])
	} else {
		target = path.Join("docs", u.Path)
	}
	if target == ".." || strings.HasPrefix(target, "../") {
		return "", "", false
	}
	return target, frag, true
}

func (b *builder) githubURL(repoPath string) string {
	if repoPath == "." {
		return repoURL + "/tree/main"
	}
	kind := "blob"
	if fi, err := os.Stat(filepath.Join(b.root, filepath.FromSlash(repoPath))); err == nil && fi.IsDir() {
		kind = "tree"
	}
	return repoURL + "/" + kind + "/main/" + (&url.URL{Path: repoPath}).EscapedPath()
}

// relDir is the relative URL, with a trailing slash, from one output folder to another.
func relDir(from, to string) string {
	r, _ := filepath.Rel(from, to)
	r = filepath.ToSlash(r)
	if r == "." {
		return "./"
	}
	return r + "/"
}

func relFile(from, to string) string {
	r, _ := filepath.Rel(from, to)
	return filepath.ToSlash(r)
}

func (b *builder) writePages(out string) error {
	tpl, err := template.ParseFS(assets, "template.html")
	if err != nil {
		return err
	}
	for _, d := range append([]*doc{b.index}, b.nav...) {
		p := page{
			Title:    d.Title,
			Summary:  d.Summary,
			Root:     relDir(d.Dir, "."),
			DocsHref: relDir(d.Dir, "docs"),
			RepoURL:  repoURL,
		}
		if d.root != nil {
			var buf bytes.Buffer
			if err := b.md.Renderer().Render(&buf, d.Source, d.root); err != nil {
				return err
			}
			p.Content = template.HTML(boxes.Replace(buf.String()))
			p.RawHref = relFile(d.Dir, "docs/"+d.File)
			p.EditURL = repoURL + "/edit/main/docs/" + d.File
		} else {
			p.Content = template.HTML(b.indexList())
		}
		for _, n := range b.nav {
			p.Nav = append(p.Nav, navItem{Title: n.Title, Href: relDir(d.Dir, n.Dir), Current: n == d})
		}
		var buf bytes.Buffer
		if err := tpl.Execute(&buf, p); err != nil {
			return err
		}
		if err := writeFile(out, d.Dir+"/index.html", buf.Bytes()); err != nil {
			return err
		}
		if d.root != nil {
			if err := writeFile(out, "docs/"+d.File, d.Source); err != nil {
				return err
			}
		}
	}
	return nil
}

// boxes lets tables and code blocks scroll inside their own box and be reached by keyboard.
var boxes = strings.NewReplacer(
	"<table>", `<div class="table-scroll" role="region" aria-label="Table" tabindex="0"><table>`,
	"</table>", "</table></div>",
	"<pre>", `<pre tabindex="0">`,
)

// indexList is the docs landing page when docs/README.md does not exist.
func (b *builder) indexList() string {
	var s strings.Builder
	s.WriteString("<h1>Documentation</h1>\n<ul>\n")
	for _, n := range b.nav {
		fmt.Fprintf(&s, "<li><a href=\"%s/\">%s</a>", strings.TrimPrefix(n.Dir, "docs/"), html.EscapeString(n.Title))
		if n.Summary != "" {
			s.WriteString(": " + html.EscapeString(n.Summary))
		}
		s.WriteString("</li>\n")
	}
	s.WriteString("</ul>\n")
	return s.String()
}

func (b *builder) writeAssets(out string) error {
	css, err := assets.ReadFile("docs.css")
	if err != nil {
		return err
	}
	if err := writeFile(out, assetsDir+"/docs.css", css); err != nil {
		return err
	}
	if err := b.copyFile(out, markSource, assetsDir+"/logo.svg"); err != nil {
		return err
	}
	if !b.inSite("tokens.css") {
		if err := b.copyFile(out, tokensSource, "tokens.css"); err != nil {
			return err
		}
	}
	if b.inSite("fonts") {
		return nil
	}
	fonts, err := filepath.Glob(filepath.Join(b.root, filepath.FromSlash(fontsSource), "*.woff2"))
	if err != nil {
		return err
	}
	if len(fonts) == 0 {
		return fmt.Errorf("no .woff2 files in %s", fontsSource)
	}
	for _, f := range fonts {
		if err := b.copyFile(out, filepath.ToSlash(mustRel(b.root, f)), "fonts/"+filepath.Base(f)); err != nil {
			return err
		}
	}
	return b.copyFile(out, noticesSource, "fonts/THIRD_PARTY_NOTICES.md")
}

func (b *builder) inSite(name string) bool {
	_, err := os.Stat(filepath.Join(b.root, "docs", "site", name))
	return err == nil
}

func mustRel(base, p string) string {
	r, _ := filepath.Rel(base, p)
	return r
}

// copyFile copies a repo file into the output.
func (b *builder) copyFile(out, from, to string) error {
	data, err := os.ReadFile(filepath.Join(b.root, filepath.FromSlash(from)))
	if err != nil {
		return err
	}
	return writeFile(out, to, data)
}

// llms.txt lists the raw markdown of every docs page.
func (b *builder) llms() string {
	var s strings.Builder
	s.WriteString("# tackroom\n\n")
	if b.index.Summary != "" {
		s.WriteString("> " + b.index.Summary + "\n\n")
	}
	s.WriteString("## Docs\n\n")
	docs := b.nav
	if b.index.root != nil {
		docs = append([]*doc{b.index}, docs...)
	}
	for _, d := range docs {
		fmt.Fprintf(&s, "- [%s](%s/docs/%s)", d.Title, b.base, d.File)
		if d.Summary != "" {
			s.WriteString(": " + d.Summary)
		}
		s.WriteString("\n")
	}
	return s.String()
}

// checkAnchors warns about links to headings that do not exist; the site still builds.
func (b *builder) checkAnchors() {
	for _, d := range append([]*doc{b.index}, b.nav...) {
		for _, r := range d.refs {
			if r.frag != "" && r.to.root != nil && !r.to.ids[r.frag] {
				fmt.Fprintf(os.Stderr, "sitedocs: warning: %s links to %s#%s, which has no such heading\n", d.File, r.to.File, r.frag)
			}
		}
	}
}

func copyTree(src, out string) error {
	return filepath.WalkDir(src, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == src {
			return nil
		}
		if strings.HasPrefix(e.Name(), ".") {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if e.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return writeFile(out, filepath.ToSlash(rel), data)
	})
}

// writeFile refuses to overwrite, so a docs/site file that collides with a
// generated one fails the build instead of being silently replaced.
func writeFile(out, rel string, data []byte) error {
	p := filepath.Join(out, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// plainText is the visible text of a node, as GitHub sees it when it makes heading ids.
func plainText(n ast.Node, src []byte) string {
	var s strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			s.Write(c.Value(src))
			if c.SoftLineBreak() || c.HardLineBreak() {
				s.WriteByte(' ')
			}
		case *ast.String:
			s.Write(c.Value)
		case *ast.AutoLink:
			s.Write(c.Label(src))
		}
		return ast.WalkContinue, nil
	})
	return s.String()
}

// slugger makes GitHub heading anchors: lowercase, letters, marks, numbers,
// underscores and hyphens kept, spaces to hyphens, repeats suffixed -1, -2.
type slugger struct {
	used   map[string]bool
	counts map[string]int
}

func newSlugger() *slugger {
	return &slugger{used: map[string]bool{}, counts: map[string]int{}}
}

func (g *slugger) next(label string) string {
	var s strings.Builder
	for _, r := range strings.ToLower(label) {
		switch {
		case r == ' ':
			s.WriteByte('-')
		case r == '-' || unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r) || unicode.Is(unicode.Pc, r):
			s.WriteRune(r)
		}
	}
	slug := s.String()
	id := slug
	for g.used[id] {
		g.counts[slug]++
		id = fmt.Sprintf("%s-%d", slug, g.counts[slug])
	}
	g.used[id] = true
	return id
}
