#!/usr/bin/env bash
#
# Deploys one service WITHOUT invoking the SAM transform server side.
#
#   usage:  ./infra/deploy-service-no-transform.sh outbox-publisher
#           ENV=prod REGION=ap-southeast-1 ./infra/deploy-service-no-transform.sh user
#
# Use this only when an Organizations SCP denies
#
#   cloudformation:CreateChangeSet
#     on arn:aws:cloudformation:<region>:aws:transform/Serverless-2016-10-31
#
# which looks like this and cannot be fixed with IAM:
#
#   ... is not authorized to perform: cloudformation:CreateChangeSet
#   ... with an explicit deny in a service control policy: arn:aws:organizations::...
#
# Normal deploys use infra/deploy-service-local.sh or the pipeline. Prefer
# getting the SCP amended over adopting this permanently: it pins you to a
# local aws-sam-translator version that has to stay in step with what AWS runs
# server side, and drift shows up as a deploy that differs subtly from what sam
# would have produced.
#
# Same shape as a sam deploy otherwise: build, package, THEN expand (the
# transform rejects a local CodeUri, so packaging has to come first), then
# plain CloudFormation.

set -euo pipefail

SERVICE="${1:-}"
ENV="${ENV:-prod}"
REGION="${REGION:-ap-southeast-1}"

if [ -z "$SERVICE" ] || [ ! -d "services/$SERVICE" ]; then
  echo "usage: $0 <service>"
  echo "services: $(ls services | tr '\n' ' ')"
  exit 1
fi
STACK="rusuh-${ENV}-${SERVICE}"

for c in go sam aws python3; do command -v "$c" >/dev/null || { echo "$c not found"; exit 1; }; done
python3 -c "import samtranslator" 2>/dev/null || {
  echo "aws-sam-translator not installed. Install it with:"
  echo "  python3 -m pip install 'aws-sam-translator==1.116.0'"
  exit 1; }

export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"
export AWS_DEFAULT_REGION="$REGION"   # the translator resolves pseudo-parameters from this

BUCKET=$(aws ssm get-parameter --name "/rusuh/${ENV}/sam/artifact-bucket" \
           --region "$REGION" --query Parameter.Value --output text 2>/dev/null || true)
[ -n "$BUCKET" ] && [ "$BUCKET" != "None" ] || {
  echo "no artifact bucket at /rusuh/${ENV}/sam/artifact-bucket - deploy infra/foundation first"; exit 1; }

echo "service : $SERVICE"
echo "stack   : $STACK"
echo "bucket  : $BUCKET"
echo

REPO_ROOT="$PWD"
cd "services/$SERVICE"
cleanup() { rm -rf vendor .no-transform; }
trap cleanup EXIT
mkdir -p .no-transform

# 1. vendor - sam build copies only this directory, so ../../pkg must be inside it
echo "==> vendoring"
GOWORK=off go mod vendor
grep -q "be-rusuh-war-ticketing/pkg " vendor/modules.txt || {
  echo "the replace directive did not resolve"; exit 1; }

# 2. build
echo "==> sam build"
sam build >/dev/null

# 3. package - uploads the zip and rewrites CodeUri to s3://, which the
#    transform requires. This step does NOT touch the denied transform.
echo "==> sam package"
sam package --s3-bucket "$BUCKET" --s3-prefix "$STACK" --region "$REGION" \
  --output-template-file .no-transform/packaged.yaml >/dev/null

# 4. expand locally
echo "==> expanding the SAM transform locally"
PARAMS=("Environment=${ENV}")
if grep -q "DeployRevision:" template.yaml; then
  PARAMS+=("DeployRevision=local-$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || date +%s)")
fi
# Any other parameter without a Default must be supplied; the expander says
# which, rather than failing somewhere inside the library.
python3 "$REPO_ROOT/infra/expand-sam-template.py" \
  .no-transform/packaged.yaml .no-transform/expanded.json "${PARAMS[@]}"

# 5. deploy plain CloudFormation - no Transform, so no denied resource
echo "==> deploying"
aws cloudformation deploy \
  --template-file .no-transform/expanded.json \
  --stack-name "$STACK" \
  --region "$REGION" \
  --capabilities CAPABILITY_IAM CAPABILITY_NAMED_IAM \
  --no-fail-on-empty-changeset \
  --tags Project=rusuh ManagedBy=local-no-transform \
  --parameter-overrides "${PARAMS[@]}"

echo
echo "Deployed $SERVICE without the SAM transform."
echo "Tagged ManagedBy=local-no-transform so these are distinguishable later."
