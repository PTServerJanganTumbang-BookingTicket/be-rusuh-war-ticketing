#!/usr/bin/env bash
#
# Re-reads the secret-derived environment variables of the deployed Lambdas
# straight from Secrets Manager and updates the functions in place.
#
#   usage:  ./infra/refresh-lambda-secrets.sh            # dry run
#           ./infra/refresh-lambda-secrets.sh --yes      # apply
#           ENV=prod REGION=ap-southeast-1 ...  --yes
#
# Why this exists, and why it is not a workaround:
#
# CloudFormation does NOT re-resolve a {{resolve:secretsmanager:...}} dynamic
# reference unless the resource is being updated for some other reason. The
# resolved value plays no part in change detection. So rotating a secret
# changes nothing about a running Lambda - it keeps the value baked in at its
# last deploy, with no error and nothing in the console to suggest it is
# stale.
#
# The pipeline handles this by passing DeployRevision=<commit sha>, which
# makes every deploy a real change. This script is the out-of-band equivalent:
# use it after rotating a secret, or when a deploy is not available.
#
# It writes OUTSIDE CloudFormation, so the stack's idea of these values goes
# stale. That self-corrects: the next deploy that updates the function
# re-resolves the references and writes whatever Secrets Manager holds then.

set -euo pipefail

ENV="${ENV:-prod}"
REGION="${REGION:-ap-southeast-1}"
APPLY=false
[ "${1:-}" = "--yes" ] && APPLY=true

command -v aws >/dev/null || { echo "aws CLI not found"; exit 1; }

# function : env var : secret name : json key ("-" for a bare-string secret)
MAP="
nik-verify:DATABASE_URL:database-url:url
nik-verify:NIK_PEPPER:nik-pepper:-
nik-verify:DUKCAPIL_API_KEY:dukcapil-api-key:api_key
user:DATABASE_URL:database-url:url
user:JWT_SECRET:jwt-signing:-
outbox-publisher:DATABASE_URL:database-url:url
"

echo "environment : $ENV"
echo "region      : $REGION"
$APPLY || echo "MODE        : dry run. Re-run with --yes to apply."
echo

changed_fns=""
for svc in nik-verify user outbox-publisher; do
  fn="rusuh-${ENV}-${svc}"

  current=$(aws lambda get-function-configuration --function-name "$fn" --region "$REGION" \
              --query 'Environment.Variables' --output json 2>/dev/null || true)
  if [ -z "$current" ] || [ "$current" = "null" ]; then
    echo "$svc: not deployed, skipping"
    continue
  fi

  updates=""
  while IFS=: read -r s var secret key; do
    [ "$s" = "$svc" ] || continue
    [ -n "${s:-}" ] || continue

    raw=$(aws secretsmanager get-secret-value --secret-id "rusuh-${ENV}/${secret}" \
            --region "$REGION" --query SecretString --output text 2>/dev/null || true)
    [ -n "$raw" ] || { echo "  $var: secret rusuh-${ENV}/${secret} unreadable, skipping"; continue; }

    if [ "$key" = "-" ]; then
      want="$raw"
    else
      want=$(printf '%s' "$raw" | python3 -c "import sys,json; print(json.load(sys.stdin)['$key'])")
    fi
    updates="${updates}${var}"$'\x1f'"${want}"$'\x1e'
  done <<< "$(printf '%s' "$MAP" | grep -v '^$')"

  # Merge into the EXISTING variables. update-function-configuration replaces
  # the whole map, so anything left out would be deleted - which for these
  # functions means losing DUKCAPIL_MODE, REDIS_ADDR and the rest.
  merged=$(PYTHON_UPDATES="$updates" python3 -c '
import json, os, sys
cur = json.load(sys.stdin)
ups = {}
for pair in os.environ["PYTHON_UPDATES"].split("\x1e"):
    if not pair: continue
    k, v = pair.split("\x1f", 1)
    ups[k] = v
diff = {k: v for k, v in ups.items() if cur.get(k) != v}
cur.update(ups)
print(json.dumps({"changed": sorted(diff), "vars": cur}))
' <<< "$current")

  difflist=$(printf '%s' "$merged" | python3 -c "import sys,json; print(','.join(json.load(sys.stdin)['changed']))")

  if [ -z "$difflist" ]; then
    echo "$svc: already current"
    continue
  fi

  echo "$svc: stale -> $difflist"
  changed_fns="$changed_fns $fn"
  if $APPLY; then
    payload=$(printf '%s' "$merged" | python3 -c "import sys,json; print(json.dumps({'Variables': json.load(sys.stdin)['vars']}))")
    aws lambda update-function-configuration --function-name "$fn" --region "$REGION" \
      --environment "$payload" --query 'LastModified' --output text
    aws lambda wait function-updated --function-name "$fn" --region "$REGION"
    echo "  updated."
  fi
done

echo
if [ -z "$changed_fns" ]; then
  echo "Nothing to do - every function already matches Secrets Manager."
elif $APPLY; then
  echo "Updated:$changed_fns"
  echo "Each function cold-starts on its next invocation and picks up the new values."
else
  echo "Dry run only. Re-run with --yes to apply."
fi
