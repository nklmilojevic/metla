package scan_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nklmilojevic/metla/internal/discover"
	"github.com/nklmilojevic/metla/internal/gittest"
	"github.com/nklmilojevic/metla/internal/scan"
)

// fixture is a repository with an origin, plus helpers to add worktrees.
type fixture struct {
	t    *testing.T
	root string
	main *gittest.Repo
}

func newFixture(t *testing.T) *fixture {
	root := gittest.TempDir(t)
	main := gittest.New(t, filepath.Join(root, "app"))
	main.WithOrigin()
	return &fixture{t: t, root: root, main: main}
}

func (f *fixture) wt(name string) *gittest.Repo {
	return f.main.Worktree(filepath.Join(f.root, "app-"+name), name)
}

// push publishes main, standing in for a merge on GitHub.
func (f *fixture) push() {
	f.main.Git("push", "-q", "origin", "main")
}

func (f *fixture) scan(opts scan.Options) map[string]scan.Worktree {
	f.t.Helper()
	found, err := discover.Walk([]string{f.root}, discover.Options{MaxDepth: 2})
	if err != nil {
		f.t.Fatal(err)
	}
	if opts.ProcessStart == nil {
		opts.ProcessStart = func(int) string { return "" }
	}
	report := scan.Run(context.Background(), found, opts)
	out := map[string]scan.Worktree{}
	for _, w := range report.All() {
		out[filepath.Base(w.Path)] = w
	}
	return out
}

func expect(t *testing.T, w scan.Worktree, v scan.Verdict, reason string) {
	t.Helper()
	if w.Verdict != v {
		t.Errorf("%s: verdict %s, want %s (reasons %q)", w.Path, w.Verdict, v, w.Reasons)
	}
	if reason != "" && !slices.Contains(w.Reasons, reason) {
		t.Errorf("%s: reasons %q, want %q among them", w.Path, w.Reasons, reason)
	}
}

func TestVerdicts(t *testing.T) {
	f := newFixture(t)

	// Merged with a merge commit, as GitHub's merge button does.
	merged := f.wt("merged")
	merged.CommitFile("a.txt", "a\n", "a")
	merged.Git("push", "-q", "-u", "origin", "merged")
	f.main.Git("merge", "-q", "--no-ff", "-m", "merge a", "merged")

	// Squash-merged: main gets one commit with the branch's whole diff.
	squashed := f.wt("squashed")
	squashed.CommitFile("b.txt", "b1\n", "b1")
	squashed.CommitFile("b.txt", "b1\nb2\n", "b2")
	squashed.CommitFile("c.txt", "c\n", "c")
	squashed.Git("push", "-q", "-u", "origin", "squashed")
	f.main.Git("merge", "-q", "--squash", "squashed")
	f.main.Git("commit", "-q", "-m", "squash b")

	// Rebase-merged: each commit replayed onto main with a new hash.
	rebased := f.wt("rebased")
	rebased.CommitFile("d.txt", "d\n", "d")
	rebased.CommitFile("e.txt", "e\n", "e")
	rebased.Git("push", "-q", "-u", "origin", "rebased")
	f.main.CommitFile("unrelated.txt", "x\n", "unrelated work")
	f.main.Git("cherry-pick", "rebased~1", "rebased")
	f.push()

	pushed := f.wt("pushed")
	pushed.CommitFile("f.txt", "f\n", "f")
	pushed.Git("push", "-q", "-u", "origin", "pushed")

	local := f.wt("local")
	local.CommitFile("g.txt", "g\n", "g")

	dirty := f.wt("dirty")
	dirty.Write("README", "changed\n")

	lockOnly := f.wt("lockonly")
	lockOnly.Write("go.sum", "churn\n")

	f.wt("empty")

	detached := f.main.At(filepath.Join(f.root, "app-detached"))
	f.main.Git("worktree", "add", "-q", "--detach", detached.Dir)
	detached.CommitFile("h.txt", "h\n", "h")

	f.main.Git("worktree", "add", "-q", filepath.Join(f.root, "app-develop"), "-b", "develop")

	ws := f.scan(scan.Options{})
	expect(t, ws["app"], scan.Keep, "main worktree")
	expect(t, ws["app-merged"], scan.Safe, "merged")
	expect(t, ws["app-squashed"], scan.Safe, "squash-merged")
	expect(t, ws["app-rebased"], scan.Safe, "rebase-merged")
	expect(t, ws["app-pushed"], scan.Review, "not merged")
	expect(t, ws["app-local"], scan.Unsafe, "1 unpushed commit")
	expect(t, ws["app-dirty"], scan.Unsafe, "1 uncommitted change")
	expect(t, ws["app-lockonly"], scan.Safe, "no commits of its own")
	expect(t, ws["app-empty"], scan.Safe, "no commits of its own")
	expect(t, ws["app-detached"], scan.Unsafe, "1 commit only on this detached HEAD")
	expect(t, ws["app-develop"], scan.Keep, "protected branch develop")
}

func TestForkBase(t *testing.T) {
	f := newFixture(t)
	f.main.Git("branch", "staging")
	f.main.Git("push", "-q", "origin", "staging")

	w := f.main.At(filepath.Join(f.root, "app-fix"))
	f.main.Git("worktree", "add", "-q", "-b", "fix", w.Dir, "origin/staging")
	w.CommitFile("fix.txt", "fix\n", "fix")
	w.Git("push", "-q", "origin", "fix")
	// The fix lands on staging, not main.
	f.main.Git("push", "-q", "origin", "fix:staging")

	ws := f.scan(scan.Options{})
	expect(t, ws["app-fix"], scan.Safe, "merged")
}

func TestLocks(t *testing.T) {
	f := newFixture(t)
	held := f.wt("held")
	f.main.Git("worktree", "lock", "--reason", "claude agent x (pid 42 start Tue Oct 13 15:23:56 2026)", held.Dir)
	stale := f.wt("stale")
	f.main.Git("worktree", "lock", "--reason", "claude agent y (pid 43 start Tue Oct 13 15:23:56 2026)", stale.Dir)
	reused := f.wt("reused")
	f.main.Git("worktree", "lock", "--reason", "claude agent z (pid 44 start Tue Oct 13 15:23:56 2026)", reused.Dir)
	manual := f.wt("manual")
	f.main.Git("worktree", "lock", manual.Dir)

	ws := f.scan(scan.Options{ProcessStart: func(pid int) string {
		switch pid {
		case 42:
			return "Tue Oct 13 15:23:56 2026"
		case 44:
			return "Wed Oct 21 09:00:00 2026"
		}
		return ""
	}})
	expect(t, ws["app-held"], scan.Keep, "locked: claude agent x (pid 42 start Tue Oct 13 15:23:56 2026)")
	expect(t, ws["app-stale"], scan.Safe, "stale lock (its process is gone)")
	expect(t, ws["app-reused"], scan.Safe, "stale lock (its process is gone)")
	expect(t, ws["app-manual"], scan.Keep, "locked")
}

func TestBusy(t *testing.T) {
	f := newFixture(t)
	w := f.wt("busy")
	ws := f.scan(scan.Options{Busy: func(path string) []scan.Proc {
		if path == w.Dir {
			return []scan.Proc{{PID: 7, Command: "claude"}}
		}
		return nil
	}})
	expect(t, ws["app-busy"], scan.Unsafe, "in use by claude (pid 7)")
}

func TestPR(t *testing.T) {
	f := newFixture(t)
	merged := f.wt("prmerged")
	merged.CommitFile("a.txt", "a\n", "a")
	merged.Git("push", "-q", "-u", "origin", "prmerged")
	later := f.wt("prlater")
	later.CommitFile("b.txt", "b\n", "b")
	later.Git("push", "-q", "-u", "origin", "prlater")
	mergedAt := later.Head()
	later.CommitFile("b.txt", "b2\n", "after merge")
	later.Git("push", "-q")
	closed := f.wt("prclosed")
	closed.CommitFile("c.txt", "c\n", "c")
	closed.Git("push", "-q", "-u", "origin", "prclosed")

	prs := map[string]*scan.PR{
		"prmerged": {Number: 1, State: "MERGED", HeadOid: merged.Head()},
		"prlater":  {Number: 2, State: "MERGED", HeadOid: mergedAt},
		"prclosed": {Number: 3, State: "CLOSED", HeadOid: closed.Head()},
	}
	ws := f.scan(scan.Options{PRs: func(_ context.Context, _, branch string) *scan.PR { return prs[branch] }})
	expect(t, ws["app-prmerged"], scan.Safe, "PR #1 merged")
	expect(t, ws["app-prlater"], scan.Review, "PR #2 merged")
	expect(t, ws["app-prclosed"], scan.Review, "PR #3 closed")
}

func TestPrunableAndOrphans(t *testing.T) {
	f := newFixture(t)
	gone := f.wt("gone")
	if err := os.RemoveAll(gone.Dir); err != nil {
		t.Fatal(err)
	}

	// An orphan: a worktree whose repository was deleted.
	other := gittest.New(t, filepath.Join(f.root, "other"))
	orphan := other.Worktree(filepath.Join(f.root, "other-wt"), "feature")
	if err := os.RemoveAll(other.Dir); err != nil {
		t.Fatal(err)
	}

	found, err := discover.Walk([]string{f.root}, discover.Options{MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	report := scan.Run(context.Background(), found, scan.Options{})
	var prunable []string
	for _, r := range report.Repos {
		for _, p := range r.Prunable {
			prunable = append(prunable, p.Path)
		}
	}
	if !slices.Equal(prunable, []string{gone.Dir}) {
		t.Errorf("prunable = %q, want %q", prunable, gone.Dir)
	}
	if len(report.Orphans) != 1 || report.Orphans[0].Path != orphan.Dir {
		t.Fatalf("orphans = %+v", report.Orphans)
	}
	expect(t, report.Orphans[0], scan.Unsafe, "repository is gone")
}

func TestRepoWithoutLinkedWorktreesIsLeftOut(t *testing.T) {
	root := gittest.TempDir(t)
	gittest.New(t, filepath.Join(root, "solo"))
	found, err := discover.Walk([]string{root}, discover.Options{MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if r := scan.Run(context.Background(), found, scan.Options{}); len(r.Repos) != 0 {
		t.Errorf("repos = %+v, want none", r.Repos)
	}
}
