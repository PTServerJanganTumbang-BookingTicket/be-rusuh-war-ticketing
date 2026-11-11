#!/usr/bin/env bash
# Build and deploy the seed Lambda.
# Usage: ./deploy.sh [prod|dev]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENV="${1:-prod}"
REGION="${AWS_REGION:-ap-southeast-1}"
STACK="rusuh-${ENV}-seed"

echo "Building seed Lambda..."

# Build Go binary — must run from pkg/ module with GOWORK=off
cd "$REPO_ROOT/pkg"
GOWORK=off GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -o /tmp/bootstrap ./seed/cmd/lambda/
zip -j /tmp/seed-bootstrap.zip /tmp/bootstrap
echo "Built: /tmp/seed-bootstrap.zip ($(du -sh /tmp/seed-bootstrap.zip | cut -f1))"

# Get artifact bucket
BUCKET=$(aws ssm get-parameter \
  --name /rusuh/${ENV}/sam/artifact-bucket \
  --region "$REGION" \
  --query 'Parameter.Value' \
  --output text)
echo "Bucket: $BUCKET"

# Upload
S3_KEY="seed/bootstrap.zip"
aws s3 cp /tmp/seed-bootstrap.zip "s3://$BUCKET/$S3_KEY" --region "$REGION"
echo "Uploaded to s3://$BUCKET/$S3_KEY"

# Deploy stack
echo "Deploying $STACK..."
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
echo "  Seed Lambda deployed: rusuh-${ENV}-seed"
echo ""
echo "  Run seed:"
echo "    ./seed.sh"
echo ""
echo "  Reset Redis + seed:"
echo "    ./seed.sh --reset"
echo ""
echo "  Delete when done:"
echo "    ./destroy.sh"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
