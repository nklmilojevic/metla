package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/nklmilojevic/metla/internal/config"
	"github.com/nklmilojevic/metla/internal/remove"
	"github.com/nklmilojevic/metla/internal/scan"
	"github.com/nklmilojevic/metla/internal/style"
	"github.com/nklmilojevic/metla/internal/tui"
)

func newScanCmd(a *app) *cobra.Command {
	f := &scanFlags{}
	var asJSON, quiet, all bool
	cmd := &cobra.Command{
		Use:   "scan [path...]",
		Short: "List worktrees and their verdicts (read-only)",
		Long: `scan lists every linked worktree of the repositories under the given paths
(default: the configured roots, ~/dev) with a verdict and the reasons for it.
It never changes anything.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			keep, err := f.filter()
			if err != nil {
				return err
			}
			report, err := a.scan(cmd.Context(), a.roots(args), f, asJSON || quiet)
			if err != nil {
				return err
			}
			if !all && len(f.verdicts) == 0 {
				prev := keep
				keep = func(w scan.Worktree) bool { return !w.Primary && prev(w) }
			}
			report = filterReport(report, keep)
			switch {
			case asJSON:
				return writeJSON(a.stdout, report)
			case quiet:
				for _, w := range report.All() {
					sayln(a.stdout, w.Path)
				}
				return nil
			}
			a.printReport(report, f.size)
			return nil
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the full report as JSON")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only worktree paths, one per line")
	cmd.Flags().BoolVar(&all, "all", false, "include main worktrees")
	return cmd
}

// resultsKey holds the per-item results in remove and prune JSON.
const resultsKey = "results"

func filterReport(r scan.Report, keep func(scan.Worktree) bool) scan.Report {
	out := scan.Report{}
	for _, repo := range r.Repos {
		ws := []scan.Worktree{}
		for _, w := range repo.Worktrees {
			if keep(w) {
				ws = append(ws, w)
			}
		}
		repo.Worktrees = ws
		out.Repos = append(out.Repos, repo)
	}
	for _, w := range r.Orphans {
		if keep(w) {
			out.Orphans = append(out.Orphans, w)
		}
	}
	return out
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *app) styles() style.Styles {
	if !a.tty(a.stdout) {
		return style.Plain()
	}
	return style.New(lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
}

func (a *app) printReport(r scan.Report, withSize bool) {
	st := a.styles()
	ws := r.All()
	scan.Sort(ws)
	if len(ws) == 0 {
		sayln(a.stdout, "No linked worktrees found.")
	} else {
		header := []string{"VERDICT", "AGE", "REPO", "BRANCH", "PATH", "REASONS"}
		if withSize {
			header = append(header[:2], append([]string{"SIZE"}, header[2:]...)...)
		}
		rows := make([][]string, 0, 1+len(ws))
		rows = append(rows, header)
		for _, w := range ws {
			row := []string{st.Verdict(w.Verdict), style.Age(w.AgeDays), w.Repo, branch(w), tilde(w.Path), strings.Join(w.Reasons, ", ")}
			if withSize {
				row = append(row[:2], append([]string{humanKB(w.SizeKB)}, row[2:]...)...)
			}
			rows = append(rows, row)
		}
		writeTable(a.stdout, rows, st)
	}
	sayln(a.stdout)
	sayln(a.stdout, summary(r, ws))
	if n, repos := prunableCount(r); n > 0 {
		sayln(a.stdout, st.Muted.Render(fmt.Sprintf(
			"%d stale %s for worktrees already deleted, in %d %s. Run `metla prune` to clear them.",
			n, style.Plural(n, "entry", "entries"), repos, style.Plural(repos, "repository", "repositories"))))
	}
	for _, repo := range r.Repos {
		if repo.Error != "" {
			sayln(a.stderr, st.Err.Render(tilde(repo.Dir)+": "+repo.Error))
		}
	}
}

// writeTable pads columns by display width, so styled cells still line up.
// The last column is not padded.
func writeTable(w io.Writer, rows [][]string, st style.Styles) {
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], lipgloss.Width(cell))
		}
	}
	for r, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			if r == 0 {
				cell = st.Muted.Render(cell)
			}
			b.WriteString(cell)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-lipgloss.Width(cell)+2))
			}
		}
		sayln(w, strings.TrimRight(b.String(), " "))
	}
}

func summary(r scan.Report, ws []scan.Worktree) string {
	counts := map[scan.Verdict]int{}
	for _, w := range ws {
		counts[w.Verdict]++
	}
	parts := []string{}
	for _, v := range scan.Verdicts {
		if counts[v] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[v], v))
		}
	}
	s := fmt.Sprintf("%d %s in %d %s",
		len(ws), style.Plural(len(ws), "worktree", "worktrees"), len(r.Repos), style.Plural(len(r.Repos), "repository", "repositories"))
	if len(parts) > 0 {
		s += ": " + strings.Join(parts, ", ")
	}
	return s
}

func prunableCount(r scan.Report) (entries, repos int) {
	for _, repo := range r.Repos {
		if len(repo.Prunable) > 0 {
			entries += len(repo.Prunable)
			repos++
		}
	}
	return entries, repos
}

func branch(w scan.Worktree) string {
	switch {
	case w.Orphan:
		return "?"
	case w.Branch == "":
		if len(w.Head) >= 8 {
			return "(" + w.Head[:8] + ")"
		}
		return "(detached)"
	}
	return w.Branch
}

func humanKB(kb int64) string {
	switch {
	case kb >= 1<<20:
		return fmt.Sprintf("%.1fG", float64(kb)/(1<<20))
	case kb >= 1<<10:
		return fmt.Sprintf("%.0fM", float64(kb)/(1<<10))
	}
	return fmt.Sprintf("%dK", kb)
}

func newRemoveCmd(a *app) *cobra.Command {
	f := &scanFlags{}
	var in []string
	var dryRun, yes, force, deleteBranch, asJSON bool
	cmd := &cobra.Command{
		Use:   "remove [worktree...]",
		Short: "Remove worktrees by path, or every worktree with a --verdict",
		Long: `remove deletes worktrees with git worktree remove. Name them by path, or
select them with --verdict (and --older-than) across --in directories
(default: the configured roots).

Unsafe worktrees are removed only with --force. Kept ones never are. Branches
stay unless --delete-branch is given, and then only branches metla proved
merged are deleted.`,
		Example: `  metla remove --verdict safe --dry-run
  metla remove --verdict safe --older-than 14d --yes
  metla remove ~/dev/personal/thurm-sidebar`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if len(args) > 0 && (len(f.verdicts) > 0 || len(in) > 0) {
				return usageErr("name worktrees by path or select them with --verdict, not both")
			}
			if len(args) == 0 && len(f.verdicts) == 0 {
				return usageErr("name worktrees to remove, or select them with --verdict safe")
			}
			for _, v := range f.verdicts {
				if scan.Verdict(v) == scan.Keep {
					return usageErr("keep worktrees are never removed")
				}
			}
			keep, err := f.filter()
			if err != nil {
				return err
			}

			var targets []scan.Worktree
			if len(args) > 0 {
				targets, err = a.byPath(cmd, args, f)
			} else {
				var report scan.Report
				report, err = a.scan(ctx, a.roots(in), f, asJSON)
				targets = filterReport(report, keep).All()
			}
			if err != nil {
				return err
			}
			opts := remove.Options{Force: force, DeleteBranch: deleteBranch, DryRun: dryRun, IgnoreDirty: a.cfg.IgnoreDirty}
			if len(targets) == 0 {
				if asJSON {
					return writeJSON(a.stdout, map[string]any{resultsKey: []remove.Result{}})
				}
				sayln(a.stdout, "Nothing to remove.")
				return nil
			}
			if !dryRun && !yes {
				if err := a.confirm(targets, force); err != nil {
					return err
				}
			}
			st := a.styles()
			verb := "removing"
			if dryRun {
				verb = "checking"
			}
			p := a.newProgress(verb, len(targets))
			opts.Progress = func(done, total int, r remove.Result) {
				if asJSON {
					p.step(done, "")
				} else {
					p.step(done, resultLine(st, r))
				}
			}
			results := remove.Worktrees(ctx, targets, opts)
			p.end()
			if asJSON {
				if err := writeJSON(a.stdout, map[string]any{resultsKey: results}); err != nil {
					return err
				}
			} else {
				sayln(a.stdout)
				sayln(a.stdout, removeSummary(results, p.elapsed()))
			}
			for _, r := range results {
				if r.Status == remove.Failed {
					return &ExitError{Code: ExitFailed, Err: fmt.Errorf("some worktrees could not be removed")}
				}
			}
			return nil
		},
	}
	f.register(cmd)
	fl := cmd.Flags()
	fl.StringSliceVar(&in, "in", nil, "with --verdict, search these directories instead of the configured roots")
	fl.BoolVarP(&dryRun, "dry-run", "n", false, "show what would be removed, change nothing")
	fl.BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	fl.BoolVarP(&force, "force", "f", false, "also remove unsafe worktrees, losing their uncommitted work")
	fl.BoolVar(&deleteBranch, "delete-branch", false, "also delete each removed worktree's branch, when metla proved it merged")
	fl.BoolVar(&asJSON, "json", false, "print results as JSON")
	_ = cmd.RegisterFlagCompletionFunc("in", completeDirs)
	return cmd
}

// byPath scans the repositories the named worktrees belong to and returns
// those worktrees.
func (a *app) byPath(cmd *cobra.Command, paths []string, f *scanFlags) ([]scan.Worktree, error) {
	abs := make([]string, len(paths))
	for i, p := range paths {
		x, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		if r, err := filepath.EvalSymlinks(x); err == nil {
			x = r
		}
		abs[i] = x
	}
	// Each path is a worktree itself, so nothing below it needs searching.
	only := *f
	only.depth, only.exactDepth = 0, true
	report, err := a.scan(cmd.Context(), abs, &only, true)
	if err != nil {
		return nil, err
	}
	byPath := map[string]scan.Worktree{}
	for _, w := range report.All() {
		byPath[w.Path] = w
	}
	var out []scan.Worktree
	for i, p := range abs {
		w, ok := byPath[p]
		if !ok {
			return nil, usageErr("%s is not a worktree", paths[i])
		}
		if w.Verdict == scan.Keep {
			return nil, refusedErr("%s is kept: %s", paths[i], strings.Join(w.Reasons, ", "))
		}
		out = append(out, w)
	}
	return out, nil
}

func (a *app) confirm(ws []scan.Worktree, force bool) error {
	if !a.tty(a.stderr) || a.stdin == nil {
		return refusedErr("not a terminal; pass --yes to remove without confirmation, or --dry-run")
	}
	st := style.New(lipgloss.HasDarkBackground(os.Stdin, os.Stderr))
	n := 0
	for _, w := range ws {
		if w.Removable(force) {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	sayf(a.stderr, "Remove %d %s?\n", n, style.Plural(n, "worktree", "worktrees"))
	for _, w := range ws {
		if w.Removable(force) {
			sayf(a.stderr, "  %s  %s  %s\n", st.Verdict(w.Verdict), tilde(w.Path), st.Muted.Render(strings.Join(w.Reasons, ", ")))
		}
	}
	say(a.stderr, "[y/N] ")
	return a.readYes()
}

// readYes reads one answer line and accepts y or yes.
func (a *app) readYes() error {
	line, _ := bufio.NewReader(a.stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return ErrAborted
}

func resultLine(st style.Styles, r remove.Result) string {
	line := fmt.Sprintf("%-12s %s", r.Status, tilde(r.Path))
	if r.BranchDeleted {
		line += st.Muted.Render("  (branch " + r.Branch + " deleted)")
	}
	if r.Error != "" {
		line += st.Muted.Render("  (" + r.Error + ")")
	}
	if r.Status == remove.Failed {
		line = st.Err.Render(line)
	}
	return line
}

func removeSummary(results []remove.Result, took time.Duration) string {
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Status]++
	}
	var parts []string
	for _, status := range []string{remove.Removed, remove.WouldRemove, remove.Skipped, remove.Failed} {
		if n := counts[status]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, status))
		}
	}
	return strings.Join(parts, ", ") + fmt.Sprintf(" in %s", took.Round(100*time.Millisecond))
}

func newPruneCmd(a *app) *cobra.Command {
	f := &scanFlags{}
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "prune [path...]",
		Short: "Clear git's entries for worktrees whose directory is already gone",
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := a.scan(cmd.Context(), a.roots(args), f, asJSON)
			if err != nil {
				return err
			}
			results := remove.Prune(cmd.Context(), report.Repos, dryRun)
			if asJSON {
				return writeJSON(a.stdout, map[string]any{resultsKey: results})
			}
			if len(results) == 0 {
				sayln(a.stdout, "No stale worktree entries.")
				return nil
			}
			verb := "pruned"
			if dryRun {
				verb = "would prune"
			}
			failed := false
			for _, r := range results {
				line := fmt.Sprintf("%s %d %s in %s", verb, len(r.Entries), style.Plural(len(r.Entries), "entry", "entries"), tilde(r.Repo))
				if r.Error != "" {
					line += " (" + r.Error + ")"
					failed = true
				}
				sayln(a.stdout, line)
			}
			if failed {
				return &ExitError{Code: ExitFailed, Err: fmt.Errorf("git worktree prune failed")}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&f.depth, "depth", 0, "directory levels to search below each root (default from config, 3)")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would be pruned, change nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print results as JSON")
	cmd.ValidArgsFunction = completeDirs
	_ = cmd.RegisterFlagCompletionFunc("depth", noFiles)
	return cmd
}

func newTUICmd(a *app) *cobra.Command {
	f := &scanFlags{}
	cmd := &cobra.Command{
		Use:   "tui [path...]",
		Short: "Pick worktrees to remove in an interactive list",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.tty(a.stdout) {
				return usageErr("tui needs a terminal; use scan and remove instead")
			}
			keep, err := f.filter()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			roots := a.roots(args)
			load := func() (scan.Report, error) {
				r, err := a.scan(ctx, roots, f, true)
				if err != nil {
					return r, err
				}
				return filterReport(r, func(w scan.Worktree) bool { return !w.Primary && keep(w) }), nil
			}
			return tui.Run(ctx, tui.Deps{
				Title: strings.Join(mapStrings(roots, tilde), ", "),
				Load:  load,
				Remove: func(ws []scan.Worktree, force, deleteBranch bool, progress func(done, total int)) []remove.Result {
					return remove.Worktrees(ctx, ws, remove.Options{
						Force: force, DeleteBranch: deleteBranch, IgnoreDirty: a.cfg.IgnoreDirty,
						Progress: func(done, total int, _ remove.Result) { progress(done, total) },
					})
				},
				Prune: func(repos []scan.Repo) []remove.PruneResult { return remove.Prune(ctx, repos, false) },
				Tilde: tilde,
			})
		},
	}
	f.register(cmd)
	return cmd
}

func mapStrings(in []string, f func(string) string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = f(s)
	}
	return out
}

func newConfigCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Show where the config lives and what it resolves to", ValidArgsFunction: noFiles}
	cmd.AddCommand(
		&cobra.Command{
			Use:               "path",
			Short:             "Print the config file path",
			Args:              cobra.NoArgs,
			ValidArgsFunction: noFiles,
			RunE: func(*cobra.Command, []string) error {
				p, err := config.Path()
				if err != nil {
					return err
				}
				sayln(a.stdout, p)
				return nil
			},
		},
		&cobra.Command{
			Use:               "show",
			Short:             "Print the resolved config, defaults included",
			Args:              cobra.NoArgs,
			ValidArgsFunction: noFiles,
			RunE: func(*cobra.Command, []string) error {
				return toml.NewEncoder(a.stdout).Encode(a.cfg)
			},
		},
	)
	return cmd
}
