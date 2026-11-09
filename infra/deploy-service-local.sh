#!/usr/bin/env bash
#
# Deploys one service from your machine, doing exactly what
# .github/actions/sam-deploy does. For when the pipeline is unavailable.
#
#   usage:  ./infra/deploy-service-local.sh outbox-publisher
#           ENV=prod REGION=ap-southeast-1 ./infra/deploy-service-local.sh nik-verify
#
# UPDATES ONLY. It passes just Environment and DeployRevision; every other
# parameter keeps the value the stack already has, because sam sends
# UsePreviousValue for parameters it is not given. That is what makes this
# safe without the repo variables and secrets CI reads - but it also means
# this cannot create a stack from scratch. Use the pipeline for that.

set -euo pipefail

SERVICE="${1:-}"
ENV="${ENV:-prod}"
REGION="${REGION:-ap-southeast-1}"

if [ -z "$SERVICE" ] || [ ! -d "services/$SERVICE" ]; then
  echo "usage: $0 <service>"
  echo "services: $(ls services | tr '\n' ' ')"
  echo "(run from the repository root)"
  exit 1
fi

STACK="rusuh-${ENV}-${SERVICE}"

# ── toolchain ───────────────────────────────────────────────────────────────
command -v go  >/dev/null || { echo "go not found"; exit 1; }
command -v sam >/dev/null || {
  echo "sam not found. Install it with:  brew install aws-sam-cli"
  exit 1; }
command -v aws >/dev/null || { echo "aws CLI not found"; exit 1; }

# go.mod requires a newer toolchain than most machines have installed.
# GOTOOLCHAIN=auto lets go fetch it rather than failing on a version mismatch.
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

echo "service : $SERVICE"
echo "stack   : $STACK"
echo "region  : $REGION"
echo "go      : $(go version | awk '{print $3}')  (toolchain=auto)"
echo

# ── artifact bucket ─────────────────────────────────────────────────────────
BUCKET=$(aws ssm get-parameter --name "/rusuh/${ENV}/sam/artifact-bucket" \
           --region "$REGION" --query Parameter.Value --output text 2>/dev/null || true)
if [ -z "$BUCKET" ] || [ "$BUCKET" = "None" ]; then
  echo "No artifact bucket at /rusuh/${ENV}/sam/artifact-bucket."
  echo "It is created by infra/foundation - deploy that first."
  exit 1
fi
echo "bucket  : $BUCKET"
echo

cd "services/$SERVICE"

# Clean up the vendor directory however this exits. It is gitignored, but
# leaving it behind makes later `go test` runs use a stale copy of pkg/.
cleanup() { rm -rf vendor; }
trap cleanup EXIT

# ── vendor ──────────────────────────────────────────────────────────────────
# sam build copies ONLY this directory to a scratch dir and runs make there,
# so the `replace ... => ../../pkg` in go.mod points outside the copy.
# Vendoring resolves it here, where the path still exists.
echo "vendoring the shared package..."
GOWORK=off go mod vendor
pkg="github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg"
grep -q "^# ${pkg} " vendor/modules.txt || {
  echo "vendor/modules.txt has no entry for ${pkg} - the replace did not resolve."
  exit 1; }
echo "  $(grep -c '^# ' vendor/modules.txt) modules vendored"
echo

# ── build and deploy ────────────────────────────────────────────────────────
sam build

REVISION="local-$(git rev-parse --short HEAD 2>/dev/null || date +%s)-$(date +%H%M%S)"
echo
echo "deploying as DeployRevision=$REVISION"

# DeployRevision is not decoration: CloudFormation does not re-resolve a
# {{resolve:secretsmanager:...}} reference unless the resource changes for
# another reason, so without a value that differs each time, a deploy can
# leave the function running stale secrets. Only services that read secrets
# declare the parameter.
params=("Environment=${ENV}")
if grep -q "DeployRevision:" template.yaml; then
  params+=("DeployRevision=${REVISION}")
fi

sam deploy \
  --stack-name "$STACK" \
  --region "$REGION" \
  --capabilities CAPABILITY_IAM \
  --no-confirm-changeset \
  --no-fail-on-empty-changeset \
  --s3-bucket "$BUCKET" \
  --s3-prefix "$STACK" \
  --tags "Project=rusuh" "ManagedBy=local-deploy" \
  --parameter-overrides "${params[@]}"

echo
echo "Deployed $SERVICE."
echo "Tagged ManagedBy=local-deploy so this is distinguishable from a pipeline deploy."
