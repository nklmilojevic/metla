// Package gh looks up a branch's pull request with the gh CLI.
package gh

import (
	"context"
	"encoding/json"
	"os/exec"
	"sync"

	"github.com/nklmilojevic/metla/internal/scan"
)

// Lookup asks gh for the newest pull request whose head is branch. It
// returns nil when there is none or gh fails (not installed, not logged in,
// not a GitHub remote).
func Lookup(ctx context.Context, repoDir, branch string) *scan.PR {
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--head", branch, "--state", "all",
		"--json", "number,state,url,headRefOid", "--limit", "1")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parse(out)
}

func parse(out []byte) *scan.PR {
	var prs []struct {
		Number     int    `json:"number"`
		State      string `json:"state"`
		URL        string `json:"url"`
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal(out, &prs); err != nil || len(prs) == 0 {
		return nil
	}
	p := prs[0]
	return &scan.PR{Number: p.Number, State: p.State, URL: p.URL, HeadOid: p.HeadRefOid}
}

// Available reports once whether a gh binary is on PATH.
var Available = sync.OnceValue(func() bool {
	_, err := exec.LookPath("gh")
	return err == nil
})
