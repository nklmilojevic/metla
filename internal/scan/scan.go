package scan

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nklmilojevic/metla/internal/discover"
	"github.com/nklmilojevic/metla/internal/gitx"
)

// DefaultProtect are branch globs that are never removed.
var DefaultProtect = []string{"main", "master", "develop", "trunk"}

// DefaultPatchLimit bounds how many default-branch commits squash and rebase
// detection compare against.
const DefaultPatchLimit = 2000

// Options controls a scan.
type Options struct {
	Protect     []string // branch globs whose worktrees are kept
	IgnoreDirty []string // file name globs whose changes are not dirt
	PatchLimit  int
	Fetch       bool // git fetch --prune each repository first
	Size        bool // measure each worktree with du

	// PRs looks up the pull request for a branch git could not prove merged.
	// Nil skips the lookup.
	PRs func(ctx context.Context, repoDir, branch string) *PR
	// Busy returns the processes working inside a path. Nil skips the check.
	Busy func(path string) []Proc
	// ProcessStart says when a pid started, for stale lock detection; nil
	// uses ps.
	ProcessStart func(pid int) string
	// Progress, when set, is called after each repository finishes. Calls
	// come from worker goroutines but never overlap.
	Progress func(done, total int)

	Now func() time.Time
}

func (o *Options) defaults() {
	if o.Protect == nil {
		o.Protect = DefaultProtect
	}
	if o.IgnoreDirty == nil {
		o.IgnoreDirty = DefaultIgnoreDirty
	}
	if o.PatchLimit == 0 {
		o.PatchLimit = DefaultPatchLimit
	}
	if o.ProcessStart == nil {
		o.ProcessStart = processStart
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Run scans the repositories and orphans a walk found.
func Run(ctx context.Context, found discover.Result, opts Options) Report {
	opts.defaults()
	repos := make([]Repo, len(found.Repos))
	repoSem := make(chan struct{}, runtime.NumCPU())
	// One limit on worktree inspections across all repositories, so a
	// directory of repos with many worktrees each cannot fork hundreds of
	// gits (or gh calls) at once.
	wtSem := make(chan struct{}, max(4, 2*runtime.NumCPU()))
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for i, r := range found.Repos {
		wg.Go(func() {
			repoSem <- struct{}{}
			defer func() { <-repoSem }()
			repos[i] = scanRepo(ctx, r, opts, wtSem)
			if opts.Progress != nil {
				mu.Lock()
				done++
				opts.Progress(done, len(found.Repos))
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	var report Report
	for _, r := range repos {
		if len(r.Worktrees) > 0 || len(r.Prunable) > 0 || r.Error != "" {
			report.Repos = append(report.Repos, r)
		}
	}
	for _, o := range found.Orphans {
		w := Worktree{Repo: "(orphaned)", Path: o.Path, Orphan: true}
		if info, err := os.Stat(o.Path); err == nil {
			w.LastActive = info.ModTime()
			w.AgeDays = ageDays(opts.Now(), w.LastActive)
		}
		if opts.Size {
			w.SizeKB = sizeKB(ctx, o.Path)
		}
		classify(&w)
		report.Orphans = append(report.Orphans, w)
	}
	return report
}

func scanRepo(ctx context.Context, r discover.Repo, opts Options, wtSem chan struct{}) Repo {
	repo := Repo{Name: repoName(r), Dir: r.Dir, CommonDir: r.CommonDir}
	if opts.Fetch {
		// Best effort: offline or unauthenticated remotes fall back to the
		// refs already on disk.
		_, _ = gitx.Run(ctx, r.Dir, "fetch", "--prune", "--quiet")
	}
	list, err := gitx.ListWorktrees(ctx, r.Dir)
	if err != nil {
		repo.Error = err.Error()
		return repo
	}
	repo.DefaultBranch = gitx.DefaultBranch(ctx, r.Dir)
	bases := baseRefs(ctx, r.Dir, repo.DefaultBranch)

	primaryTaken := len(list) > 0 && list[0].Bare
	for _, e := range list {
		if e.Bare {
			continue
		}
		if e.Prunable {
			repo.Prunable = append(repo.Prunable, Prunable{Path: e.Path, Branch: e.Branch})
			primaryTaken = true
			continue
		}
		repo.Worktrees = append(repo.Worktrees, Worktree{
			Repo: repo.Name, RepoDir: r.Dir, Path: e.Path, Branch: e.Branch, Head: e.Head,
			Primary: !primaryTaken, Detached: e.Detached, Locked: e.Locked, LockReason: e.LockReason,
		})
		primaryTaken = true
	}
	// Only linked worktrees are candidates; a repo with none has nothing to
	// sweep, so skip inspecting its main checkout.
	if len(repo.Worktrees) <= 1 && len(repo.Prunable) == 0 {
		repo.Worktrees = nil
		return repo
	}

	var wg sync.WaitGroup
	for i := range repo.Worktrees {
		w := &repo.Worktrees[i]
		w.Protected = !w.Primary && protected(w.Branch, opts.Protect)
		if w.Primary || w.Protected {
			classify(w)
			continue
		}
		wg.Go(func() {
			wtSem <- struct{}{}
			defer func() { <-wtSem }()
			inspect(ctx, w, r.Dir, repo.DefaultBranch, bases, opts)
			classify(w)
		})
	}
	wg.Wait()
	return repo
}

func inspect(ctx context.Context, w *Worktree, repoDir, base string, bases []string, opts Options) {
	if w.Locked {
		w.LockStale = staleLock(w.LockReason, opts.ProcessStart)
	}
	if opts.Busy != nil {
		w.Busy = opts.Busy(w.Path)
	}
	changed, newest, err := status(ctx, w.Path, opts.IgnoreDirty)
	if err != nil {
		// A worktree git cannot read is treated as holding work.
		w.Dirty, w.DirtyFiles = true, 1
	} else {
		w.Dirty, w.DirtyFiles = changed > 0, changed
	}

	if w.Head != "" {
		if up := forkBase(ctx, repoDir, w.Branch); up != "" {
			bases = append(append([]string{}, bases...), up)
		}
		if how := merged(ctx, repoDir, w.Head, bases, opts.PatchLimit); how != "" {
			w.Merged, w.MergedBy = true, how
			if how == "ancestor" && neverCommitted(ctx, repoDir, w.Branch) {
				w.MergedBy = "empty"
			}
		}
		if w.Detached {
			w.Unreachable = unreachable(ctx, repoDir, w.Head)
		} else {
			w.Unpushed = unpushed(ctx, repoDir, w.Head, base)
		}
		w.LastActive = commitTime(ctx, repoDir, w.Head)
	}
	if newest.After(w.LastActive) {
		w.LastActive = newest
	}
	if !w.LastActive.IsZero() {
		w.AgeDays = ageDays(opts.Now(), w.LastActive)
	}
	if opts.PRs != nil && !w.Merged && w.Branch != "" {
		if pr := opts.PRs(ctx, repoDir, w.Branch); pr != nil {
			w.PR = pr
			// Commits made after the pull request merged have not landed.
			if pr.State == "MERGED" && (pr.HeadOid == w.Head || gitx.IsAncestor(ctx, repoDir, w.Head, pr.HeadOid)) {
				w.Merged, w.MergedBy = true, "pr"
			}
		}
	}
	if opts.Size {
		w.SizeKB = sizeKB(ctx, w.Path)
	}
}

func ageDays(now, t time.Time) int {
	if t.IsZero() {
		return 0
	}
	return max(0, int(now.Sub(t).Hours()/24))
}

func protected(branch string, globs []string) bool {
	if branch == "" {
		return false
	}
	for _, g := range globs {
		if ok, _ := filepath.Match(g, branch); ok {
			return true
		}
	}
	return false
}

// repoName is the repository's directory name, without a bare repo's .git.
func repoName(r discover.Repo) string {
	return strings.TrimSuffix(filepath.Base(r.Dir), ".git")
}

// Sort orders worktrees oldest first, then by path.
func Sort(ws []Worktree) {
	sort.SliceStable(ws, func(i, j int) bool {
		if ws[i].AgeDays != ws[j].AgeDays {
			return ws[i].AgeDays > ws[j].AgeDays
		}
		return ws[i].Path < ws[j].Path
	})
}
