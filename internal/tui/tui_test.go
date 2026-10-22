package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/nklmilojevic/metla/internal/remove"
	"github.com/nklmilojevic/metla/internal/scan"
)

func report() scan.Report {
	return scan.Report{Repos: []scan.Repo{{
		Name: "app", Dir: "/w/app",
		Worktrees: []scan.Worktree{
			{Path: "/w/app-done", Branch: "done", Verdict: scan.Safe, Reasons: []string{"merged"}, AgeDays: 9},
			{Path: "/w/app-open", Branch: "open", Verdict: scan.Review, Reasons: []string{"not merged"}, AgeDays: 5},
			{Path: "/w/app-wip", Branch: "wip", Verdict: scan.Unsafe, Reasons: []string{"1 uncommitted change"}, AgeDays: 1},
			{Path: "/w/app-held", Branch: "held", Verdict: scan.Keep, Reasons: []string{"locked"}},
		},
		Prunable: []scan.Prunable{{Path: "/w/gone"}},
	}}}
}

type fake struct {
	loads   int
	removed [][]scan.Worktree
	force   []bool
	pruned  int
}

func newModel(f *fake) *Model {
	m := New(Deps{
		Title: "~/w",
		Load:  func() (scan.Report, error) { f.loads++; return report(), nil },
		Remove: func(ws []scan.Worktree, force, _ bool, progress func(done, total int)) []remove.Result {
			for i := range ws {
				progress(i+1, len(ws))
			}
			f.removed = append(f.removed, ws)
			f.force = append(f.force, force)
			return []remove.Result{{Status: remove.Removed}}
		},
		Prune: func([]scan.Repo) []remove.PruneResult { f.pruned++; return nil },
	}, true)
	m.Update(m.Init()())
	return m
}

// press sends keys and runs any command they return, like the runtime would.
func press(m *Model, keys ...string) {
	for _, k := range keys {
		msg := tea.KeyPressMsg{Text: k, Code: []rune(k)[0]}
		switch k {
		case "space":
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		}
		_, cmd := m.Update(msg)
		for cmd != nil {
			out := cmd()
			if out == nil {
				break
			}
			if _, ok := out.(tea.QuitMsg); ok {
				break
			}
			_, cmd = m.Update(out)
		}
	}
}

func paths(ws []scan.Worktree) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.Path)
	}
	return out
}

func TestSelectAndRemove(t *testing.T) {
	f := &fake{}
	m := newModel(f)
	if m.rows[m.cursor].wt == nil {
		t.Fatal("cursor starts on a header")
	}
	// Tick the review worktree by hand and every safe one with a.
	press(m, "down", "space", "a")
	// Unsafe cannot be ticked without force; kept never can.
	press(m, "down", "space", "down", "space")
	if got := paths(m.selected()); len(got) != 2 {
		t.Fatalf("selected = %q, want done and open", got)
	}
	if !strings.Contains(m.render(), "2 selected") {
		t.Errorf("footer:\n%s", m.render())
	}
	press(m, "d", "n")
	if len(f.removed) != 0 {
		t.Fatal("removed without confirmation")
	}
	press(m, "d", "y")
	if len(f.removed) != 1 || len(f.removed[0]) != 2 || f.force[0] {
		t.Fatalf("removed = %+v force %v", f.removed, f.force)
	}
	if f.loads != 2 || len(m.picked) != 0 || !strings.Contains(m.status, "removed 1") {
		t.Errorf("after removal: loads %d, picked %v, status %q", f.loads, m.picked, m.status)
	}
}

func TestForce(t *testing.T) {
	f := &fake{}
	m := newModel(f)
	press(m, "down", "down", "space")
	if len(m.selected()) != 0 || !strings.Contains(m.status, "press f") {
		t.Fatalf("unsafe ticked without force; status %q", m.status)
	}
	press(m, "f", "space")
	if got := paths(m.selected()); len(got) != 1 || got[0] != "/w/app-wip" {
		t.Fatalf("selected = %q", got)
	}
	press(m, "f")
	if len(m.selected()) != 0 {
		t.Error("turning force off keeps unsafe ticks")
	}
}

func TestPrune(t *testing.T) {
	f := &fake{}
	m := newModel(f)
	if !strings.Contains(m.render(), "1 stale entry") {
		t.Errorf("header lacks the stale count:\n%s", m.render())
	}
	press(m, "p", "y")
	if f.pruned != 1 {
		t.Error("prune not run")
	}
}
