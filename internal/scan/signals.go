package scan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nklmilojevic/metla/internal/gitx"
)

// DefaultIgnoreDirty are files whose changes alone do not make a worktree
// dirty: lock files a package manager rewrites, which hold no authored work.
var DefaultIgnoreDirty = []string{
	"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml",
	"bun.lock", "bun.lockb", "composer.lock", "Gemfile.lock", "Cargo.lock",
	"poetry.lock", "Pipfile.lock", "uv.lock", "go.sum", "flake.lock",
	"gradle.lockfile", "packages.lock.json",
}

// status counts changed files, leaving out ones that match ignore, and
// returns the newest modification time among the changes it counted.
func status(ctx context.Context, dir string, ignore []string) (changed int, newest time.Time, err error) {
	out, err := gitx.RunRaw(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return 0, time.Time{}, err
	}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		xy, path := f[:2], f[3:]
		if xy[0] == 'R' || xy[0] == 'C' {
			i++ // the original path follows a rename or copy
		}
		if ignored(path, ignore) {
			continue
		}
		changed++
		if info, err := os.Lstat(filepath.Join(dir, path)); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return changed, newest, nil
}

// Changed counts a worktree's changed files, leaving out ones that match
// ignore.
func Changed(ctx context.Context, dir string, ignore []string) (int, error) {
	n, _, err := status(ctx, dir, ignore)
	return n, err
}

func ignored(path string, globs []string) bool {
	base := filepath.Base(strings.TrimSuffix(path, "/"))
	for _, g := range globs {
		if ok, _ := filepath.Match(g, base); ok {
			return true
		}
	}
	return false
}

func count(ctx context.Context, dir string, args ...string) int {
	out, err := gitx.Run(ctx, dir, append([]string{"rev-list", "--count"}, args...)...)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}

// unpushed counts commits on head that are on no remote and not in the local
// default branch. With no remotes at all, that is everything not merged
// locally.
func unpushed(ctx context.Context, dir, head, base string) int {
	args := []string{head, "--not", "--remotes"}
	if base != "" && gitx.RefExists(ctx, dir, "refs/heads/"+base) {
		args = append(args, "refs/heads/"+base)
	}
	return count(ctx, dir, args...)
}

// unreachable counts commits only a detached HEAD holds.
func unreachable(ctx context.Context, dir, head string) int {
	return count(ctx, dir, head, "--not", "--branches", "--tags", "--remotes")
}

// baseRefs returns the refs that count as "landed": the default branch on
// origin and locally.
func baseRefs(ctx context.Context, dir, base string) []string {
	if base == "" {
		return nil
	}
	var refs []string
	for _, r := range []string{"refs/remotes/origin/" + base, "refs/heads/" + base} {
		if gitx.RefExists(ctx, dir, r) {
			refs = append(refs, r)
		}
	}
	return refs
}

// forkBase returns the upstream of a branch that tracks a different branch,
// such as one created with `git worktree add -b fix origin/staging`. Work on
// it lands there, not necessarily in the default branch.
func forkBase(ctx context.Context, dir, branch string) string {
	if branch == "" {
		return ""
	}
	up, err := gitx.Run(ctx, dir, "for-each-ref", "--format=%(upstream)", "refs/heads/"+branch)
	if err != nil || up == "" || strings.HasSuffix(up, "/"+branch) || !gitx.RefExists(ctx, dir, up) {
		return ""
	}
	return up
}

// neverCommitted reports whether a branch's reflog holds only its creation,
// so the branch has no commits of its own.
func neverCommitted(ctx context.Context, dir, branch string) bool {
	if branch == "" {
		return false
	}
	out, err := gitx.Run(ctx, dir, "reflog", "show", "--format=%gs", "refs/heads/"+branch, "--")
	if err != nil {
		return false
	}
	lines := strings.Split(out, "\n")
	return len(lines) == 1 && strings.HasPrefix(lines[0], "branch: Created from")
}

// merged reports how head landed in one of the base refs: as an ancestor, as
// a squash of all its changes, or rebased commit by commit. Squash and rebase
// detection compare patch-ids against at most limit commits on the base, so a
// very old branch on a busy repository falls back to "not merged".
func merged(ctx context.Context, dir, head string, bases []string, limit int) string {
	for _, b := range bases {
		if gitx.IsAncestor(ctx, dir, head, b) {
			return "ancestor"
		}
	}
	for _, b := range bases {
		if how := patchMerged(ctx, dir, head, b, limit); how != "" {
			return how
		}
	}
	return ""
}

func patchMerged(ctx context.Context, dir, head, base string, limit int) string {
	mb, err := gitx.Run(ctx, dir, "merge-base", base, head)
	if err != nil || mb == "" {
		return ""
	}
	if n := count(ctx, dir, mb+".."+base); n == 0 || n > limit {
		return ""
	}
	landed := patchIDs(ctx, dir, logArgs(mb+".."+base))
	if len(landed) == 0 {
		return ""
	}
	diff := []string{"diff", "--no-ext-diff", "--no-color", "--no-textconv", mb, head}
	if ids := patchIDs(ctx, dir, diff); len(ids) == 1 && landed[firstKey(ids)] {
		return "squash"
	}
	own := patchIDs(ctx, dir, logArgs(mb+".."+head))
	if len(own) == 0 {
		return ""
	}
	for id := range own {
		if !landed[id] {
			return ""
		}
	}
	return "rebase"
}

func logArgs(rng string) []string {
	return []string{
		"-c", "log.showSignature=false", "log", "-p", "--no-merges", "--no-ext-diff",
		"--no-color", "--no-textconv", "--format=commit %H", rng,
	}
}

// patchIDs returns the stable patch-ids of the patches the git command
// prints.
func patchIDs(ctx context.Context, dir string, args []string) map[string]bool {
	out, err := gitx.Pipe(ctx, dir, args, []string{"patch-id", "--stable"})
	if err != nil {
		return nil
	}
	ids := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		if id, _, ok := strings.Cut(line, " "); ok && id != "" {
			ids[id] = true
		}
	}
	return ids
}

func firstKey(m map[string]bool) string {
	for k := range m {
		return k
	}
	return ""
}

func commitTime(ctx context.Context, dir, head string) time.Time {
	out, err := gitx.Run(ctx, dir, "log", "-1", "--format=%ct", head)
	if err != nil {
		return time.Time{}
	}
	ts, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}

// sizeKB returns the on-disk size of path. du is much faster than a Go walk
// over node_modules-sized trees.
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

// lockPIDRe matches the reason agent harnesses write when they lock a
// worktree, e.g. "claude agent agent-a7dd… (pid 44637 start Tue Oct 13
// 15:23:56 2026)".
var lockPIDRe = regexp.MustCompile(`\bpid (\d+)(?: start ([^)]+))?`)

// staleLock reports whether a lock names a process that is gone. A lock
// without a pid is a deliberate one and never stale. startOf returns a
// process's start time in `ps -o lstart` form, or "" when it is not running.
func staleLock(reason string, startOf func(pid int) string) bool {
	m := lockPIDRe.FindStringSubmatch(reason)
	if m == nil {
		return false
	}
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		return false
	}
	started := startOf(pid)
	if started == "" {
		return true
	}
	// A recorded start time that differs means the pid was reused.
	return m[2] != "" && normSpace(m[2]) != normSpace(started)
}

// processStart returns when pid started, as `ps -o lstart` prints it, or ""
// when no such process runs.
func processStart(pid int) string {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func normSpace(s string) string { return strings.Join(strings.Fields(s), " ") }
