#!/usr/bin/env bash
# Claude Code PostToolUse hook (Edit|Write|MultiEdit). See docs/DEVELOPMENT-FLOW.md §5.
#
#   *.go       → gofmt -w the file, then `go build ./...` in its module.
#   *.feature  → `make spec` (parse + tag lint).
#
# Exit 2 + stderr feeds the problem back to Claude (the edit itself stays).
# Files outside this repo, and every other file type, are ignored.
set -uo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
f="$(jq -r '.tool_input.file_path // .tool_response.filePath // empty')"

[ -n "$f" ] || exit 0
case "$f" in "$repo"/*) ;; *) exit 0 ;; esac
[ -f "$f" ] || exit 0

case "$f" in
  *.go)
    if ! out="$(gofmt -w "$f" 2>&1)"; then
      printf 'gofmt failed on %s:\n%s\n' "${f#"$repo"/}" "$out" >&2
      exit 2
    fi
    # Nearest go.mod above the file.
    dir="$(dirname "$f")"
    while [ "$dir" != "$repo" ] && [ ! -f "$dir/go.mod" ]; do dir="$(dirname "$dir")"; done
    [ -f "$dir/go.mod" ] || exit 0
    # Modules outside go.work (e.g. tools/*) must build without the workspace.
    rel="${dir#"$repo"/}"
    gowork=""
    grep -Eq "^[[:space:]]*\./${rel}[[:space:]]*$|^use[[:space:]]+\./${rel}[[:space:]]*$" "$repo/go.work" 2>/dev/null || gowork="off"
    if ! out="$(cd "$dir" && GOWORK="${gowork:-${GOWORK:-}}" go build ./... 2>&1)"; then
      printf 'go build failed in %s after editing %s:\n%s\n' "$rel" "${f#"$repo"/}" "$out" >&2
      exit 2
    fi
    ;;
  *.feature)
    if ! out="$(make -s -C "$repo" spec 2>&1)"; then
      printf 'make spec failed after editing %s:\n%s\n' "${f#"$repo"/}" "$(printf '%s\n' "$out" | grep -E 'FAIL|problems' | tail -20)" >&2
      exit 2
    fi
    ;;
esac
exit 0
