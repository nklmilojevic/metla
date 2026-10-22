package gitx

import (
	"reflect"
	"testing"
)

func TestParseWorktrees(t *testing.T) {
	out := "worktree /r\x00HEAD aaa\x00branch refs/heads/main\x00\x00" +
		"worktree /r-feat\x00HEAD bbb\x00branch refs/heads/feat/x\x00locked claude agent (pid 1)\x00\x00" +
		"worktree /r-det\x00HEAD ccc\x00detached\x00locked\x00\x00" +
		"worktree /gone\x00HEAD ddd\x00branch refs/heads/gone\x00prunable gitdir file points to non-existent location\x00\x00"
	got := ParseWorktrees(out)
	want := []Worktree{
		{Path: "/r", Head: "aaa", Branch: "main"},
		{Path: "/r-feat", Head: "bbb", Branch: "feat/x", Locked: true, LockReason: "claude agent (pid 1)"},
		{Path: "/r-det", Head: "ccc", Detached: true, Locked: true},
		{Path: "/gone", Head: "ddd", Branch: "gone", Prunable: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseBare(t *testing.T) {
	got := ParseWorktrees("worktree /r.git\x00bare\x00\x00")
	if len(got) != 1 || !got[0].Bare {
		t.Errorf("got %+v", got)
	}
}
