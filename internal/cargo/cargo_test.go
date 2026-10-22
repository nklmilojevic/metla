package cargo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nklmilojevic/metla/internal/scan"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const tag = "Signature: 8a477f597d28d172789f06886806bc55\n# This file is a cache directory tag created by cargo.\n"

func TestFind(t *testing.T) {
	wt := t.TempDir()
	write(t, filepath.Join(wt, "Cargo.toml"), "[workspace]\n")
	write(t, filepath.Join(wt, "target", "CACHEDIR.TAG"), tag)
	write(t, filepath.Join(wt, "target", "debug", "big"), strings.Repeat("x", 64<<10))
	// A nested project with its own target dir.
	write(t, filepath.Join(wt, "tools", "gen", "Cargo.toml"), "[package]\n")
	write(t, filepath.Join(wt, "tools", "gen", "target", "CACHEDIR.TAG"), tag)
	// A target dir some other tool made is left alone.
	write(t, filepath.Join(wt, "web", "Cargo.toml"), "[package]\n")
	write(t, filepath.Join(wt, "web", "target", "out.js"), "x")
	// Too deep, and hidden.
	write(t, filepath.Join(wt, "a", "b", "c", "Cargo.toml"), "")
	write(t, filepath.Join(wt, "a", "b", "c", "target", "CACHEDIR.TAG"), tag)
	write(t, filepath.Join(wt, ".claude", "x", "Cargo.toml"), "")
	write(t, filepath.Join(wt, ".claude", "x", "target", "CACHEDIR.TAG"), tag)

	busy := func(dir string) []scan.Proc {
		if dir == filepath.Join(wt, "tools", "gen") {
			return []scan.Proc{{PID: 1, Command: "rust-analyzer"}, {PID: 2, Command: "cargo"}}
		}
		return nil
	}
	// The same worktree listed twice yields each project once.
	got := Find(context.Background(), []Worktree{{"r", wt}, {"r", wt}}, busy, time.Now())
	if len(got) != 2 {
		t.Fatalf("found %d targets: %+v", len(got), got)
	}
	if got[0].Dir != wt || got[0].SizeKB < 64 {
		t.Errorf("largest first: %+v", got[0])
	}
	if b := got[1].Busy; len(b) != 1 || b[0].Command != "cargo" {
		t.Errorf("busy = %+v, want cargo only (rust-analyzer does not count)", b)
	}
}

func TestClean(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	write(t, filepath.Join(bin, "cargo"), "#!/bin/sh\necho \"$RUSTUP_AUTO_INSTALL $*\" > "+log+"\n")
	if err := os.Chmod(filepath.Join(bin, "cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	if err := Clean(context.Background(), Target{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "0 clean --offline --manifest-path " + filepath.Join(dir, "Cargo.toml") + "\n"
	if string(b) != want {
		t.Errorf("cargo ran with %q, want %q", b, want)
	}

	t.Setenv("PATH", t.TempDir())
	if err := Clean(context.Background(), Target{Dir: dir}); err != ErrNoCargo {
		t.Errorf("without cargo: %v", err)
	}
}
