# Changelog

## v0.2.0 - 2026-10-22

This release shows the progress of the `remove` and `clean` commands. You can see each result as it finishes.

### New features

- The `remove` and `clean` commands print each result as it finishes and show a live counter on stderr in a terminal. They run up to four repositories or projects at once, and the TUI shows removal progress.

## v0.1.0 - 2026-10-22

This first release of metla helps you remove finished git worktrees from the command line or a terminal interface. Nix users can install metla as a package or with the Home Manager module.

### New features

- metla scans every repository under the configured roots and gives each worktree a verdict: safe, review, unsafe, or keep. It removes the finished worktrees, so you free disk space without manual work.
