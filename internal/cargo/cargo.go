// Package cargo finds the target directories of Rust projects in worktrees
// and empties them with cargo clean.
package cargo

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nklmilojevic/metla/internal/scan"
)

// maxDepth is how far below a worktree root a Cargo.toml can be.
const maxDepth = 2

// editors run inside a workspace without building in it. Cleaning under them
// only makes them index again, so they do not make a project busy.
var editors = map[string]bool{"rust-analyzer": true, "rust-analyzer-proc-macro-srv": true}

// Target is a Rust project with a build directory.
type Target struct {
	Repo      string      `json:"repo"`
	Worktree  string      `json:"worktree"`
	Dir       string      `json:"dir"` // where cargo clean runs
	TargetDir string      `json:"target_dir"`
	SizeKB    int64       `json:"size_kb"`
	LastBuilt time.Time   `json:"last_built"`
	AgeDays   int         `json:"age_days"`
	Busy      []scan.Proc `json:"busy,omitempty"`
}

// Worktree is a checkout to search, with the repository it belongs to.
type Worktree struct {
	Repo string
	Path string
}

// Find returns the target directories under the worktrees, largest first.
// busy reports processes working in a path; nil skips the check.
func Find(ctx context.Context, ws []Worktree, busy func(string) []scan.Proc, now time.Time) []Target {
	var mu sync.Mutex
	seen := map[string]bool{}
	var out []Target
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for _, w := range ws {
		for _, t := range projects(w) {
			mu.Lock()
			dup := seen[t.TargetDir]
			seen[t.TargetDir] = true
			mu.Unlock()
			if dup {
				continue
			}
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				t.SizeKB = sizeKB(ctx, t.TargetDir)
				t.LastBuilt = lastBuilt(t.TargetDir)
				if !t.LastBuilt.IsZero() {
					t.AgeDays = max(0, int(now.Sub(t.LastBuilt).Hours()/24))
				}
				if busy != nil {
					for _, p := range busy(t.Dir) {
						if !editors[p.Command] {
							t.Busy = append(t.Busy, p)
						}
					}
				}
				mu.Lock()
				out = append(out, t)
				mu.Unlock()
			})
		}
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].SizeKB != out[j].SizeKB {
			return out[i].SizeKB > out[j].SizeKB
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}

// projects finds directories with a Cargo.toml and a cargo-made target dir.
func projects(w Worktree) []Target {
	var out []Target
	base := strings.Count(filepath.Clean(w.Path), string(filepath.Separator))
	_ = filepath.WalkDir(w.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		name := d.Name()
		if path != w.Path && (strings.HasPrefix(name, ".") || name == "target" || name == "node_modules") {
			return fs.SkipDir
		}
		target := filepath.Join(path, "target")
		if exists(filepath.Join(path, "Cargo.toml")) && madeByCargo(target) {
			out = append(out, Target{Repo: w.Repo, Worktree: w.Path, Dir: path, TargetDir: target})
		}
		if strings.Count(path, string(filepath.Separator))-base >= maxDepth {
			return fs.SkipDir
		}
		return nil
	})
	return out
}

// madeByCargo checks the CACHEDIR.TAG cargo writes into every target dir, so
// a "target" directory of some other tool is never touched.
func madeByCargo(target string) bool {
	b, err := os.ReadFile(filepath.Join(target, "CACHEDIR.TAG"))
	return err == nil && bytes.Contains(b, []byte("created by cargo"))
}

// lastBuilt is the newest modification time among the target dir's entries
// (debug, release, a target triple), which cargo touches on every build.
func lastBuilt(target string) time.Time {
	var newest time.Time
	entries, err := os.ReadDir(target)
	if err != nil {
		return newest
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}

func sizeKB(ctx context.Context, path string) int64 {
	out, err := exec.CommandContext(ctx, "du", "-sk", path).Output()
	if err != nil {
		return 0
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return 0
	}
	kb, _ := strconv.ParseInt(f[0], 10, 64)
	return kb
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ErrNoCargo is returned when cargo is not on PATH.
var ErrNoCargo = errors.New("cargo not found on PATH")

// Clean runs cargo clean in the project. It never touches the network, and
// it never lets rustup install a toolchain a rust-toolchain.toml asks for.
func Clean(ctx context.Context, t Target) error {
	bin, err := exec.LookPath("cargo")
	if err != nil {
		return ErrNoCargo
	}
	cmd := exec.CommandContext(ctx, bin, "clean", "--offline", "--manifest-path", filepath.Join(t.Dir, "Cargo.toml"))
	cmd.Dir = t.Dir
	cmd.Env = append(os.Environ(), "RUSTUP_AUTO_INSTALL=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(lastLine(msg))
		}
		return err
	}
	return nil
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
