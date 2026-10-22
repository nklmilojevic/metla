# metla

metla finds the git worktrees that you do not need now, and removes them. It
also frees the disk space that Rust builds use.

metla examines all the repositories in your code directories. It gives each
worktree a verdict, and it shows the reasons for that verdict. metla does not
remove a worktree until you tell it to.

```
$ metla scan
VERDICT  AGE  REPO  BRANCH        PATH                                REASONS
unsafe   9d   app   feat/motion   ~/dev/app/.claude/worktrees/motion  1 uncommitted change
safe     5d   app   fix/notify    ~/dev/app-notify                    squash-merged
safe     1d   api   fix/keys      ~/dev/org/api-keys                  no commits of its own
review   1d   api   fix/batch     ~/dev/org/api-batch                 not merged

4 worktrees in 2 repositories: 1 unsafe, 2 safe, 1 review
```

## Why

The command `git worktree prune` removes only the records of worktrees that
are already deleted. It cannot tell you which live worktrees are finished.
Coding agents make many worktrees, in many places. metla finds all of them and
tells you which ones you can remove safely.

## Install

Nix:

```sh
nix run github:nklmilojevic/metla -- scan
nix profile install github:nklmilojevic/metla
```

Home Manager:

```nix
{
  inputs.metla.url = "github:nklmilojevic/metla";

  # In your Home Manager configuration:
  imports = [ inputs.metla.homeManagerModules.default ];
  programs.metla = {
    enable = true;
    settings.roots = [ "~/dev" ];
  };
}
```

Go:

```sh
go install github.com/nklmilojevic/metla/cmd/metla@latest
```

Each [GitHub Release](https://github.com/nklmilojevic/metla/releases) has
binaries for Linux and macOS.

metla needs `git`. The `--prs` option also needs `gh`. The `clean` command
needs `cargo`.

## Usage

```sh
metla scan                          # show all worktrees and their verdicts
metla scan --verdict safe -q        # show only the paths of safe worktrees
metla scan --json                   # show the full report as JSON
metla scan --prs                    # ask GitHub about branches that git cannot prove merged
metla remove --verdict safe -n      # show what metla removes, change nothing
metla remove --verdict safe --older-than 14d --yes
metla remove ~/dev/app-feature      # remove one worktree
metla prune                         # remove the records of deleted worktrees
metla clean -n                      # show the Rust target directories that metla cleans
metla clean --older-than 30d --min-size 500
metla tui                           # select worktrees in a list, then remove them
metla config show                   # show the configuration with all defaults
```

Without a path, metla examines the directories in `roots`. The default is
`~/dev`.

In `metla tui`, use these keys:

| Key     | Action                                              |
| ------- | --------------------------------------------------- |
| `space` | Select or clear the worktree at the cursor.        |
| `a`     | Select all safe worktrees.                          |
| `x`     | Clear all selections.                               |
| `f`     | Let you select unsafe worktrees.                    |
| `b`     | Also delete the branches that metla proved merged. |
| `d`     | Remove the selected worktrees, after you confirm.  |
| `p`     | Remove the records of deleted worktrees.            |
| `r`     | Examine the repositories again.                     |
| `q`     | Stop metla.                                         |

## Verdicts

| Verdict  | Meaning                                                          | Removal                    |
| -------- | ---------------------------------------------------------------- | -------------------------- |
| `safe`   | The worktree is clean. Its work is in the default branch.        | Yes                        |
| `review` | The worktree is clean and pushed. Its work is not merged.        | Only when you select it    |
| `unsafe` | The worktree has work that a removal can lose.                   | Only with `--force`        |
| `keep`   | The worktree is a main worktree, a protected branch, or locked.  | Never                      |

metla gives the `unsafe` verdict for these conditions:

- The worktree has uncommitted changes.
- The branch has commits that are not on a remote and not in the default
  branch.
- A detached HEAD has commits that no branch, tag, or remote has.
- A process has its working directory in the worktree. An idle shell does
  not count.
- The repository of the worktree does not exist now (an orphan).

metla finds merged work in four ways:

- The branch is in the history of the default branch.
- The default branch has a squash commit with the same changes as the branch.
- The default branch has a copy of each commit of the branch (a rebase merge).
- With `--prs`, GitHub shows a merged pull request for the last commit of the
  branch.

A branch can track a different branch, for example `origin/staging`. Then
metla also accepts work that is merged into that branch.

## Safety

- `scan` changes nothing.
- `remove` and `clean` ask before they change anything. Off a terminal, they
  need `--yes`.
- `remove` examines each worktree again just before it removes it. If the
  worktree has new changes, metla does not remove it.
- metla never removes a main worktree, a protected branch, or a lock that a
  live process holds.
- An agent can lock a worktree and then stop. If the process in the lock
  reason is gone, metla removes the lock.
- `remove` keeps the branch. With `--delete-branch`, metla deletes only the
  branches that it proved merged.
- metla ignores changes to lock files, for example `go.sum`, `flake.lock`, and
  `package-lock.json`. These changes alone do not make a worktree dirty.

## How metla finds worktrees

metla searches the roots to a depth of 3 directories. It does not go into
hidden directories, dependency directories, or repositories. For each
repository, `git worktree list` gives all the worktrees, in all locations. Thus
metla also finds worktrees in `~/.cache`, in `/tmp`, and in
`<repo>/.claude/worktrees`.

## Rust target directories

`metla clean` finds Rust projects in all the worktrees, main worktrees
included. It runs `cargo clean` in each project. metla touches only the
`target` directories that have the `CACHEDIR.TAG` file from cargo.

`cargo clean` runs with `--offline`, and rustup does not install a toolchain.
metla does not clean a project that has a build or another process in it. Use
`--force` to clean it. rust-analyzer does not count as a process in the
project.

## Configuration

metla reads `~/.config/metla/config.toml`. The `METLA_CONFIG` variable can
give a different path. The `METLA_ROOTS` variable replaces `roots`. Flags
replace the values in the file.

```toml
roots = ["~/dev", "~/src"]
max_depth = 3
skip = ["third_party"]                 # added to the directories that metla always skips
protect_branches = ["main", "master", "develop", "trunk"]
ignore_dirty = ["go.sum", "flake.lock"]  # replaces the list of lock files
prs = false
patch_limit = 2000                     # the maximum commits for the squash and rebase check
```

## Shell completion

The Nix package and the Home Manager module install the completions for bash,
zsh, and fish. Each release archive has them in `completions/`. To make them
yourself, use `metla completion`:

```sh
metla completion fish > ~/.config/fish/completions/metla.fish
```

The completions include the verdicts, with a description for each one.

## Exit codes

| Code | Meaning                                                         |
| ---- | --------------------------------------------------------------- |
| 0    | metla completed the command.                                    |
| 1    | metla could not complete the command, or a removal failed.      |
| 2    | The command or a flag is not correct.                           |
| 3    | metla refused the command, or you did not confirm it.           |

## Development

```sh
nix develop    # or direnv
just check     # lint, tidy check, and tests
just nix-check # build the Nix package and examine the Home Manager module
```

## License

[MIT](LICENSE)
