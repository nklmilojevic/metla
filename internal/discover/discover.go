// Package discover walks directory trees for git repositories. It finds
// worktrees from the repository side (git knows where all of them are), so
// the walk only has to find each repository once, through its main checkout,
// a bare repository, or any of its linked worktrees.
package discover

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultSkip are directory names the walk never enters: dependency and
// build trees that are large and never hold repositories worth sweeping.
var DefaultSkip = []string{
	"node_modules", "vendor", "target", "dist", "build", "result",
	"__pycache__", "Pods", "DerivedData",
}

// Options bounds the walk.
type Options struct {
	MaxDepth int      // directory levels below each root; 0 means only the roots
	Skip     []string // directory names never entered
}

// Repo is a repository found on disk.
type Repo struct {
	// CommonDir is the repository's git directory, shared by all worktrees.
	CommonDir string
	// Dir is where to run git: the main worktree, or CommonDir when the
	// repository is bare or its main worktree is gone.
	Dir string
}

// Orphan is a linked worktree whose repository no longer exists.
type Orphan struct {
	Path   string
	GitDir string // where its .git file points
}

// Result lists what a walk found, sorted by path.
type Result struct {
	Repos   []Repo
	Orphans []Orphan
}

// Walk searches roots for repositories. A root may itself be a repository or
// a worktree. Hidden directories below a root are skipped; repositories are
// not descended into.
func Walk(roots []string, opts Options) (Result, error) {
	skip := map[string]bool{}
	for _, s := range opts.Skip {
		skip[s] = true
	}
	repos := map[string]Repo{}
	orphans := map[string]Orphan{}

	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return Result{}, err
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		if _, err := os.Stat(abs); err != nil {
			return Result{}, err
		}
		base := depth(abs)
		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// Unreadable directories are skipped, not fatal.
				if d != nil && d.IsDir() && path != abs {
					return fs.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			name := d.Name()
			if path != abs && (strings.HasPrefix(name, ".") || skip[name]) {
				return fs.SkipDir
			}
			switch kind, r, o := Inspect(path); kind {
			case KindRepo:
				repos[r.CommonDir] = r
				return fs.SkipDir
			case KindOrphan:
				orphans[o.Path] = o
				return fs.SkipDir
			}
			if depth(path)-base >= opts.MaxDepth {
				return fs.SkipDir
			}
			return nil
		})
		if err != nil {
			return Result{}, err
		}
	}

	res := Result{}
	for _, r := range repos {
		res.Repos = append(res.Repos, r)
	}
	for _, o := range orphans {
		res.Orphans = append(res.Orphans, o)
	}
	sort.Slice(res.Repos, func(i, j int) bool { return res.Repos[i].Dir < res.Repos[j].Dir })
	sort.Slice(res.Orphans, func(i, j int) bool { return res.Orphans[i].Path < res.Orphans[j].Path })
	return res, nil
}

func depth(p string) int { return strings.Count(filepath.Clean(p), string(filepath.Separator)) }

// Kind says what Inspect found at a path.
type Kind int

// Kinds of directory Inspect reports.
const (
	KindNone   Kind = iota // not part of a repository
	KindRepo               // a repository, found via a checkout or bare dir
	KindOrphan             // a linked worktree whose repository is gone
)

// Inspect reports whether dir is a checkout, a bare repository, or an
// orphaned worktree. It reads files only and never runs git.
func Inspect(dir string) (Kind, Repo, Orphan) {
	dotgit := filepath.Join(dir, ".git")
	info, err := os.Lstat(dotgit)
	switch {
	case err == nil && info.IsDir():
		// An empty .git husk (a clone that never finished) is not a repo.
		if !exists(filepath.Join(dotgit, "HEAD")) {
			return KindNone, Repo{}, Orphan{}
		}
		return KindRepo, repo(dotgit), Orphan{}
	case err == nil && info.Mode().IsRegular():
		return inspectGitFile(dir, dotgit)
	case isBare(dir):
		return KindRepo, repo(dir), Orphan{}
	}
	return KindNone, Repo{}, Orphan{}
}

// inspectGitFile follows a `gitdir:` file. A linked worktree's git dir has a
// commondir file naming the shared repository; a checkout made with
// --separate-git-dir (or a submodule) points straight at its git dir.
func inspectGitFile(dir, dotgit string) (Kind, Repo, Orphan) {
	b, err := os.ReadFile(dotgit)
	if err != nil {
		return KindNone, Repo{}, Orphan{}
	}
	line, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return KindNone, Repo{}, Orphan{}
	}
	gitdir := strings.TrimSpace(line)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	orphan := Orphan{Path: dir, GitDir: gitdir}
	if _, err := os.Stat(gitdir); err != nil {
		if isLinkedGitDir(gitdir) {
			return KindOrphan, Repo{}, orphan
		}
		return KindNone, Repo{}, Orphan{}
	}
	c, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return KindRepo, repo(gitdir), Orphan{}
	}
	common := strings.TrimSpace(string(c))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	if _, err := os.Stat(filepath.Join(common, "HEAD")); err != nil {
		return KindOrphan, Repo{}, orphan
	}
	return KindRepo, repo(common), Orphan{}
}

// isLinkedGitDir reports whether a gitdir path has the shape of a linked
// worktree's admin dir, <common>/worktrees/<name>.
func isLinkedGitDir(gitdir string) bool {
	return filepath.Base(filepath.Dir(filepath.Clean(gitdir))) == "worktrees"
}

func isBare(dir string) bool {
	for _, p := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			return false
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "config"))
	return err == nil && strings.Contains(string(b), "bare = true")
}

func repo(common string) Repo {
	common = filepath.Clean(common)
	if r, err := filepath.EvalSymlinks(common); err == nil {
		common = r
	}
	r := Repo{CommonDir: common, Dir: common}
	if filepath.Base(common) == ".git" {
		if main := filepath.Dir(common); exists(main) {
			r.Dir = main
		}
	}
	return r
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
