// Package gitx runs git and parses the output metla needs.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// baseEnv keeps git from taking the index lock while the user works in the
// tree, and from localizing the output metla parses.
var baseEnv = []string{"GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"}

// Run runs git in dir and returns stdout with surrounding whitespace removed.
// A failure carries git's stderr instead of a bare exit status.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := RunRaw(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

// RunRaw is Run without trimming, for NUL-separated output.
func RunRaw(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), baseEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("git %s: %s", args[0], msg)
		}
		return string(out), fmt.Errorf("git %s: %w", args[0], err)
	}
	return string(out), nil
}

// Pipe runs `git a... | git b...` in dir and returns the second command's
// output. It streams, so a long `log -p` never sits in memory.
func Pipe(ctx context.Context, dir string, a, b []string) (string, error) {
	first := exec.CommandContext(ctx, "git", a...)
	second := exec.CommandContext(ctx, "git", b...)
	for _, c := range []*exec.Cmd{first, second} {
		c.Dir = dir
		c.Env = append(os.Environ(), baseEnv...)
	}
	pipe, err := first.StdoutPipe()
	if err != nil {
		return "", err
	}
	second.Stdin = pipe
	var out bytes.Buffer
	second.Stdout = &out
	if err := first.Start(); err != nil {
		return "", err
	}
	if err := second.Start(); err != nil {
		_ = first.Process.Kill()
		_ = first.Wait()
		return "", err
	}
	errSecond := second.Wait()
	errFirst := first.Wait()
	if err := errors.Join(errFirst, errSecond); err != nil {
		return "", fmt.Errorf("git %s | git %s: %w", a[0], b[0], err)
	}
	return out.String(), nil
}

// Worktree is one record of `git worktree list --porcelain`.
type Worktree struct {
	Path       string
	Head       string
	Branch     string // short name; empty when detached
	Bare       bool
	Detached   bool
	Locked     bool
	LockReason string
	Prunable   bool
}

// ListWorktrees lists the worktrees of the repository that dir belongs to.
// The first entry is the main worktree, or the bare repository.
func ListWorktrees(ctx context.Context, dir string) ([]Worktree, error) {
	out, err := RunRaw(ctx, dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return ParseWorktrees(out), nil
}

// ParseWorktrees parses `git worktree list --porcelain -z` output: records
// end with an empty field, and every field ends with NUL.
func ParseWorktrees(out string) []Worktree {
	var list []Worktree
	var cur *Worktree
	for field := range strings.SplitSeq(out, "\x00") {
		if field == "" {
			if cur != nil {
				list = append(list, *cur)
				cur = nil
			}
			continue
		}
		key, val, _ := strings.Cut(field, " ")
		if key == "worktree" {
			if cur != nil {
				list = append(list, *cur)
			}
			cur = &Worktree{Path: val}
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "HEAD":
			cur.Head = val
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "bare":
			cur.Bare = true
		case "detached":
			cur.Detached = true
		case "locked":
			cur.Locked = true
			cur.LockReason = val
		case "prunable":
			cur.Prunable = true
		}
	}
	if cur != nil {
		list = append(list, *cur)
	}
	return list
}

// DefaultBranch resolves the default branch: origin/HEAD, then a local main
// or master. It returns "" when none exists.
func DefaultBranch(ctx context.Context, dir string) string {
	if ref, err := Run(ctx, dir, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		return strings.TrimPrefix(ref, "refs/remotes/origin/")
	}
	for _, b := range []string{"main", "master"} {
		if RefExists(ctx, dir, "refs/heads/"+b) || RefExists(ctx, dir, "refs/remotes/origin/"+b) {
			return b
		}
	}
	return ""
}

// RefExists reports whether ref resolves to a commit.
func RefExists(ctx context.Context, dir, ref string) bool {
	_, err := Run(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// IsAncestor reports whether a is an ancestor of (or equal to) b.
func IsAncestor(ctx context.Context, dir, a, b string) bool {
	_, err := Run(ctx, dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}
