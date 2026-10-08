package app

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type reviewAction int

const (
	actionShare reviewAction = iota
	actionKeep
	actionSkip

	numReviewActions = int(actionSkip) + 1
)

func (a reviewAction) String() string {
	switch a {
	case actionShare:
		return "share"
	case actionKeep:
		return "keep"
	default:
		return "skip"
	}
}

// reviewRow is one reviewable item plus the user's pending decision.
// sourceIdx selects which harness copy becomes canonical when sharing an
// item whose content differs between sources.
type reviewRow struct {
	item      detectedItem
	action    reviewAction
	sourceIdx int
}

// reviewDecision is the outcome for a single item after the review screen.
type reviewDecision struct {
	Surface string
	Name    string
	Action  reviewAction
	Source  detectedSource
}

type reviewModel struct {
	harnesses  []string
	rows       []reviewRow
	resolved   int
	cursor     int
	confirming bool
	applied    bool
	aborted    bool
}

var (
	reviewHeaderStyle = lipgloss.NewStyle().Bold(true)
	reviewDimStyle    = lipgloss.NewStyle().Faint(true)
	reviewCursorStyle = lipgloss.NewStyle().Reverse(true)
	reviewShareStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	reviewSkipStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

func newReviewModel(detection *detectionResult) reviewModel {
	rows := make([]reviewRow, 0, len(detection.Items))
	for _, item := range detection.Items {
		rows = append(rows, reviewRow{item: item, action: actionShare})
	}
	harnesses := append([]string(nil), detection.Harnesses...)
	sort.Strings(harnesses)
	return reviewModel{
		harnesses: harnesses,
		rows:      rows,
		resolved:  detection.AutoResolved,
	}
}

func (m reviewModel) Init() tea.Cmd { return nil }

func (m reviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if m.confirming {
		switch keyMsg.String() {
		case "enter", "y":
			m.applied = true
			return m, tea.Quit
		case "ctrl+c":
			m.aborted = true
			return m, tea.Quit
		case "esc", "n", "q":
			m.confirming = false
		}
		return m, nil
	}
	switch keyMsg.String() {
	case "ctrl+c", "q", "esc":
		m.aborted = true
		return m, tea.Quit
	case "enter":
		m.confirming = true
		return m, nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case "space":
		if len(m.rows) > 0 {
			m.rows[m.cursor].action = reviewAction((int(m.rows[m.cursor].action) + 1) % numReviewActions)
		}
	case "a":
		for i := range m.rows {
			m.rows[i].action = actionShare
		}
	case "s":
		for i := range m.rows {
			m.rows[i].action = actionSkip
		}
	case "left", "h":
		m.cycleSource(-1)
	case "right", "l":
		m.cycleSource(1)
	}
	return m, nil
}

func (m *reviewModel) cycleSource(delta int) {
	if len(m.rows) == 0 {
		return
	}
	row := &m.rows[m.cursor]
	n := len(row.item.Sources)
	if n < 2 {
		return
	}
	row.sourceIdx = (row.sourceIdx + delta + n) % n
}

func (m reviewModel) View() tea.View {
	return tea.NewView(m.render())
}

func (m reviewModel) render() string {
	var b strings.Builder
	title := fmt.Sprintf("tackroom setup — review %d item(s)", len(m.rows))
	if m.resolved > 0 {
		title += reviewDimStyle.Render(fmt.Sprintf("  (%d identical, shared automatically)", m.resolved))
	}
	b.WriteString(reviewHeaderStyle.Render(title) + "\n\n")

	if len(m.rows) == 0 {
		b.WriteString(reviewDimStyle.Render("nothing to review") + "\n")
	}

	nameWidth := 4
	for _, row := range m.rows {
		if len(row.item.Name) > nameWidth {
			nameWidth = len(row.item.Name)
		}
	}

	for i, row := range m.rows {
		line := fmt.Sprintf("  %-7s %-*s %s %s", row.item.Surface, nameWidth, row.item.Name, m.presence(row), m.actionCell(row))
		if i == m.cursor {
			line = reviewCursorStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}

	if m.confirming {
		share, keep, skip := m.tally()
		summary := fmt.Sprintf("apply %d share, %d keep, %d skip", share+m.resolved, keep, skip)
		b.WriteString("\n" + reviewHeaderStyle.Render(summary) + reviewDimStyle.Render("  — enter/y confirm, esc back") + "\n")
	} else {
		b.WriteString("\n" + reviewDimStyle.Render("↑↓ move  space cycle action  ←→ pick source  a share-all  s skip-all  enter apply  q abort") + "\n")
	}
	return b.String()
}

func (m reviewModel) tally() (share, keep, skip int) {
	for _, row := range m.rows {
		switch row.action {
		case actionShare:
			share++
		case actionKeep:
			keep++
		case actionSkip:
			skip++
		}
	}
	return share, keep, skip
}

// presence renders one glyph column per detected harness plus a differ marker.
func (m reviewModel) presence(row reviewRow) string {
	present := make(map[string]bool, len(row.item.Sources))
	for _, src := range row.item.Sources {
		present[src.Harness] = true
	}
	parts := make([]string, 0, len(m.harnesses))
	for _, h := range m.harnesses {
		if present[h] {
			parts = append(parts, h+"✓")
		} else {
			parts = append(parts, reviewDimStyle.Render(h+"·"))
		}
	}
	out := strings.Join(parts, " ")
	if len(row.item.Sources) > 1 && !row.item.Identical {
		out += " (differ)"
	}
	return out
}

func (m reviewModel) actionCell(row reviewRow) string {
	cell := "[" + row.action.String() + "]"
	switch row.action {
	case actionShare:
		cell = reviewShareStyle.Render(cell)
	case actionSkip:
		cell = reviewSkipStyle.Render(cell)
	}
	if row.action == actionShare && len(row.item.Sources) > 1 && !row.item.Identical {
		cell += " from " + row.item.Sources[row.sourceIdx].Harness
	}
	return cell
}

// decisions returns the review outcome. Auto-resolved items are appended as
// share decisions using their first source; nil is returned on abort.
func (m reviewModel) decisions(detection *detectionResult) []reviewDecision {
	if m.aborted {
		return nil
	}
	out := make([]reviewDecision, 0, len(m.rows)+len(detection.Resolved))
	for _, row := range m.rows {
		out = append(out, reviewDecision{
			Surface: row.item.Surface,
			Name:    row.item.Name,
			Action:  row.action,
			Source:  row.item.Sources[row.sourceIdx],
		})
	}
	for _, item := range detection.Resolved {
		out = append(out, reviewDecision{
			Surface: item.Surface,
			Name:    item.Name,
			Action:  actionShare,
			Source:  item.Sources[0],
		})
	}
	return out
}
