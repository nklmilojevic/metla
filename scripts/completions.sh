#!/bin/sh
# Writes the shell completion scripts GoReleaser puts in each archive.
set -eu
rm -rf completions
mkdir completions
go run ./cmd/metla completion bash > completions/metla.bash
go run ./cmd/metla completion zsh > completions/_metla
go run ./cmd/metla completion fish > completions/metla.fish
