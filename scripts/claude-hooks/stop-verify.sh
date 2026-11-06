#!/usr/bin/env bash
# Claude Code Stop hook. See docs/DEVELOPMENT-FLOW.md §5.
#
# Runs `make verify-fast` and BLOCKS the turn from ending while it fails, so the
# agent has to fix what it broke before handing back.
#
# Cheap when nothing relevant changed: it fingerprints HEAD plus the content of every
# changed .go / .feature / go.mod / go.sum / Makefile / docker-compose.yml file and
# skips when that fingerprint matches the last green run.
#
# Loop guard: when Claude is already continuing because of this hook
# (stop_hook_active = true), it does not block a second time.
set -uo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
input="$(cat)"

[ "$(printf '%s' "$input" | jq -r '.stop_hook_active // false')" = "true" ] && exit 0

gitdir="$(git -C "$repo" rev-parse --absolute-git-dir 2>/dev/null)" || exit 0
stamp="$gitdir/claude-verify-fast.ok"

changed="$(git -C "$repo" status --porcelain --untracked-files=all \
  | sed -E 's/^.{3}//; s/.* -> //' \
  | grep -E '(\.go|\.feature|(^|/)go\.(mod|sum)|(^|/)Makefile|(^|/)docker-compose\.yml)$' \
  | sort)"
[ -n "$changed" ] || exit 0

fingerprint="$(
  {
    git -C "$repo" rev-parse HEAD 2>/dev/null
    while IFS= read -r p; do
      printf '%s\n' "$p"
      [ -f "$repo/$p" ] && cat "$repo/$p"
    done <<< "$changed"
  } | sha256sum | cut -d' ' -f1
)"
[ -f "$stamp" ] && [ "$(cat "$stamp")" = "$fingerprint" ] && exit 0

log="$(mktemp)"
if make -C "$repo" --no-print-directory verify-fast >"$log" 2>&1; then
  printf '%s\n' "$fingerprint" > "$stamp"
  rm -f "$log"
  exit 0
fi

reason="make verify-fast FAILED in be-rusuh-war-ticketing. Fix it before finishing (do not claim the work is done). Last lines:
$(grep -vE '^(ok|\?) |^@draft|^approved ' "$log" | tail -40)"
rm -f "$log"
jq -n --arg r "$reason" '{decision: "block", reason: $r}'
exit 0
