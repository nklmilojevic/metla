// Package gittest builds throwaway git repositories for tests.
package gittest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nklmilojevic/metla/internal/gitx"
)

// repoEnv points git at a repository other than the working directory. Git
// sets these for hooks, so tests run from a pre-push hook would otherwise
// init, configure and commit in the developer's repository.
var repoEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
}

// Isolate keeps git away from the developer's config and from any repository
// a hook runs the tests in.
func Isolate(t *testing.T) {
	t.Helper()
	for _, k := range repoEnv {
		if v, ok := os.LookupEnv(k); ok {
			t.Setenv(k, v) // restores the value after the test
			if err := os.Unsetenv(k); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

// TempDir is t.TempDir with symlinks resolved, so paths match the ones git
// prints (macOS puts temp dirs under the /var -> /private/var link).
func TempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Repo is a repository or worktree on disk.
type Repo struct {
	Dir string
	t   *testing.T
}

// New creates a repository at dir with one commit on main.
func New(t *testing.T, dir string) *Repo {
	t.Helper()
	Isolate(t)
	r := &Repo{Dir: dir, t: t}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.Git("init", "-q", "-b", "main")
	r.Git("config", "commit.gpgSign", "false")
	r.CommitFile("README", "hello\n", "initial")
	return r
}

// WithOrigin creates a bare origin in a temp dir, pushes main to it and sets
// origin/HEAD, like a fresh clone. It returns the origin's path.
func (r *Repo) WithOrigin() string {
	r.t.Helper()
	origin := filepath.Join(TempDir(r.t), "origin.git")
	r.Git("init", "-q", "--bare", "-b", "main", origin)
	r.Git("remote", "add", "origin", origin)
	r.Git("push", "-q", "-u", "origin", "main")
	r.Git("remote", "set-head", "origin", "main")
	return origin
}

// At returns a handle for another worktree of the same repository.
func (r *Repo) At(dir string) *Repo { return &Repo{Dir: dir, t: r.t} }

// Worktree adds a linked worktree at dir on a new branch.
func (r *Repo) Worktree(dir, branch string) *Repo {
	r.t.Helper()
	r.Git("worktree", "add", "-q", "-b", branch, dir)
	return r.At(dir)
}

// Git runs git and fails the test on error.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	out, err := gitx.Run(context.Background(), r.Dir, args...)
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

// Write creates or replaces a file relative to the worktree root.
func (r *Repo) Write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.Dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// CommitFile writes a file and commits it with msg.
func (r *Repo) CommitFile(path, content, msg string) string {
	r.t.Helper()
	r.Write(path, content)
	r.Git("add", path)
	r.Git("commit", "-q", "-m", msg)
	return r.Git("rev-parse", "HEAD")
}

// Head returns the current commit.
func (r *Repo) Head() string { return r.Git("rev-parse", "HEAD") }

// Lines splits git output into non-empty lines.
func Lines(s string) []string {
	var out []string
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
