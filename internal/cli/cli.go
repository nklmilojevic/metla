// Package cli wires the metla commands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/nklmilojevic/metla/internal/busy"
	"github.com/nklmilojevic/metla/internal/config"
	"github.com/nklmilojevic/metla/internal/discover"
	"github.com/nklmilojevic/metla/internal/gh"
	"github.com/nklmilojevic/metla/internal/scan"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitFailed  = 1
	ExitUsage   = 2
	ExitRefused = 3
)

// ExitError carries the exit code main should use.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

func usageErr(format string, a ...any) error {
	return &ExitError{Code: ExitUsage, Err: fmt.Errorf(format, a...)}
}

func refusedErr(format string, a ...any) error {
	return &ExitError{Code: ExitRefused, Err: fmt.Errorf(format, a...)}
}

// ErrAborted is returned when the user declines a confirmation.
var ErrAborted = &ExitError{Code: ExitRefused, Err: errors.New("aborted")}

// Code maps an error from Execute to an exit code.
func Code(err error) int {
	if err == nil {
		return ExitOK
	}
	if e, ok := errors.AsType[*ExitError](err); ok {
		return e.Code
	}
	return ExitFailed
}

// app holds what every command shares.
type app struct {
	stdout, stderr io.Writer
	stdin          io.Reader
	cfg            *config.Config

	// Set by tests.
	isTTY  func(w io.Writer) bool
	busyFn func(path string) []scan.Proc
	prFn   func(ctx context.Context, repoDir, branch string) *scan.PR
}

// scanFlags are shared by the commands that scan.
type scanFlags struct {
	depth      int
	exactDepth bool // use depth even when it is 0
	fetch      bool
	prs        bool
	noPRs      bool
	size       bool
	verdicts   []string
	olderThan  string
}

func (f *scanFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.IntVar(&f.depth, "depth", 0, "directory levels to search below each root (default from config, 3)")
	fl.BoolVar(&f.fetch, "fetch", false, "git fetch --prune each repository first (slow)")
	fl.BoolVar(&f.prs, "prs", false, "ask gh for pull requests of branches git cannot prove merged")
	fl.BoolVar(&f.noPRs, "no-prs", false, "skip pull request lookups even when the config enables them")
	fl.BoolVar(&f.size, "size", false, "measure each worktree with du")
	fl.StringSliceVar(&f.verdicts, "verdict", nil, "only worktrees with this verdict: safe, review, unsafe, keep (repeatable)")
	fl.StringVar(&f.olderThan, "older-than", "", "only worktrees untouched for at least this long, e.g. 14d or 36h")
	cmd.ValidArgsFunction = completeDirs
	_ = cmd.RegisterFlagCompletionFunc("depth", noFiles)
	_ = cmd.RegisterFlagCompletionFunc("verdict", completeVerdicts)
	_ = cmd.RegisterFlagCompletionFunc("older-than", completeAges)
}

// verdictHelp describes each verdict for shell completion.
var verdictHelp = map[scan.Verdict]string{
	scan.Safe:   "clean, and its work landed in the default branch",
	scan.Review: "clean and pushed, but not merged",
	scan.Unsafe: "uncommitted or unpushed work, or a process in it",
	scan.Keep:   "a main worktree, protected branch or held lock",
}

func completeVerdicts(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	out := make([]cobra.Completion, 0, len(scan.Verdicts))
	for _, v := range scan.Verdicts {
		out = append(out, cobra.CompletionWithDesc(string(v), verdictHelp[v]))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeAges(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return []cobra.Completion{"7d", "14d", "30d", "90d"}, cobra.ShellCompDirectiveNoFileComp
}

func completeDirs(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveFilterDirs
}

func noFiles(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// filter returns the worktree predicate the flags ask for.
func (f *scanFlags) filter() (func(scan.Worktree) bool, error) {
	want := map[scan.Verdict]bool{}
	for _, v := range f.verdicts {
		if !validVerdict(scan.Verdict(v)) {
			return nil, usageErr("unknown verdict %q (want safe, review, unsafe or keep)", v)
		}
		want[scan.Verdict(v)] = true
	}
	var minAge time.Duration
	if f.olderThan != "" {
		d, err := parseAge(f.olderThan)
		if err != nil {
			return nil, usageErr("--older-than: %v", err)
		}
		minAge = d
	}
	return func(w scan.Worktree) bool {
		if len(want) > 0 && !want[w.Verdict] {
			return false
		}
		if minAge > 0 && (w.LastActive.IsZero() || time.Since(w.LastActive) < minAge) {
			return false
		}
		return true
	}, nil
}

func validVerdict(v scan.Verdict) bool {
	return slices.Contains(scan.Verdicts, v)
}

// parseAge accepts Go durations plus a d (day) and w (week) suffix.
func parseAge(s string) (time.Duration, error) {
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if n, ok := strings.CutSuffix(s, suffix); ok {
			v, err := strconv.Atoi(n)
			if err != nil || v < 0 {
				return 0, fmt.Errorf("bad age %q", s)
			}
			return time.Duration(v) * unit, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("bad age %q", s)
	}
	return d, nil
}

// Execute runs the root command.
func Execute(ctx context.Context, version string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	a := &app{stdout: stdout, stderr: stderr, stdin: stdin}
	root := newRoot(a, version)
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}

func newRoot(a *app, version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "metla",
		Short: "Find finished git worktrees across your repositories and sweep them away",
		Long: `metla walks your code directories for git repositories, inspects every
worktree they have, and gives each one a verdict:

  safe    clean, and its work landed in the default branch
  review  clean and pushed, but not merged
  unsafe  uncommitted changes, unpushed commits, or a process working in it
  keep    a main worktree, a protected branch, or a held lock

scan only reports. remove deletes safe and review worktrees you select, and
unsafe ones only with --force. keep is never removed.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       resolveVersion(version),
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Completion and `config path` work with a broken config.
			switch {
			case cmd.Name() == "path", cmd.Name() == "completion", cmd.Name() == cobra.ShellCompRequestCmd,
				cmd.HasParent() && cmd.Parent().Name() == "completion":
				return nil
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			a.cfg = cfg
			return nil
		},
	}
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.SetIn(a.stdin)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &ExitError{Code: ExitUsage, Err: err} })
	root.AddCommand(newScanCmd(a), newRemoveCmd(a), newPruneCmd(a), newTUICmd(a), newCleanCmd(a), newConfigCmd(a))
	return root
}

func resolveVersion(v string) string {
	if v != "" && v != "dev" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func (a *app) tty(w io.Writer) bool {
	if a.isTTY != nil {
		return a.isTTY(w)
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// roots returns the paths to scan: the arguments, or the configured roots.
func (a *app) roots(args []string) []string {
	if len(args) > 0 {
		return args
	}
	return a.cfg.Roots
}

// scan walks roots and inspects what it finds. Progress goes to stderr when
// it is a terminal and quiet is false.
func (a *app) scan(ctx context.Context, roots []string, f *scanFlags, quiet bool) (scan.Report, error) {
	depth := a.cfg.MaxDepth
	if f.depth > 0 || f.exactDepth {
		depth = f.depth
	}
	found, err := discover.Walk(roots, discover.Options{MaxDepth: depth, Skip: a.cfg.Skip})
	if err != nil {
		return scan.Report{}, err
	}
	opts := scan.Options{
		Protect:     a.cfg.ProtectBranches,
		IgnoreDirty: a.cfg.IgnoreDirty,
		PatchLimit:  a.cfg.PatchLimit,
		Fetch:       f.fetch,
		Size:        f.size,
	}
	if (f.prs || a.cfg.PRs) && !f.noPRs {
		opts.PRs = a.prFn
		if opts.PRs == nil && gh.Available() {
			opts.PRs = gh.Lookup
		}
	}
	opts.Busy = a.busyFn
	if opts.Busy == nil {
		opts.Busy = busy.Snapshot().In
	}
	if !quiet && a.tty(a.stderr) {
		opts.Progress = func(done, total int) {
			sayf(a.stderr, "\r\x1b[2Kscanning %d/%d repositories", done, total)
		}
		defer say(a.stderr, "\r\x1b[2K")
	}
	return scan.Run(ctx, found, opts), nil
}

// say, sayf and sayln write output; a failed terminal write has nowhere
// to be reported.
func say(w io.Writer, a ...any)                 { _, _ = fmt.Fprint(w, a...) }
func sayf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
func sayln(w io.Writer, a ...any)               { _, _ = fmt.Fprintln(w, a...) }

// tilde shortens a path under the home directory for display.
func tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~/" + rel
	}
	return p
}
