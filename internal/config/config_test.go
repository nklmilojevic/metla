package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nklmilojevic/metla/internal/scan"
)

func write(t *testing.T, content string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("METLA_CONFIG", p)
	t.Setenv("METLA_ROOTS", "")
}

func TestDefaults(t *testing.T) {
	t.Setenv("METLA_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("METLA_ROOTS", "")
	t.Setenv("HOME", "/home/u")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Roots, []string{"/home/u/dev"}) || c.MaxDepth != DefaultMaxDepth || c.PatchLimit != scan.DefaultPatchLimit {
		t.Errorf("defaults = %+v", c)
	}
	if !slices.Equal(c.ProtectBranches, scan.DefaultProtect) || !slices.Contains(c.Skip, "node_modules") {
		t.Errorf("defaults = %+v", c)
	}
}

func TestFile(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	write(t, `roots = ["~/code", "/srv/src"]
max_depth = 2
skip = ["third_party"]
protect_branches = ["main", "release/*"]
ignore_dirty = []
prs = true
`)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Roots, []string{"/home/u/code", "/srv/src"}) || c.MaxDepth != 2 || !c.PRs {
		t.Errorf("config = %+v", c)
	}
	if !slices.Contains(c.Skip, "third_party") || !slices.Contains(c.Skip, "node_modules") {
		t.Errorf("skip = %q, want built-ins plus third_party", c.Skip)
	}
	if len(c.IgnoreDirty) != 0 {
		t.Errorf("ignore_dirty = [] must turn the built-in list off, got %q", c.IgnoreDirty)
	}
}

func TestEnvRoots(t *testing.T) {
	write(t, `roots = ["/a"]`)
	t.Setenv("METLA_ROOTS", "/b"+string(os.PathListSeparator)+"/c")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Roots, []string{"/b", "/c"}) {
		t.Errorf("roots = %q", c.Roots)
	}
}

func TestErrors(t *testing.T) {
	for content, want := range map[string]string{
		`root = ["/a"]`:             `unknown key "root"`,
		`max_depth = -1`:            "max_depth must not be negative",
		`protect_branches = ["[x"]`: "bad glob",
		`roots = "not a list"`:      "config.toml",
	} {
		write(t, content)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", content, err, want)
		}
	}
}
