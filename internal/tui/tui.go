// Package tui is the interactive sweep: a list of worktrees grouped by
// repository, where the user ticks the ones to remove and confirms.
package tui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/nklmilojevic/metla/internal/remove"
	"github.com/nklmilojevic/metla/internal/scan"
	"github.com/nklmilojevic/metla/internal/style"
)

// Deps is what the screen needs from the rest of metla.
type Deps struct {
	Title  string
	Load   func() (scan.Report, error)
	Remove func(ws []scan.Worktree, force, deleteBranch bool) []remove.Result
	Prune  func(repos []scan.Repo) []remove.PruneResult
	Tilde  func(string) string
}

type mode int

const (
	modeLoading mode = iota
	modeList
	modeConfirm
	modeConfirmPrune
	modeWorking
)

const help = "↑/↓ move · space tick · a tick safe · x clear · f force · b delete branches · " +
	"d remove · p prune · r rescan · q quit"

// row is a line in the list: a repository header or a worktree.
type row struct {
	header string
	wt     *scan.Worktree
}

// Model is the Bubble Tea model.
type Model struct {
	deps   Deps
	st     style.Styles
	mode   mode
	report scan.Report
	rows   []row
	picked map[string]bool // by worktree path
	cursor int             // index into rows, always on a worktree row
	offset int
	width  int
	height int

	force        bool
	deleteBranch bool
	status       string
}

// New builds the model; Init starts the first scan.
func New(d Deps, dark bool) *Model {
	if d.Tilde == nil {
		d.Tilde = func(s string) string { return s }
	}
	return &Model{deps: d, st: style.New(dark), picked: map[string]bool{}, width: 100, height: 30}
}

type loadedMsg struct {
	report scan.Report
	err    error
}

type removedMsg struct{ results []remove.Result }

type prunedMsg struct{ results []remove.PruneResult }

func (m *Model) load() tea.Cmd {
	return func() tea.Msg {
		r, err := m.deps.Load()
		return loadedMsg{r, err}
	}
}

// Init starts the scan.
func (m *Model) Init() tea.Cmd { return m.load() }

// Update handles a message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.st = style.New(msg.IsDark())
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scroll()
	case loadedMsg:
		m.mode = modeList
		if msg.err != nil {
			m.status = m.st.Err.Render(msg.err.Error())
			return m, nil
		}
		m.setReport(msg.report)
	case removedMsg:
		m.status = removeSummary(msg.results)
		m.picked = map[string]bool{}
		m.mode = modeLoading
		return m, m.load()
	case prunedMsg:
		n := 0
		for _, r := range msg.results {
			n += len(r.Entries)
		}
		m.status = fmt.Sprintf("pruned %d stale %s", n, style.Plural(n, "entry", "entries"))
		m.mode = modeLoading
		return m, m.load()
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeConfirm:
		if k == "y" {
			ws := m.selected()
			m.mode = modeWorking
			return m, func() tea.Msg { return removedMsg{m.deps.Remove(ws, m.force, m.deleteBranch)} }
		}
		m.mode = modeList
		return m, nil
	case modeConfirmPrune:
		if k == "y" {
			repos := m.report.Repos
			m.mode = modeWorking
			return m, func() tea.Msg { return prunedMsg{m.deps.Prune(repos)} }
		}
		m.mode = modeList
		return m, nil
	case modeLoading, modeWorking:
		if k == "q" {
			return m, tea.Quit
		}
		return m, nil
	}

	switch k {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-max(1, m.listHeight()-1))
	case "pgdown":
		m.move(max(1, m.listHeight()-1))
	case "home", "g":
		m.cursor = 0
		m.move(1)
		m.move(-1)
	case "end", "G":
		m.cursor = len(m.rows) - 1
		m.move(-1)
		m.move(1)
	case "space", " ":
		m.toggle()
	case "a":
		for _, w := range m.worktrees() {
			if w.Verdict == scan.Safe {
				m.picked[w.Path] = true
			}
		}
	case "x":
		m.picked = map[string]bool{}
	case "f":
		m.force = !m.force
		if !m.force {
			for _, w := range m.worktrees() {
				if w.Verdict == scan.Unsafe {
					delete(m.picked, w.Path)
				}
			}
		}
	case "b":
		m.deleteBranch = !m.deleteBranch
	case "d", "enter":
		if len(m.selected()) > 0 {
			m.mode = modeConfirm
		} else {
			m.status = "nothing selected: space ticks a worktree, a ticks every safe one"
		}
	case "p":
		if n := prunable(m.report); n > 0 {
			m.mode = modeConfirmPrune
		}
	case "r":
		m.mode = modeLoading
		return m, m.load()
	}
	return m, nil
}

func (m *Model) setReport(r scan.Report) {
	m.report = r
	m.rows = nil
	repos := append([]scan.Repo{}, r.Repos...)
	sort.Slice(repos, func(i, j int) bool { return repos[i].Dir < repos[j].Dir })
	for _, repo := range repos {
		if len(repo.Worktrees) == 0 {
			continue
		}
		m.rows = append(m.rows, row{header: m.deps.Tilde(repo.Dir)})
		ws := append([]scan.Worktree{}, repo.Worktrees...)
		scan.Sort(ws)
		for i := range ws {
			m.rows = append(m.rows, row{wt: &ws[i]})
		}
	}
	if len(r.Orphans) > 0 {
		m.rows = append(m.rows, row{header: "orphaned worktrees (repository gone)"})
		for i := range r.Orphans {
			m.rows = append(m.rows, row{wt: &r.Orphans[i]})
		}
	}
	live := map[string]bool{}
	for _, w := range m.worktrees() {
		live[w.Path] = true
	}
	for p := range m.picked {
		if !live[p] {
			delete(m.picked, p)
		}
	}
	m.cursor = min(m.cursor, max(0, len(m.rows)-1))
	if len(m.rows) > 0 && m.rows[m.cursor].wt == nil {
		m.move(1)
	}
	m.scroll()
}

func (m *Model) worktrees() []*scan.Worktree {
	var out []*scan.Worktree
	for _, r := range m.rows {
		if r.wt != nil {
			out = append(out, r.wt)
		}
	}
	return out
}

func (m *Model) selected() []scan.Worktree {
	var out []scan.Worktree
	for _, w := range m.worktrees() {
		if m.picked[w.Path] && w.Removable(m.force) {
			out = append(out, *w)
		}
	}
	return out
}

func (m *Model) toggle() {
	if len(m.rows) == 0 || m.rows[m.cursor].wt == nil {
		return
	}
	w := m.rows[m.cursor].wt
	switch {
	case m.picked[w.Path]:
		delete(m.picked, w.Path)
	case w.Removable(m.force):
		m.picked[w.Path] = true
	case w.Verdict == scan.Unsafe:
		m.status = "unsafe: press f to allow removing worktrees with work in them"
	default:
		m.status = "kept: " + strings.Join(w.Reasons, ", ")
	}
}

// move steps the cursor over worktree rows, skipping headers.
func (m *Model) move(delta int) {
	if len(m.rows) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for n := abs(delta); n > 0; n-- {
		next := m.cursor + step
		for next >= 0 && next < len(m.rows) && m.rows[next].wt == nil {
			next += step
		}
		if next < 0 || next >= len(m.rows) {
			break
		}
		m.cursor = next
	}
	m.scroll()
}

func (m *Model) listHeight() int { return max(3, m.height-5) }

func (m *Model) scroll() {
	h := m.listHeight()
	// Keep the repository header above the cursor in view.
	top := m.cursor
	if top > 0 && m.rows[top-1].wt == nil {
		top--
	}
	if top < m.offset {
		m.offset = top
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(0, min(m.offset, max(0, len(m.rows)-h)))
}

// View renders the screen.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m *Model) render() string {
	var b strings.Builder
	b.WriteString(m.st.Title.Render("metla") + "  " + m.st.Muted.Render(m.deps.Title))
	switch m.mode {
	case modeLoading:
		b.WriteString("\n\n  scanning…\n")
		return b.String()
	case modeWorking:
		b.WriteString("\n\n  working…\n")
		return b.String()
	}
	ws := m.worktrees()
	b.WriteString(m.st.Muted.Render(fmt.Sprintf("  · %d %s", len(ws), style.Plural(len(ws), "worktree", "worktrees"))))
	if n := prunable(m.report); n > 0 {
		b.WriteString(m.st.Muted.Render(fmt.Sprintf(" · %d stale %s (p)", n, style.Plural(n, "entry", "entries"))))
	}
	b.WriteString("\n\n")

	if len(m.rows) == 0 {
		b.WriteString("  No linked worktrees found.\n")
	}
	end := min(len(m.rows), m.offset+m.listHeight())
	for i := m.offset; i < end; i++ {
		b.WriteString(m.line(i) + "\n")
	}
	for i := end - m.offset; i < m.listHeight(); i++ {
		b.WriteString("\n")
	}

	sel := m.selected()
	b.WriteString(m.footer(len(sel)) + "\n")
	switch m.mode {
	case modeConfirm:
		b.WriteString(m.st.Warn.Render(fmt.Sprintf("Remove %d %s%s? y to confirm, any other key cancels",
			len(sel), style.Plural(len(sel), "worktree", "worktrees"), m.branchNote())))
	case modeConfirmPrune:
		b.WriteString(m.st.Warn.Render("Run git worktree prune where stale entries exist? y to confirm"))
	default:
		b.WriteString(m.st.Muted.Render(help))
	}
	return b.String()
}

func (m *Model) branchNote() string {
	if m.deleteBranch {
		return " and their merged branches"
	}
	return ""
}

func (m *Model) footer(selected int) string {
	parts := []string{fmt.Sprintf("%d selected", selected)}
	if m.force {
		parts = append(parts, m.st.Err.Render("force on"))
	}
	if m.deleteBranch {
		parts = append(parts, m.st.Warn.Render("deleting merged branches"))
	}
	line := strings.Join(parts, " · ")
	if m.status != "" {
		line += "  " + m.st.Muted.Render(m.status)
	}
	return line
}

func (m *Model) line(i int) string {
	r := m.rows[i]
	if r.wt == nil {
		return " " + m.st.Accent.Render(r.header)
	}
	w := r.wt
	cursor := "  "
	if i == m.cursor {
		cursor = m.st.Title.Render("▸ ")
	}
	box := "[ ]"
	switch {
	case m.picked[w.Path]:
		box = m.st.Title.Render("[x]")
	case !w.Removable(m.force):
		box = m.st.Muted.Render(" · ")
	}
	name := w.Branch
	if name == "" {
		name = "(detached)"
	}
	verdict := m.st.Verdict(w.Verdict) + strings.Repeat(" ", 7-len(w.Verdict))
	ageCol := fmt.Sprintf("%5s", style.Age(w.AgeDays))
	left := fmt.Sprintf("%s%s %s %s  %s  ", cursor, box, verdict, ageCol, truncate(name, 32))
	rest := m.st.Muted.Render(truncate(strings.Join(w.Reasons, ", ")+"  "+m.deps.Tilde(w.Path),
		max(10, m.width-lipgloss.Width(left)-1)))
	return left + strings.Repeat(" ", max(0, 32-lipgloss.Width(truncate(name, 32)))) + rest
}

func removeSummary(results []remove.Result) string {
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Status]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "removed %d", counts[remove.Removed])
	if n := counts[remove.Failed]; n > 0 {
		for _, r := range results {
			if r.Status == remove.Failed {
				fmt.Fprintf(&b, ", %d failed (%s: %s)", n, r.Path, r.Error)
				break
			}
		}
	}
	if n := counts[remove.Skipped]; n > 0 {
		fmt.Fprintf(&b, ", %d skipped", n)
	}
	return b.String()
}

func prunable(r scan.Report) int {
	n := 0
	for _, repo := range r.Repos {
		n += len(repo.Prunable)
	}
	return n
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Run shows the screen until the user quits.
func Run(ctx context.Context, d Deps) error {
	m := New(d, lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	return err
}
