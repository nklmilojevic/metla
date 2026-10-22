package cli

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/nklmilojevic/metla/internal/busy"
	"github.com/nklmilojevic/metla/internal/cargo"
	"github.com/nklmilojevic/metla/internal/discover"
	"github.com/nklmilojevic/metla/internal/gitx"
	"github.com/nklmilojevic/metla/internal/style"
)

// Statuses a clean result can have.
const (
	statusCleaned    = "cleaned"
	statusWouldClean = "would-clean"
	statusSkipped    = "skipped"
	statusFailed     = "failed"
)

// cleanResult is what happened to one target directory.
type cleanResult struct {
	cargo.Target
	Status string `json:"status"` // cleaned, would-clean, skipped or failed
	Error  string `json:"error,omitempty"`
}

func newCleanCmd(a *app) *cobra.Command {
	var depth, minSizeMB int
	var olderThan string
	var dryRun, yes, force, asJSON bool
	cmd := &cobra.Command{
		Use:   "clean [path...]",
		Short: "Run cargo clean in Rust projects to free their target directories",
		Long: `clean finds Rust projects in every worktree of the repositories under the
given paths (default: the configured roots), main worktrees included, and runs
cargo clean in each one. Only target directories that cargo made are touched.

A project with a build or another process in it is skipped unless --force is
given. rust-analyzer does not count.`,
		Example: `  metla clean --dry-run
  metla clean --older-than 14d --min-size 500 --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var minAge time.Duration
			if olderThan != "" {
				d, err := parseAge(olderThan)
				if err != nil {
					return usageErr("--older-than: %v", err)
				}
				minAge = d
			}
			d := a.cfg.MaxDepth
			if depth > 0 {
				d = depth
			}
			found, err := discover.Walk(a.roots(args), discover.Options{MaxDepth: d, Skip: a.cfg.Skip})
			if err != nil {
				return err
			}
			busyFn := a.busyFn
			if busyFn == nil {
				busyFn = busy.Snapshot().In
			}
			now := time.Now()
			var targets []cargo.Target
			for _, t := range cargo.Find(ctx, worktreesOf(ctx, found), busyFn, now) {
				if t.SizeKB < int64(minSizeMB)*1024 || (minAge > 0 && now.Sub(t.LastBuilt) < minAge) {
					continue
				}
				targets = append(targets, t)
			}
			if len(targets) == 0 {
				if asJSON {
					return writeJSON(a.stdout, map[string]any{resultsKey: []cleanResult{}})
				}
				sayln(a.stdout, "No Rust target directories to clean.")
				return nil
			}
			if !dryRun && !yes {
				if err := a.confirmClean(targets, force); err != nil {
					return err
				}
			}
			st := a.styles()
			var p *progress
			if !dryRun {
				p = a.newProgress("cleaning", len(targets))
			}
			results := make([]cleanResult, len(targets))
			sem := make(chan struct{}, 4)
			var mu sync.Mutex
			var wg sync.WaitGroup
			finished := 0
			for i, t := range targets {
				wg.Go(func() {
					sem <- struct{}{}
					defer func() { <-sem }()
					r := cleanOne(ctx, t, dryRun, force)
					mu.Lock()
					defer mu.Unlock()
					results[i] = r
					finished++
					if p != nil {
						line := ""
						if !asJSON {
							line = cleanLine(st, r)
						}
						p.step(finished, line)
					}
				})
			}
			wg.Wait()
			failed := false
			for _, r := range results {
				failed = failed || r.Status == statusFailed
			}
			switch {
			case asJSON:
				if err := writeJSON(a.stdout, map[string]any{resultsKey: results}); err != nil {
					return err
				}
			case dryRun:
				a.printClean(results)
			default:
				p.end()
				sayln(a.stdout)
				sayf(a.stdout, "freed about %s in %s\n", humanKB(freedKB(results)), p.elapsed().Round(100*time.Millisecond))
			}
			if failed {
				return &ExitError{Code: ExitFailed, Err: fmt.Errorf("cargo clean failed in some projects")}
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.IntVar(&depth, "depth", 0, "directory levels to search below each root (default from config, 3)")
	fl.StringVar(&olderThan, "older-than", "", "only projects not built for at least this long, e.g. 14d")
	fl.IntVar(&minSizeMB, "min-size", 1, "only target directories of at least this many MB")
	fl.BoolVarP(&dryRun, "dry-run", "n", false, "show what would be cleaned, change nothing")
	fl.BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	fl.BoolVarP(&force, "force", "f", false, "also clean projects with a process working in them")
	fl.BoolVar(&asJSON, "json", false, "print results as JSON")
	cmd.ValidArgsFunction = completeDirs
	_ = cmd.RegisterFlagCompletionFunc("depth", noFiles)
	_ = cmd.RegisterFlagCompletionFunc("min-size", noFiles)
	_ = cmd.RegisterFlagCompletionFunc("older-than", completeAges)
	return cmd
}

func cleanOne(ctx context.Context, t cargo.Target, dryRun, force bool) cleanResult {
	r := cleanResult{Target: t, Status: statusCleaned}
	switch {
	case len(t.Busy) > 0 && !force:
		r.Status = statusSkipped
		r.Error = fmt.Sprintf("in use by %s (pid %d); pass --force", t.Busy[0].Command, t.Busy[0].PID)
	case dryRun:
		r.Status = statusWouldClean
	default:
		if err := cargo.Clean(ctx, t); err != nil {
			r.Status, r.Error = statusFailed, err.Error()
		}
	}
	return r
}

func cleanLine(st style.Styles, r cleanResult) string {
	line := fmt.Sprintf("%-8s %6s  %s", r.Status, humanKB(r.SizeKB), tilde(r.Dir))
	if r.Error != "" {
		line += st.Muted.Render("  (" + r.Error + ")")
	}
	if r.Status == statusFailed {
		line = st.Err.Render(line)
	}
	return line
}

func freedKB(results []cleanResult) int64 {
	var kb int64
	for _, r := range results {
		if r.Status == statusCleaned || r.Status == statusWouldClean {
			kb += r.SizeKB
		}
	}
	return kb
}

// worktreesOf lists every worktree of the repositories, main ones included.
func worktreesOf(ctx context.Context, found discover.Result) []cargo.Worktree {
	lists := make([][]cargo.Worktree, len(found.Repos))
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for i, r := range found.Repos {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			list, err := gitx.ListWorktrees(ctx, r.Dir)
			if err != nil {
				return
			}
			name := strings.TrimSuffix(lastElem(r.Dir), ".git")
			for _, w := range list {
				if !w.Bare && !w.Prunable {
					lists[i] = append(lists[i], cargo.Worktree{Repo: name, Path: w.Path})
				}
			}
		})
	}
	wg.Wait()
	var out []cargo.Worktree
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

func lastElem(p string) string {
	if i := strings.LastIndexByte(strings.TrimRight(p, "/"), '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (a *app) confirmClean(ts []cargo.Target, force bool) error {
	if !a.tty(a.stderr) || a.stdin == nil {
		return refusedErr("not a terminal; pass --yes to clean without confirmation, or --dry-run")
	}
	var n int
	var kb int64
	for _, t := range ts {
		if len(t.Busy) == 0 || force {
			n++
			kb += t.SizeKB
		}
	}
	if n == 0 {
		return nil
	}
	sayf(a.stderr, "Run cargo clean in %d %s, freeing about %s? [y/N] ",
		n, style.Plural(n, "project", "projects"), humanKB(kb))
	return a.readYes()
}

func (a *app) printClean(results []cleanResult) {
	st := a.styles()
	rows := make([][]string, 0, 1+len(results))
	rows = append(rows, []string{"STATUS", "SIZE", "BUILT", "REPO", "PROJECT", ""})
	for _, r := range results {
		rows = append(rows, []string{r.Status, humanKB(r.SizeKB), style.Age(r.AgeDays) + " ago", r.Repo, tilde(r.Dir), st.Muted.Render(r.Error)})
	}
	writeTable(a.stdout, rows, st)
	sayln(a.stdout)
	sayf(a.stdout, "would free about %s\n", humanKB(freedKB(results)))
}
