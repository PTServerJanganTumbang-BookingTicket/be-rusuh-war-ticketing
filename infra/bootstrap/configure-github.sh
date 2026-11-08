#!/usr/bin/env bash
#
# Sets every GitHub Actions variable and secret the pipeline reads, then
# verifies the result. Idempotent: re-running overwrites rather than appends,
# so this is also the way to change one value later.
#
# Deliberately NOT run by CI. It configures the repository that CI runs in,
# which is a chicken-and-egg CI cannot resolve, and it needs credentials no
# workflow should hold.
#
#   usage:  DUKCAPIL_TOKEN=... ./infra/bootstrap/configure-github.sh
#
# The Apidog token is read from the environment, never hardcoded: this file is
# committed, and a token in git history is a token you have to rotate.

set -euo pipefail

# ── values for this POC ──────────────────────────────────────────────────────
REGION="ap-southeast-1"
FROM_EMAIL="sjt.backend@gmail.com"
APP_BASE_URL="https://ticket.sjt.com"
DB_ALLOWED_CIDR="0.0.0.0/0"

DUKCAPIL_MODE="live"
DUKCAPIL_BASE_URL="https://mock.apidog.com/m1/1409481-1420084-default"
DUKCAPIL_EXTRA_HEADER_NAME="apidogToken"

BOOTSTRAP_STACK="rusuh-cd-bootstrap"
# Region of the bootstrap stack. The role it creates is global, so this only
# has to find the stack - it need not match REGION.
BOOTSTRAP_REGION="${BOOTSTRAP_REGION:-$(aws configure get region || echo "$REGION")}"

# ── preflight ────────────────────────────────────────────────────────────────
command -v gh  >/dev/null || { echo "gh CLI not found"; exit 1; }
command -v aws >/dev/null || { echo "aws CLI not found"; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "gh is not authenticated: run 'gh auth login'"; exit 1; }

REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner)
echo "repository : $REPO"
echo "region     : $REGION"
echo

if [ -z "${DUKCAPIL_TOKEN:-}" ]; then
  # Read, not echoed. An empty answer is allowed: the Apidog token is only
  # needed in live mode, and preflight will refuse that combination loudly
  # rather than silently sending a blank header.
  printf 'Apidog token (DUKCAPIL_EXTRA_HEADER_VALUE), blank to skip: '
  # `|| DUKCAPIL_TOKEN=""` matters: with stdin redirected or closed - a CI
  # runner, a pipe - read returns non-zero on EOF, and under `set -e` that
  # would abort before anything is configured, which looks like a crash
  # rather than "no token given".
  read -rs DUKCAPIL_TOKEN || DUKCAPIL_TOKEN=""
  echo
fi

# ── the deploy role ──────────────────────────────────────────────────────────
echo "reading RoleArn from $BOOTSTRAP_STACK in $BOOTSTRAP_REGION..."
ROLE_ARN=$(aws cloudformation describe-stacks \
  --stack-name "$BOOTSTRAP_STACK" \
  --region "$BOOTSTRAP_REGION" \
  --query 'Stacks[0].Outputs[?OutputKey==`RoleArn`].OutputValue' \
  --output text 2>/dev/null || true)

if [ -z "$ROLE_ARN" ] || [ "$ROLE_ARN" = "None" ]; then
  echo "could not read RoleArn from stack '$BOOTSTRAP_STACK' in '$BOOTSTRAP_REGION'."
  echo "deploy infra/bootstrap/github-oidc-role.yaml first, or set BOOTSTRAP_REGION"
  echo "to the region you created it in."
  exit 1
fi
echo "  $ROLE_ARN"
echo

# ── secrets ──────────────────────────────────────────────────────────────────
echo "secrets:"
gh secret set AWS_DEPLOY_ROLE_ARN --body "$ROLE_ARN"
if [ -n "$DUKCAPIL_TOKEN" ]; then
  gh secret set DUKCAPIL_EXTRA_HEADER_VALUE --body "$DUKCAPIL_TOKEN"
else
  echo "  skipped DUKCAPIL_EXTRA_HEADER_VALUE (no token given)"
fi
echo

# ── variables ────────────────────────────────────────────────────────────────
echo "variables:"
gh variable set AWS_REGION                 --body "$REGION"
gh variable set EMAIL_FROM_ADDRESS         --body "$FROM_EMAIL"
gh variable set APP_BASE_URL               --body "$APP_BASE_URL"
gh variable set DB_ALLOWED_CIDR            --body "$DB_ALLOWED_CIDR"
gh variable set DUKCAPIL_MODE              --body "$DUKCAPIL_MODE"
gh variable set DUKCAPIL_BASE_URL          --body "$DUKCAPIL_BASE_URL"
gh variable set DUKCAPIL_EXTRA_HEADER_NAME --body "$DUKCAPIL_EXTRA_HEADER_NAME"
echo

# SINGLE_AZ, DB_PUBLICLY_ACCESSIBLE and ENABLE_NAT_GATEWAY are left unset on
# purpose - the workflow defaults them to true, which is the POC profile.
# COOKIE_DOMAIN is unset because nothing validates it and an empty value is
# correct until there is a real domain.

# ── the prod environment ─────────────────────────────────────────────────────
# Every deploy job declares `environment: prod`, so without this they all fail.
echo "environment:"
if gh api "repos/$REPO/environments/prod" >/dev/null 2>&1; then
  echo "  prod already exists"
else
  gh api -X PUT "repos/$REPO/environments/prod" >/dev/null
  echo "  created prod"
fi
echo

# ── verify ───────────────────────────────────────────────────────────────────
echo "verifying..."
fail=0
have_var=$(gh variable list --json name -q '.[].name' | tr '\n' ' ')
have_sec=$(gh secret   list --json name -q '.[].name' | tr '\n' ' ')

for v in AWS_REGION EMAIL_FROM_ADDRESS APP_BASE_URL DB_ALLOWED_CIDR \
         DUKCAPIL_MODE DUKCAPIL_BASE_URL DUKCAPIL_EXTRA_HEADER_NAME; do
  case " $have_var " in *" $v "*) echo "  ok      var $v" ;;
                        *) echo "  MISSING var $v"; fail=1 ;; esac
done
for s in AWS_DEPLOY_ROLE_ARN; do
  case " $have_sec " in *" $s "*) echo "  ok      sec $s" ;;
                        *) echo "  MISSING sec $s"; fail=1 ;; esac
done
case " $have_sec " in
  *" DUKCAPIL_EXTRA_HEADER_VALUE "*) echo "  ok      sec DUKCAPIL_EXTRA_HEADER_VALUE" ;;
  *) echo "  WARNING sec DUKCAPIL_EXTRA_HEADER_VALUE is unset."
     echo "          DUKCAPIL_MODE=$DUKCAPIL_MODE with a header name and no value is"
     echo "          rejected by the preflight job. Set the secret, or clear"
     echo "          DUKCAPIL_EXTRA_HEADER_NAME." ; fail=1 ;;
esac

echo
[ "$fail" -eq 0 ] || { echo "incomplete - see above"; exit 1; }
echo "step 3 and step 4 complete. Next: commit, then"
echo "  git push origin HEAD:test-deployment"
