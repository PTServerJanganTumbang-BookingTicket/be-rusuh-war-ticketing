#!/usr/bin/env bash
# Deploy the seed stack (Postgres seed Lambda + Redis debug Lambda).
# Usage: ./deploy.sh [prod|dev]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV="${1:-prod}"
REGION="${AWS_REGION:-ap-southeast-1}"
STACK="rusuh-${ENV}-seed"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Deploying seed stack: $STACK"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Build Go binary for Postgres seed Lambda
echo "Building Postgres seed Lambda..."
cd "$REPO_ROOT/pkg"
GOWORK=off GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -o /tmp/bootstrap ./seed/cmd/lambda/
zip -j /tmp/seed-bootstrap.zip /tmp/bootstrap
echo "  Built: /tmp/seed-bootstrap.zip ($(du -sh /tmp/seed-bootstrap.zip | cut -f1))"

# Get artifact bucket from SSM
echo ""
echo "Getting artifact bucket..."
BUCKET=$(aws ssm get-parameter \
  --name "/rusuh/${ENV}/sam/artifact-bucket" \
  --region "$REGION" \
  --query 'Parameter.Value' \
  --output text 2>/dev/null || true)

if [ -z "$BUCKET" ] || [ "$BUCKET" = "None" ]; then
  echo "ERROR: Artifact bucket not found. Deploy foundation first."
  exit 1
fi
echo "  Bucket: $BUCKET"

# Upload Lambda zip
S3_KEY="seed/bootstrap.zip"
echo ""
echo "Uploading Lambda package..."
aws s3 cp /tmp/seed-bootstrap.zip "s3://$BUCKET/$S3_KEY" --region "$REGION"
echo "  Uploaded to s3://$BUCKET/$S3_KEY"

# Deploy CloudFormation stack
echo ""
echo "Deploying CloudFormation stack..."
aws cloudformation deploy \
  --template-file "$SCRIPT_DIR/template.yaml" \
  --stack-name "$STACK" \
  --capabilities CAPABILITY_IAM CAPABILITY_NAMED_IAM \
  --region "$REGION" \
  --no-fail-on-empty-changeset \
  --parameter-overrides \
    "Environment=$ENV" \
    "LambdaS3Bucket=$BUCKET" \
    "LambdaS3Key=$S3_KEY" \
  --tags Project=rusuh ManagedBy=manual Debug=true

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  ✓ Seed stack deployed"
echo ""
echo "  Seed Postgres:"
echo "    ./seed-postgres.sh"
echo ""
echo "  Seed Redis:"
echo "    ./seed-redis.sh"
echo "    ./seed-redis.sh --reset   # clear keys first"
echo ""
echo "  Redis CLI:"
echo "    ./redis.sh PING"
echo "    ./redis.sh KEYS '*'"
echo "    ./redis.sh HGETALL 'queue:{evt_jkt_istora_nightfall}'"
echo ""
echo "  Delete when done:"
echo "    ./destroy.sh"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
