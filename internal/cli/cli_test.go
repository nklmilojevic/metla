package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nklmilojevic/metla/internal/gittest"
	"github.com/nklmilojevic/metla/internal/scan"
)

type result struct {
	out, err string
	code     int
}

func run(t *testing.T, tty bool, stdin string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	a := &app{
		stdout: &out, stderr: &errb, stdin: strings.NewReader(stdin),
		isTTY:  func(io.Writer) bool { return tty },
		busyFn: func(string) []scan.Proc { return nil },
	}
	root := newRoot(a, "test")
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	if err != nil {
		errb.WriteString(err.Error())
	}
	return result{out.String(), errb.String(), Code(err)}
}

// setup builds ~/dev-like roots: one repo with a merged, an unmerged and a
// dirty worktree, plus a worktree whose directory is gone.
func setup(t *testing.T) (root string, app *gittest.Repo) {
	root = gittest.TempDir(t)
	t.Setenv("METLA_CONFIG", filepath.Join(root, "none.toml"))
	t.Setenv("METLA_ROOTS", root)
	app = gittest.New(t, filepath.Join(root, "org", "app"))
	app.WithOrigin()
	done := app.Worktree(filepath.Join(root, "org", "app-done"), "done")
	done.CommitFile("a", "a\n", "a")
	done.Git("push", "-q", "-u", "origin", "done")
	app.Git("merge", "-q", "done")
	app.Git("push", "-q")
	open := app.Worktree(filepath.Join(root, "org", "app-open"), "open")
	open.CommitFile("b", "b\n", "b")
	open.Git("push", "-q", "-u", "origin", "open")
	wip := app.Worktree(filepath.Join(root, "org", "app-wip"), "wip")
	wip.Write("c", "c\n")
	gone := app.Worktree(filepath.Join(root, "org", "app-gone"), "gone")
	if err := os.RemoveAll(gone.Dir); err != nil {
		t.Fatal(err)
	}
	return root, app
}

func TestScanJSON(t *testing.T) {
	setup(t)
	r := run(t, false, "", "scan", "--json")
	if r.code != ExitOK {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	var report scan.Report
	if err := json.Unmarshal([]byte(r.out), &report); err != nil {
		t.Fatal(err)
	}
	got := map[string]scan.Verdict{}
	for _, w := range report.All() {
		got[filepath.Base(w.Path)] = w.Verdict
	}
	want := map[string]scan.Verdict{"app-done": scan.Safe, "app-open": scan.Review, "app-wip": scan.Unsafe}
	if len(got) != len(want) {
		t.Errorf("worktrees = %v, want %v (main worktree left out)", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
	if len(report.Repos) != 1 || len(report.Repos[0].Prunable) != 1 {
		t.Errorf("prunable = %+v", report.Repos)
	}
}

func TestScanTable(t *testing.T) {
	setup(t)
	r := run(t, false, "", "scan")
	for _, want := range []string{"VERDICT", "app-done", "3 worktrees in 1 repository: 1 unsafe, 1 safe, 1 review", "1 stale entry"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("output lacks %q:\n%s", want, r.out)
		}
	}
	q := run(t, false, "", "scan", "-q", "--verdict", "safe")
	if strings.TrimSpace(q.out) == "" || strings.Count(q.out, "\n") != 1 || !strings.HasSuffix(strings.TrimSpace(q.out), "app-done") {
		t.Errorf("quiet output = %q", q.out)
	}
}

func TestRemove(t *testing.T) {
	root, _ := setup(t)
	done := filepath.Join(root, "org", "app-done")

	r := run(t, false, "", "remove", "--verdict", "safe")
	if r.code != ExitRefused || !exists(done) {
		t.Fatalf("without --yes off a terminal: exit %d, %s", r.code, r.err)
	}
	r = run(t, true, "n\n", "remove", "--verdict", "safe")
	if r.code != ExitRefused || !exists(done) {
		t.Fatalf("declined: exit %d, %s", r.code, r.err)
	}
	r = run(t, false, "", "remove", "--verdict", "safe", "--dry-run")
	if r.code != ExitOK || !strings.Contains(r.out, "would-remove") || !exists(done) {
		t.Fatalf("dry run: exit %d, %s%s", r.code, r.out, r.err)
	}
	r = run(t, true, "y\n", "remove", "--verdict", "safe")
	if r.code != ExitOK || exists(done) {
		t.Fatalf("confirmed: exit %d, %s%s", r.code, r.out, r.err)
	}
	if !strings.Contains(r.err, "removing 0/1…") || !strings.Contains(r.out, "removed      "+tilde(done)) ||
		!strings.Contains(r.out, "1 removed in ") {
		t.Errorf("progress and summary missing:\nstdout %q\nstderr %q", r.out, r.err)
	}
}

func TestRemoveByPath(t *testing.T) {
	root, app := setup(t)
	wip := filepath.Join(root, "org", "app-wip")

	if r := run(t, false, "", "remove", "--yes", app.Dir); r.code != ExitRefused {
		t.Errorf("main worktree: exit %d, %s", r.code, r.err)
	}
	if r := run(t, false, "", "remove", "--yes", root); r.code != ExitUsage {
		t.Errorf("not a worktree: exit %d, %s", r.code, r.err)
	}
	r := run(t, false, "", "remove", "--yes", wip)
	if r.code != ExitOK || !strings.Contains(r.out, "skipped") || !exists(wip) {
		t.Errorf("unsafe without --force: exit %d, %s", r.code, r.out)
	}
	r = run(t, false, "", "remove", "--yes", "--force", wip)
	if r.code != ExitOK || exists(wip) {
		t.Errorf("unsafe with --force: exit %d, %s%s", r.code, r.out, r.err)
	}
}

func TestUsage(t *testing.T) {
	setup(t)
	for _, args := range [][]string{
		{"remove"},
		{"remove", "--verdict", "keep"},
		{"remove", "--verdict", "safe", "/x"},
		{"scan", "--verdict", "bogus"},
		{"scan", "--older-than", "soon"},
		{"scan", "--bogus-flag"},
		{"tui"},
	} {
		if r := run(t, false, "", args...); r.code != ExitUsage {
			t.Errorf("%q: exit %d, want %d (%s)", args, r.code, ExitUsage, r.err)
		}
	}
}

func TestPrune(t *testing.T) {
	setup(t)
	r := run(t, false, "", "prune", "--dry-run")
	if !strings.Contains(r.out, "would prune 1 entry") {
		t.Errorf("dry run: %q", r.out)
	}
	run(t, false, "", "prune")
	if r := run(t, false, "", "prune"); !strings.Contains(r.out, "No stale worktree entries") {
		t.Errorf("after prune: %q", r.out)
	}
}

func TestParseAge(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"14d": 14 * 24 * time.Hour, "2w": 14 * 24 * time.Hour, "36h": 36 * time.Hour, "0d": 0,
	} {
		if got, err := parseAge(in); err != nil || got != want {
			t.Errorf("parseAge(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "d", "-1d", "1y", "abc"} {
		if _, err := parseAge(in); err == nil {
			t.Errorf("parseAge(%q) succeeded", in)
		}
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestCompletion(t *testing.T) {
	// A broken config must not break the completion scripts.
	dir := t.TempDir()
	bad := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(bad, []byte("not toml ["), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("METLA_CONFIG", bad)
	for _, sh := range []string{"bash", "zsh", "fish", "powershell"} {
		if r := run(t, false, "", "completion", sh); r.code != ExitOK || !strings.Contains(r.out, "metla") {
			t.Errorf("completion %s: exit %d, %s", sh, r.code, r.err)
		}
	}
	r := run(t, false, "", "__complete", "remove", "--verdict", "")
	for _, want := range []string{"safe\t", "review\t", "unsafe\t"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("verdict completion lacks %q:\n%s", want, r.out)
		}
	}
	if r := run(t, false, "", "__complete", "scan", ""); !strings.Contains(r.out, ":16") {
		t.Errorf("paths should complete as directories:\n%s", r.out)
	}
}

func TestClean(t *testing.T) {
	root, app := setup(t)
	for _, dir := range []string{app.Dir, filepath.Join(root, "org", "app-open")} {
		if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "target"), 0o755); err != nil {
			t.Fatal(err)
		}
		tag := "Signature: 8a477f597d28d172789f06886806bc55\n# This file is a cache directory tag created by cargo.\n"
		if err := os.WriteFile(filepath.Join(dir, "target", "CACHEDIR.TAG"), []byte(tag), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "cargo"), []byte("#!/bin/sh\nrm -rf target\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if r := run(t, false, "", "clean"); !strings.Contains(r.out, "No Rust target directories") {
		t.Errorf("tiny targets are below the 1 MB default: %s", r.out)
	}
	if r := run(t, false, "", "clean", "--min-size", "0"); r.code != ExitRefused {
		t.Errorf("without --yes off a terminal: exit %d, %s", r.code, r.err)
	}
	r := run(t, false, "", "clean", "--min-size", "0", "--dry-run")
	if strings.Count(r.out, "would-clean") != 2 || !exists(filepath.Join(app.Dir, "target")) {
		t.Errorf("dry run, main and linked worktree both listed:\n%s", r.out)
	}
	r = run(t, false, "", "clean", "--min-size", "0", "--yes")
	if r.code != ExitOK || strings.Count(r.out, "cleaned") != 2 || exists(filepath.Join(app.Dir, "target")) {
		t.Errorf("clean: exit %d\n%s%s", r.code, r.out, r.err)
	}
}
