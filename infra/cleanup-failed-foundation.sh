#!/usr/bin/env bash
#
# Clears what a FAILED foundation deploy leaves behind, so the next attempt
# can create the stack from scratch.
#
#   usage:  ./infra/cleanup-failed-foundation.sh            # dry run
#           ./infra/cleanup-failed-foundation.sh --yes      # do it
#           ENV=prod REGION=ap-southeast-1 ... --yes
#
# Why this is needed at all: five resources in infra/foundation carry
# DeletionPolicy: Retain, because losing them is unrecoverable once the system
# is real. On a rollback they survive, their NAMES stay taken, and the retry
# fails with "already exists" - which reads like a new problem rather than
# debris from the last one.
#
# This script is for a foundation that never came up. It is NOT a teardown
# tool: it refuses to run against a healthy stack, and it refuses to delete
# the NIK pepper's key if a pepper was ever stored. Read "Refusals" below
# before adding anything to it.

set -euo pipefail

ENV="${ENV:-prod}"
REGION="${REGION:-ap-southeast-1}"
STACK="rusuh-${ENV}-foundation"
APPLY=false
[ "${1:-}" = "--yes" ] && APPLY=true

say()  { printf '%s\n' "$*"; }
act()  { if $APPLY; then say "  -> $*"; else say "  would: $*"; fi; }

say "stack  : $STACK"
say "region : $REGION"
$APPLY || say "MODE   : dry run. Re-run with --yes to apply."
say

# ── Refusal 1: never touch a stack that is not in a failed state ────────────
#
# Running this against a working foundation would force-delete the database
# credentials and schedule the pepper key for deletion. The stack status is
# the only thing standing between a retry and that, so it is checked first
# and the script exits rather than prompting.
status=$(aws cloudformation describe-stacks --stack-name "$STACK" --region "$REGION" \
          --query 'Stacks[0].StackStatus' --output text 2>/dev/null || echo "ABSENT")
say "status : $status"

case "$status" in
  ABSENT)
    say "  stack does not exist - only leftovers to clear."
    ;;
  *ROLLBACK_FAILED|*ROLLBACK_COMPLETE|CREATE_FAILED|DELETE_FAILED)
    say "  failed state - the stack will be deleted first."
    act "aws cloudformation delete-stack --stack-name $STACK"
    if $APPLY; then
      aws cloudformation delete-stack --stack-name "$STACK" --region "$REGION"
      say "  waiting for delete to complete..."
      aws cloudformation wait stack-delete-complete --stack-name "$STACK" --region "$REGION" \
        || { say "  delete did not complete. Check describe-stack-events and resolve"
             say "  the resource that refused to delete, then re-run."; exit 1; }
      say "  deleted."
    fi
    ;;
  *)
    say
    say "REFUSING: '$status' is not a failed state."
    say "This script force-deletes credentials and is only for a foundation that"
    say "never came up. To tear down a working stack, delete it deliberately with"
    say "  aws cloudformation delete-stack --stack-name $STACK --region $REGION"
    say "and clear the retained resources by hand, knowing what each one holds."
    exit 1
    ;;
esac
say

# ── Secrets whose names block a recreate ────────────────────────────────────
#
# nik-pepper is NOT in this list, on purpose. Everything else the stack
# regenerates on the next deploy; the pepper cannot be regenerated, because
# every NIK hash ever written was derived from it. If a pepper exists, the
# right move is to import it into the new stack, not to delete it - see
# "Refusal 2".
say "secrets that would block a recreate:"
for name in "rusuh-${ENV}/aurora-master" "rusuh-${ENV}/jwt-signing" \
            "rusuh-${ENV}/dukcapil-api-key" "rusuh-${ENV}/database-url"; do
  if aws secretsmanager describe-secret --secret-id "$name" --region "$REGION" >/dev/null 2>&1; then
    act "force-delete secret $name"
    if $APPLY; then
      aws secretsmanager delete-secret --secret-id "$name" --region "$REGION" \
        --force-delete-without-recovery >/dev/null
    fi
  else
    say "  absent: $name"
  fi
done
say

# ── Refusal 2: the NIK pepper ───────────────────────────────────────────────
#
# The pepper is not rotatable (ADR-005 §8). If the secret exists, something
# may already have been hashed with it, and deleting its key destroys every
# stored nik_hmac's meaning - BR-02 ("1 NIK = 1 seat") stops working and
# cannot be repaired. So: if the secret is there, stop and say so. Only an
# orphaned key with NO pepper secret is safe to remove.
pepper="rusuh-${ENV}/nik-pepper"
if aws secretsmanager describe-secret --secret-id "$pepper" --region "$REGION" >/dev/null 2>&1; then
  say "NIK PEPPER EXISTS: $pepper"
  say "  Not deleting it, and not touching its KMS key."
  say "  A pepper cannot be regenerated: every stored nik_hmac derives from it,"
  say "  so deleting the key would break BR-02 permanently."
  say
  say "  If this really is a POC with no data worth keeping, delete both by hand:"
  say "    aws secretsmanager delete-secret --secret-id $pepper --region $REGION \\"
  say "      --force-delete-without-recovery"
  say "  and then schedule its key for deletion. Otherwise leave them and import"
  say "  the secret into the new stack."
  say
  say "Done (pepper left intact)."
  exit 0
fi
say "no pepper secret - its key, if any, is orphaned."
say

# ── Orphaned pepper keys ────────────────────────────────────────────────────
#
# Matched by the description the template sets, and only when the key has no
# alias: the alias is recreated by the next deploy, so a key that still has
# one may be in use by something this script cannot see.
say "orphaned KMS keys (enabled, no alias, rusuh-${ENV} pepper description):"
aliased=$(aws kms list-aliases --region "$REGION" \
            --query 'Aliases[].TargetKeyId' --output text 2>/dev/null | tr '\t' '\n' | sort -u)
found=0
for kid in $(aws kms list-keys --region "$REGION" --query 'Keys[].KeyId' --output text); do
  meta=$(aws kms describe-key --key-id "$kid" --region "$REGION" \
          --query 'KeyMetadata.[Description,KeyState,KeyManager]' --output text 2>/dev/null || true)
  case "$meta" in
    *"rusuh-${ENV} NIK pepper"*) ;;
    *) continue ;;
  esac
  case "$meta" in *PendingDeletion*) say "  already pending deletion: $kid"; continue ;; esac
  if printf '%s\n' "$aliased" | grep -qx "$kid"; then
    say "  has an alias, skipping: $kid"
    continue
  fi
  found=1
  act "schedule-key-deletion $kid (7-day window, reversible until then)"
  if $APPLY; then
    aws kms schedule-key-deletion --key-id "$kid" --region "$REGION" \
      --pending-window-in-days 7 >/dev/null
  fi
done
[ "$found" -eq 1 ] || say "  none found."
say

if $APPLY; then
  say "Cleanup complete. Re-run the workflow to deploy the foundation again."
else
  say "Dry run only. Re-run with --yes to apply."
fi
