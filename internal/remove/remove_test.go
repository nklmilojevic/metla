package remove_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nklmilojevic/metla/internal/discover"
	"github.com/nklmilojevic/metla/internal/gittest"
	"github.com/nklmilojevic/metla/internal/remove"
	"github.com/nklmilojevic/metla/internal/scan"
)

func scanAll(t *testing.T, root string) (scan.Report, map[string]scan.Worktree) {
	t.Helper()
	found, err := discover.Walk([]string{root}, discover.Options{MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	r := scan.Run(context.Background(), found, scan.Options{ProcessStart: func(int) string { return "" }})
	ws := map[string]scan.Worktree{}
	for _, w := range r.All() {
		ws[filepath.Base(w.Path)] = w
	}
	return r, ws
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestWorktrees(t *testing.T) {
	root := gittest.TempDir(t)
	app := gittest.New(t, filepath.Join(root, "app"))
	app.WithOrigin()
	merged := app.Worktree(filepath.Join(root, "merged"), "merged")
	merged.CommitFile("a", "a\n", "a")
	merged.Git("push", "-q", "-u", "origin", "merged")
	app.Git("merge", "-q", "--squash", "merged")
	app.Git("commit", "-q", "-m", "squash")
	app.Git("push", "-q")
	dirty := app.Worktree(filepath.Join(root, "dirty"), "dirty")
	dirty.Write("x", "work\n")
	stale := app.Worktree(filepath.Join(root, "stale"), "stale")
	app.Git("worktree", "lock", "--reason", "agent (pid 99999)", stale.Dir)
	lockfile := app.Worktree(filepath.Join(root, "lockfile"), "lockfile")
	lockfile.Write("flake.lock", "{}\n")

	_, ws := scanAll(t, root)
	targets := []scan.Worktree{ws["app"], ws["merged"], ws["dirty"], ws["stale"], ws["lockfile"]}

	dry := remove.Worktrees(context.Background(), targets, remove.Options{DryRun: true})
	wantDry := []string{remove.Skipped, remove.WouldRemove, remove.Skipped, remove.WouldRemove, remove.WouldRemove}
	for i, r := range dry {
		if r.Status != wantDry[i] {
			t.Errorf("dry run %s: %s, want %s", r.Path, r.Status, wantDry[i])
		}
	}
	if !exists(merged.Dir) {
		t.Fatal("dry run removed a worktree")
	}

	res := remove.Worktrees(context.Background(), targets, remove.Options{DeleteBranch: true, IgnoreDirty: scan.DefaultIgnoreDirty})
	want := []string{remove.Skipped, remove.Removed, remove.Skipped, remove.Removed, remove.Removed}
	for i, r := range res {
		if r.Status != want[i] {
			t.Errorf("%s: %s (%s), want %s", r.Path, r.Status, r.Error, want[i])
		}
	}
	if exists(merged.Dir) || exists(stale.Dir) || exists(lockfile.Dir) {
		t.Error("removed worktrees still on disk")
	}
	if !exists(dirty.Dir) || !exists(app.Dir) {
		t.Error("kept worktrees are gone")
	}
	if !res[1].BranchDeleted {
		t.Error("squash-merged branch was not deleted")
	}
	if out := app.Git("branch", "--list", "merged"); out != "" {
		t.Errorf("branch merged still exists: %q", out)
	}

	forced := remove.Worktrees(context.Background(), []scan.Worktree{ws["dirty"]}, remove.Options{Force: true})
	if forced[0].Status != remove.Removed || exists(dirty.Dir) {
		t.Errorf("--force did not remove the dirty worktree: %+v", forced[0])
	}
}

// A worktree that gained changes after the scan is left alone.
func TestChangedSinceScan(t *testing.T) {
	root := gittest.TempDir(t)
	app := gittest.New(t, filepath.Join(root, "app"))
	wt := app.Worktree(filepath.Join(root, "wt"), "wt")
	_, ws := scanAll(t, root)
	if ws["wt"].Verdict != scan.Safe {
		t.Fatalf("verdict %s, want safe", ws["wt"].Verdict)
	}
	wt.Write("new.txt", "fresh work\n")
	res := remove.Worktrees(context.Background(), []scan.Worktree{ws["wt"]}, remove.Options{})
	if res[0].Status != remove.Failed || !exists(filepath.Join(wt.Dir, "new.txt")) {
		t.Errorf("result %+v; the new file must survive", res[0])
	}
}

func TestOrphan(t *testing.T) {
	root := gittest.TempDir(t)
	app := gittest.New(t, filepath.Join(root, "app"))
	wt := app.Worktree(filepath.Join(root, "wt"), "wt")
	if err := os.RemoveAll(app.Dir); err != nil {
		t.Fatal(err)
	}
	r, _ := scanAll(t, root)
	if len(r.Orphans) != 1 {
		t.Fatalf("orphans = %+v", r.Orphans)
	}
	if res := remove.Worktrees(context.Background(), r.Orphans, remove.Options{}); res[0].Status != remove.Skipped {
		t.Errorf("orphan removed without --force: %+v", res[0])
	}
	if res := remove.Worktrees(context.Background(), r.Orphans, remove.Options{Force: true}); res[0].Status != remove.Removed || exists(wt.Dir) {
		t.Errorf("orphan not removed with --force: %+v", res[0])
	}
}

func TestPrune(t *testing.T) {
	root := gittest.TempDir(t)
	app := gittest.New(t, filepath.Join(root, "app"))
	wt := app.Worktree(filepath.Join(root, "wt"), "wt")
	if err := os.RemoveAll(wt.Dir); err != nil {
		t.Fatal(err)
	}
	r, _ := scanAll(t, root)
	if res := remove.Prune(context.Background(), r.Repos, true); len(res) != 1 || len(res[0].Entries) != 1 {
		t.Fatalf("dry prune = %+v", res)
	}
	if out := app.Git("worktree", "list"); len(gittest.Lines(out)) != 2 {
		t.Fatalf("dry run pruned: %q", out)
	}
	remove.Prune(context.Background(), r.Repos, false)
	if out := app.Git("worktree", "list"); len(gittest.Lines(out)) != 1 {
		t.Errorf("entry not pruned: %q", out)
	}
}
