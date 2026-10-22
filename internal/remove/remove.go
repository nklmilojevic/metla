// Package remove deletes worktrees the way git expects, and clears the
// entries git keeps for worktrees whose directory is already gone.
package remove

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/nklmilojevic/metla/internal/discover"
	"github.com/nklmilojevic/metla/internal/gitx"
	"github.com/nklmilojevic/metla/internal/scan"
)

// Statuses a Result can have.
const (
	Removed     = "removed"
	WouldRemove = "would-remove"
	Skipped     = "skipped"
	Failed      = "failed"
)

// Options controls removal.
type Options struct {
	Force        bool // also remove unsafe worktrees
	DeleteBranch bool // delete the branch of a worktree metla proved merged
	DryRun       bool
	IgnoreDirty  []string // must match the scan's, for the last-moment check
}

// Result is what happened to one worktree.
type Result struct {
	Path          string       `json:"path"`
	Branch        string       `json:"branch,omitempty"`
	Verdict       scan.Verdict `json:"verdict"`
	Status        string       `json:"status"`
	BranchDeleted bool         `json:"branch_deleted,omitempty"`
	Error         string       `json:"error,omitempty"`
}

var errChanged = errors.New("has uncommitted changes now; rescan or pass --force")

// Worktrees removes each worktree its verdict allows and reports the rest as
// skipped.
func Worktrees(ctx context.Context, ws []scan.Worktree, opts Options) []Result {
	out := make([]Result, 0, len(ws))
	for _, w := range ws {
		res := Result{Path: w.Path, Branch: w.Branch, Verdict: w.Verdict, Status: Removed}
		switch {
		case !w.Removable(opts.Force):
			res.Status = Skipped
			res.Error = skipReason(w)
		case opts.DryRun:
			res.Status = WouldRemove
		default:
			if err := one(ctx, w, opts); err != nil {
				res.Status, res.Error = Failed, err.Error()
			} else if opts.DeleteBranch && w.Merged && w.Branch != "" {
				// -D because a squash-merged tip is not an ancestor of the
				// base, so -d refuses it; metla proved the merge itself.
				_, err := gitx.Run(ctx, w.RepoDir, "branch", "-D", w.Branch)
				res.BranchDeleted = err == nil
			}
		}
		out = append(out, res)
	}
	return out
}

func skipReason(w scan.Worktree) string {
	if w.Verdict == scan.Unsafe {
		return "unsafe; pass --force to remove anyway"
	}
	return "kept: " + firstReason(w)
}

func firstReason(w scan.Worktree) string {
	if len(w.Reasons) == 0 {
		return string(w.Verdict)
	}
	return w.Reasons[0]
}

func one(ctx context.Context, w scan.Worktree, opts Options) error {
	if w.Orphan {
		return removeOrphan(w.Path)
	}
	// The scan may be minutes old by the time someone confirms in the TUI.
	if !opts.Force {
		n, err := scan.Changed(ctx, w.Path, opts.IgnoreDirty)
		if err != nil {
			return err
		}
		if n > 0 {
			return errChanged
		}
	}
	if w.Locked {
		if _, err := gitx.Run(ctx, w.RepoDir, "worktree", "unlock", w.Path); err != nil {
			return err
		}
	}
	// --force is always passed: lock-file churn and submodules make git
	// refuse otherwise, and the check above already stood in for git's own.
	_, err := gitx.Run(ctx, w.RepoDir, "worktree", "remove", "--force", w.Path)
	return err
}

// removeOrphan deletes a worktree whose repository is gone. It checks again
// that the directory is still an orphan, since no git command can vouch for it.
func removeOrphan(path string) error {
	if kind, _, _ := discover.Inspect(path); kind != discover.KindOrphan {
		return errors.New("no longer an orphaned worktree")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) == "/" {
		return errors.New("refusing to remove " + path)
	}
	return os.RemoveAll(path)
}

// PruneResult is what git worktree prune did, or would do, in one repository.
type PruneResult struct {
	Repo    string   `json:"repo"`
	Entries []string `json:"entries"`
	Error   string   `json:"error,omitempty"`
}

// Prune clears stale worktree entries in every repository that has some.
func Prune(ctx context.Context, repos []scan.Repo, dryRun bool) []PruneResult {
	var out []PruneResult
	for _, r := range repos {
		if len(r.Prunable) == 0 {
			continue
		}
		res := PruneResult{Repo: r.Dir}
		for _, p := range r.Prunable {
			res.Entries = append(res.Entries, p.Path)
		}
		if !dryRun {
			if _, err := gitx.Run(ctx, r.Dir, "worktree", "prune"); err != nil {
				res.Error = err.Error()
			}
		}
		out = append(out, res)
	}
	return out
}
