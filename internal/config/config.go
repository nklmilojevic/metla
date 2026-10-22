// Package config loads ~/.config/metla/config.toml. Flags override the file,
// and METLA_ROOTS overrides its roots.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nklmilojevic/metla/internal/discover"
	"github.com/nklmilojevic/metla/internal/scan"
)

// DefaultMaxDepth reaches ~/dev/<org>/<repo> with room to spare.
const DefaultMaxDepth = 3

// Config is the file's content, with defaults filled in.
type Config struct {
	// Roots are the directories scanned when no path is given.
	Roots []string `toml:"roots"`
	// MaxDepth is how many directory levels below a root to search.
	MaxDepth int `toml:"max_depth"`
	// Skip are directory names never entered, on top of the built-in ones.
	Skip []string `toml:"skip"`
	// ProtectBranches are branch globs whose worktrees are always kept.
	ProtectBranches []string `toml:"protect_branches"`
	// IgnoreDirty are file name globs whose changes do not count as dirty.
	IgnoreDirty []string `toml:"ignore_dirty"`
	// PRs looks up pull requests with gh for branches git cannot prove merged.
	PRs bool `toml:"prs"`
	// PatchLimit bounds squash and rebase detection; see scan.DefaultPatchLimit.
	PatchLimit int `toml:"patch_limit"`
}

// Path returns where the config file lives: $METLA_CONFIG, else
// $XDG_CONFIG_HOME/metla/config.toml, else ~/.config/metla/config.toml.
func Path() (string, error) {
	if p := os.Getenv("METLA_CONFIG"); p != "" {
		return p, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "metla", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "metla", "config.toml"), nil
}

// Load reads the config file, which may be missing.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	c := &Config{}
	md, err := toml.DecodeFile(path, c)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("%s: %w", path, err)
	default:
		if keys := md.Undecoded(); len(keys) > 0 {
			return nil, fmt.Errorf("%s: unknown key %q", path, keys[0].String())
		}
	}
	if env := os.Getenv("METLA_ROOTS"); env != "" {
		c.Roots = filepath.SplitList(env)
	}
	return c, c.fill()
}

func (c *Config) fill() error {
	if len(c.Roots) == 0 {
		c.Roots = []string{"~/dev"}
	}
	for i, r := range c.Roots {
		p, err := Expand(r)
		if err != nil {
			return err
		}
		c.Roots[i] = p
	}
	if c.MaxDepth == 0 {
		c.MaxDepth = DefaultMaxDepth
	}
	if c.MaxDepth < 0 {
		return fmt.Errorf("max_depth must not be negative, got %d", c.MaxDepth)
	}
	c.Skip = append(append([]string{}, discover.DefaultSkip...), c.Skip...)
	if c.ProtectBranches == nil {
		c.ProtectBranches = scan.DefaultProtect
	}
	if c.IgnoreDirty == nil {
		c.IgnoreDirty = scan.DefaultIgnoreDirty
	}
	for _, g := range append(append([]string{}, c.ProtectBranches...), c.IgnoreDirty...) {
		if _, err := filepath.Match(g, ""); err != nil {
			return fmt.Errorf("bad glob %q: %w", g, err)
		}
	}
	if c.PatchLimit == 0 {
		c.PatchLimit = scan.DefaultPatchLimit
	}
	return nil
}

// Expand replaces a leading ~ with the home directory.
func Expand(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}
