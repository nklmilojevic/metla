package discover_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nklmilojevic/metla/internal/discover"
	"github.com/nklmilojevic/metla/internal/gittest"
)

func TestWalk(t *testing.T) {
	root := gittest.TempDir(t)
	org := filepath.Join(root, "org")
	app := gittest.New(t, filepath.Join(org, "app"))
	// A sibling worktree is found as the same repository, not a second one.
	app.Worktree(filepath.Join(org, "app-feature"), "feature")
	// A worktree inside the repo is not walked into at all.
	app.Worktree(filepath.Join(app.Dir, ".claude", "worktrees", "agent"), "agent")

	lib := gittest.New(t, filepath.Join(root, "lib"))
	bare := filepath.Join(root, "mirror.git")
	lib.Git("clone", "-q", "--bare", lib.Dir, bare)

	gittest.New(t, filepath.Join(root, "node_modules", "dep"))
	gittest.New(t, filepath.Join(root, ".hidden", "repo"))
	gittest.New(t, filepath.Join(root, "a", "b", "c", "deep"))
	if err := os.MkdirAll(filepath.Join(root, "husk", ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}

	other := gittest.New(t, filepath.Join(root, "other"))
	orphan := other.Worktree(filepath.Join(root, "other-wt"), "x")
	if err := os.RemoveAll(other.Dir); err != nil {
		t.Fatal(err)
	}

	res, err := discover.Walk([]string{root}, discover.Options{MaxDepth: 3, Skip: discover.DefaultSkip})
	if err != nil {
		t.Fatal(err)
	}
	dirs := make([]string, 0, len(res.Repos))
	for _, r := range res.Repos {
		dirs = append(dirs, r.Dir)
	}
	want := []string{filepath.Join(root, "lib"), bare, filepath.Join(org, "app")}
	if !sameSet(dirs, want) {
		t.Errorf("repos = %q, want %q", dirs, want)
	}
	if len(res.Orphans) != 1 || res.Orphans[0].Path != orphan.Dir {
		t.Errorf("orphans = %+v, want %s", res.Orphans, orphan.Dir)
	}
}

func TestRootIsAWorktree(t *testing.T) {
	root := gittest.TempDir(t)
	app := gittest.New(t, filepath.Join(root, "app"))
	wt := app.Worktree(filepath.Join(root, "wt"), "feature")
	res, err := discover.Walk([]string{wt.Dir}, discover.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Repos) != 1 || res.Repos[0].Dir != app.Dir || res.Repos[0].CommonDir != filepath.Join(app.Dir, ".git") {
		t.Errorf("repos = %+v", res.Repos)
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
