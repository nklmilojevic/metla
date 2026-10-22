// Package scan inspects every worktree of the repositories discover finds
// and decides which ones are safe to remove.
package scan

import "time"

// Verdict is metla's recommendation for a worktree.
type Verdict string

// Verdicts, from most to least protected.
const (
	// Keep is never removed: a primary worktree, a protected branch, or a
	// lock that something still holds.
	Keep Verdict = "keep"
	// Unsafe would lose work: uncommitted changes, commits nowhere else, a
	// process inside the tree, or an orphan metla cannot inspect. Removable
	// only with --force.
	Unsafe Verdict = "unsafe"
	// Safe is clean and its work has landed in the default branch.
	Safe Verdict = "safe"
	// Review is clean and pushed but not merged. Removed only on request.
	Review Verdict = "review"
)

// Verdicts lists every verdict, for flag validation.
var Verdicts = []Verdict{Keep, Unsafe, Safe, Review}

// Proc is a process whose working directory is inside a worktree.
type Proc struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

// PR is the pull request gh found for a branch.
type PR struct {
	Number int    `json:"number"`
	State  string `json:"state"` // MERGED, CLOSED or OPEN
	URL    string `json:"url"`
	// HeadOid is the commit the pull request ended on.
	HeadOid string `json:"head_oid"`
}

// Worktree is one checkout and what metla learned about it.
type Worktree struct {
	Repo    string `json:"repo"`
	RepoDir string `json:"repo_dir"`
	Path    string `json:"path"`
	Branch  string `json:"branch,omitempty"`
	Head    string `json:"head,omitempty"`

	Primary   bool `json:"primary"`
	Protected bool `json:"protected"`
	Detached  bool `json:"detached"`
	Orphan    bool `json:"orphan"`

	Dirty      bool `json:"dirty"`
	DirtyFiles int  `json:"dirty_files"`
	// Unpushed counts commits that are on no remote and not in the local
	// default branch.
	Unpushed int `json:"unpushed"`
	// Unreachable counts commits on a detached HEAD that no branch, tag or
	// remote holds: removing the worktree loses them.
	Unreachable int `json:"unreachable"`

	Merged   bool   `json:"merged"`
	MergedBy string `json:"merged_by,omitempty"` // ancestor, empty, squash, rebase or pr
	PR       *PR    `json:"pr,omitempty"`

	Busy       []Proc `json:"busy,omitempty"`
	Locked     bool   `json:"locked"`
	LockReason string `json:"lock_reason,omitempty"`
	LockStale  bool   `json:"lock_stale"`

	LastActive time.Time `json:"last_active"`
	AgeDays    int       `json:"age_days"`
	SizeKB     int64     `json:"size_kb,omitempty"`

	Verdict Verdict  `json:"verdict"`
	Reasons []string `json:"reasons"`
}

// Removable reports whether the worktree may be removed, given --force.
func (w Worktree) Removable(force bool) bool {
	switch w.Verdict {
	case Safe, Review:
		return true
	case Unsafe:
		return force
	}
	return false
}

// Prunable is an entry git keeps for a worktree whose directory is gone.
type Prunable struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
}

// Repo is a repository and its worktrees.
type Repo struct {
	Name          string     `json:"name"`
	Dir           string     `json:"dir"`
	CommonDir     string     `json:"common_dir"`
	DefaultBranch string     `json:"default_branch,omitempty"`
	Worktrees     []Worktree `json:"worktrees"`
	Prunable      []Prunable `json:"prunable,omitempty"`
	Error         string     `json:"error,omitempty"`
}

// Report is the result of a scan.
type Report struct {
	Repos   []Repo     `json:"repos"`
	Orphans []Worktree `json:"orphans,omitempty"`
}

// All returns every worktree in the report, orphans last.
func (r Report) All() []Worktree {
	var out []Worktree
	for _, repo := range r.Repos {
		out = append(out, repo.Worktrees...)
	}
	return append(out, r.Orphans...)
}
