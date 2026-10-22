# Changelog

## v0.1.0 - 2026-10-22

This first release of metla helps you remove finished git worktrees from the command line or a terminal interface. Nix users can install metla as a package or with the Home Manager module.

### New features

- metla scans every repository under the configured roots and gives each worktree a verdict: safe, review, unsafe, or keep. It removes the finished worktrees, so you free disk space without manual work.
