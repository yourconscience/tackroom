package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

type configCommandOptions struct {
	ConfigPath string
}

type configServeOptions struct {
	ConfigPath   string
	Addr         string
	NoOpen       bool
	SecureCookie bool
	// SSHHost, when set, prints a ready-to-copy `ssh -L` tunnel command for
	// reaching the loopback-bound UI from this host instead of auto-opening a
	// browser on a remote box.
	SSHHost string
	// TokenFile, when set, holds a stable session token (created on first use)
	// so a restarted persistent `view` service keeps the same access URL.
	TokenFile string
}

func runConfigCommand(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		opts, err := parseConfigFlags(args)
		if err != nil {
			return err
		}
		return runConfigTUI(opts)
	}
	switch args[0] {
	case "validate":
		opts, err := parseConfigFlags(args[1:])
		if err != nil {
			return err
		}
		return runConfigValidate(opts)
	case "print":
		opts, err := parseConfigFlags(args[1:])
		if err != nil {
			return err
		}
		return runConfigPrint(opts)
	default:
		return fmt.Errorf("unknown config subcommand %q", args[0])
	}
}

func parseConfigFlags(args []string) (configCommandOptions, error) {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var opts configCommandOptions
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to tackroom YAML config")
	if err := fs.Parse(args); err != nil {
		return configCommandOptions{}, err
	}
	if fs.NArg() != 0 {
		return configCommandOptions{}, errors.New("config does not accept positional arguments")
	}
	return opts, nil
}

func openConfigDocument(opts configCommandOptions) (*configDocument, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("resolve home: %w", err)
	}
	path, err := configPathFor(runOptions{ConfigPath: opts.ConfigPath}, home)
	if err != nil {
		return nil, "", err
	}
	doc, err := newConfigDocument(path, home)
	if err != nil {
		return nil, path, err
	}
	return doc, path, nil
}

func runConfigValidate(opts configCommandOptions) error {
	doc, path, err := openConfigDocument(opts)
	if err != nil {
		return err
	}
	fmt.Printf("valid: %s\nrevision: %s\n", path, doc.revision(configLayerShared))
	if len(doc.localBytes) != 0 {
		fmt.Printf("local overlay: %s\n", doc.localPath)
	}
	return nil
}

func runConfigPrint(opts configCommandOptions) error {
	doc, path, err := openConfigDocument(opts)
	if err != nil {
		return err
	}
	fmt.Printf("shared: %s\n", path)
	fmt.Printf("local: %s\n", doc.localPath)
	fmt.Printf("shared revision: %s\n", doc.revision(configLayerShared))
	if len(doc.localBytes) != 0 {
		fmt.Printf("local revision: %s\n", doc.revision(configLayerLocal))
	}
	fmt.Println("effective:")
	data, err := yamlMarshalConfig(doc.effective)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

func runConfigTUI(opts configCommandOptions) error {
	doc, _, err := openConfigDocument(opts)
	if err != nil {
		return err
	}
	model := newConfigTUIModel(doc)
	_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}

type configTUIModel struct {
	doc          *configDocument
	layer        configLayer
	text         []rune
	cursor       int
	editing      bool
	status       string
	diff         string
	width        int
	height       int
	showDiff     bool
	staleText    string
	syncPlan     *syncPlan
	syncRevision string
	syncArmed    bool
}

func newConfigTUIModel(doc *configDocument) configTUIModel {
	return configTUIModel{doc: doc, layer: configLayerShared, text: []rune(string(doc.bytes(configLayerShared)))}
}

func (m configTUIModel) Init() tea.Cmd { return nil }

func (m configTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		if m.editing {
			switch msg.Type {
			case tea.KeyCtrlS:
				m.save()
				return m, nil
			case tea.KeyEsc:
				m.editing = false
			case tea.KeyBackspace:
				if m.cursor > 0 {
					m.text = append(m.text[:m.cursor-1], m.text[m.cursor:]...)
					m.cursor--
				}
			case tea.KeyEnter:
				m.insertRune('\n')
			case tea.KeyLeft:
				if m.cursor > 0 {
					m.cursor--
				}
			case tea.KeyRight:
				if m.cursor < len(m.text) {
					m.cursor++
				}
			case tea.KeyUp:
				m.cursor = verticalCursor(m.text, m.cursor, -1)
			case tea.KeyDown:
				m.cursor = verticalCursor(m.text, m.cursor, 1)
			case tea.KeyHome:
				m.cursor = lineStart(m.text, m.cursor)
			case tea.KeyEnd:
				m.cursor = lineEnd(m.text, m.cursor)
			case tea.KeyDelete:
				if m.cursor < len(m.text) {
					m.text = append(m.text[:m.cursor], m.text[m.cursor+1:]...)
				}
			default:
				if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
					for _, r := range msg.Runes {
						m.insertRune(r)
					}
				}
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "e", "y":
			m.editing = true
			m.showDiff = false
		case "l":
			m.layer = configLayerLocal
			m.text = []rune(string(m.doc.bytes(m.layer)))
			m.cursor = len(m.text)
			m.status = "editing local overlay; effective is never written"
		case "h":
			m.layer = configLayerShared
			m.text = []rune(string(m.doc.bytes(m.layer)))
			m.cursor = len(m.text)
			m.status = "editing shared canonical YAML"
		case "f":
			m.layer = configLayerEffective
			m.text = []rune(string(m.doc.bytes(m.layer)))
			m.cursor = len(m.text)
			m.editing = false
			m.status = "effective merge is read-only"
		case "v":
			m.validate()
		case "r":
			m.showDiff = true
			m.diff = unifiedConfigDiff(m.doc.path(m.layer), m.doc.bytes(m.layer), []byte(string(m.text)))
		case "p":
			m.previewSync()
		case "x":
			m.applySync()
		case "s":
			m.save()
		}
	}
	return m, nil
}

func (m *configTUIModel) previewSync() {
	if m.layer != configLayerShared && m.layer != configLayerLocal {
		m.status = "select shared or local before previewing sync"
		return
	}
	plan, _, revision, err := buildConfigSyncPlan(m.doc)
	if err != nil {
		m.status = "sync preview failed: " + err.Error()
		return
	}
	m.syncPlan = &plan
	m.syncRevision = revision
	m.syncArmed = false
	m.status = fmt.Sprintf("sync preview %s; %d destructive item(s), press x to review/apply", plan.Digest[:12], len(plan.Destructive))
}

func (m *configTUIModel) applySync() {
	if m.syncPlan == nil {
		m.previewSync()
		return
	}
	if len(m.syncPlan.Destructive) > 0 && !m.syncArmed {
		m.syncArmed = true
		m.status = "destructive sync is armed; press x again to apply the reviewed plan"
		return
	}
	configSyncMu.Lock()
	defer configSyncMu.Unlock()
	if err := m.doc.reload(); err != nil {
		m.status = "reload failed: " + err.Error()
		return
	}
	plan, cfg, revision, err := buildConfigSyncPlan(m.doc)
	if err != nil {
		m.status = "sync preview failed: " + err.Error()
		return
	}
	if revision != m.syncRevision {
		m.syncPlan = nil
		m.syncArmed = false
		m.status = "config changed on disk; press p to preview again"
		return
	}
	if plan.Digest != m.syncPlan.Digest {
		m.syncPlan = nil
		m.syncArmed = false
		m.status = "sync plan changed; press p to preview again"
		return
	}
	if err := runSync(runOptions{ConfigPath: m.doc.sharedPath, ConfigOverride: &cfg, Stdout: io.Discard, Stdin: strings.NewReader("n\n")}); err != nil {
		m.status = "sync failed: " + err.Error()
		return
	}
	_ = m.doc.reload()
	m.syncPlan = nil
	m.syncRevision = ""
	m.syncArmed = false
	m.status = "sync applied; native harnesses changed only after explicit x"
}

func (m *configTUIModel) insertRune(r rune) {
	m.text = append(m.text, 0)
	copy(m.text[m.cursor+1:], m.text[m.cursor:])
	m.text[m.cursor] = r
	m.cursor++
}

func lineStart(text []rune, cursor int) int {
	for cursor > 0 && text[cursor-1] != '\n' {
		cursor--
	}
	return cursor
}

func lineEnd(text []rune, cursor int) int {
	for cursor < len(text) && text[cursor] != '\n' {
		cursor++
	}
	return cursor
}

func verticalCursor(text []rune, cursor int, direction int) int {
	start := lineStart(text, cursor)
	column := cursor - start
	if direction < 0 {
		if start == 0 {
			return cursor
		}
		previousEnd := start - 1
		previousStart := lineStart(text, previousEnd)
		if previousStart+column < previousEnd {
			return previousStart + column
		}
		return previousEnd
	}
	end := lineEnd(text, cursor)
	if end == len(text) {
		return cursor
	}
	nextStart := end + 1
	nextEnd := lineEnd(text, nextStart)
	if nextStart+column < nextEnd {
		return nextStart + column
	}
	return nextEnd
}

func (m *configTUIModel) validate() {
	if m.layer == configLayerEffective {
		m.status = "effective view is read-only"
		return
	}
	if _, err := m.doc.validateRaw(m.layer, []byte(string(m.text))); err != nil {
		m.status = "invalid: " + err.Error()
		return
	}
	m.status = "valid YAML and typed config"
}

func (m *configTUIModel) save() {
	if m.layer == configLayerEffective {
		m.status = "effective view is read-only"
		return
	}
	beforeRevision := m.doc.revision(m.layer)
	saved, err := m.doc.saveRaw(m.layer, beforeRevision, []byte(string(m.text)))
	if errors.Is(err, errStaleRevision) {
		m.staleText = string(m.text)
		m.status = "stale revision: reload with h/l or copy unsaved YAML"
		return
	}
	if err != nil {
		m.status = "save failed: " + err.Error()
		return
	}
	m.text = []rune(string(saved.After))
	m.cursor = len(m.text)
	m.diff = saved.Diff
	m.showDiff = true
	m.status = "saved canonical YAML; sync remains a separate action"
}

func (m configTUIModel) View() string {
	if m.doc == nil {
		return "loading config"
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#155EEF"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#46515F"))
	var b strings.Builder
	b.WriteString(title.Render("tackroom config"))
	b.WriteString("  ")
	b.WriteString("shared [h]  local [l]  effective [f]")
	b.WriteString("\n")
	b.WriteString(dim.Render("[e/y] edit  [v] validate  [r] review  [s/ctrl-s] save  [p] preview sync  [x] sync  [q] quit"))
	b.WriteString("\n\n")
	if m.layer == configLayerEffective {
		b.WriteString(dim.Render("effective (read-only)"))
	} else if m.editing {
		b.WriteString(dim.Render("YAML editor"))
	} else {
		b.WriteString(dim.Render("press e to edit YAML"))
	}
	b.WriteString("\n")
	text := string(m.text)
	if m.editing {
		text = text[:byteOffset(text, m.cursor)] + "│" + text[byteOffset(text, m.cursor):]
	}
	lines := strings.Split(text, "\n")
	max := 28
	if m.height > 12 {
		max = m.height - 12
	}
	if m.width > 0 && m.width < 80 && max > 16 {
		max = 16
	}
	cursorLine := strings.Count(string(m.text[:m.cursor]), "\n")
	start := cursorLine - max/2
	if start < 0 {
		start = 0
	}
	if start+max > len(lines) {
		start = len(lines) - max
		if start < 0 {
			start = 0
		}
	}
	for i := start; i < len(lines) && i < start+max; i++ {
		fmt.Fprintf(&b, "%3d  %s\n", i+1, lines[i])
	}
	if start+max < len(lines) {
		b.WriteString(dim.Render(fmt.Sprintf("… %d more lines", len(lines)-(start+max))))
	}
	if m.showDiff && m.diff != "" {
		b.WriteString("\n")
		b.WriteString(title.Render("YAML diff"))
		b.WriteString("\n")
		b.WriteString(m.diff)
	}
	if m.status != "" {
		b.WriteString("\n")
		b.WriteString(dim.Render(m.status))
	}
	return b.String()
}

func byteOffset(text string, runeIndex int) int {
	if runeIndex <= 0 {
		return 0
	}
	return len(string([]rune(text)[:minInt(runeIndex, utf8.RuneCountInString(text))]))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func yamlMarshalConfig(cfg config) ([]byte, error) {
	return yaml.Marshal(cfg)
}
