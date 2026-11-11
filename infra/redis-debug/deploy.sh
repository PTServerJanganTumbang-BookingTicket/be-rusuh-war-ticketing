#!/usr/bin/env bash
# Deploy the Redis debug Lambda.
# Usage: ./deploy.sh [prod|dev]

set -euo pipefail
ENV="${1:-prod}"
REGION="${AWS_REGION:-ap-southeast-1}"
STACK="rusuh-${ENV}-redis-debug"

echo "Deploying $STACK..."

aws cloudformation deploy \
  --template-file "$(dirname "$0")/template.yaml" \
  --stack-name "$STACK" \
  --capabilities CAPABILITY_IAM CAPABILITY_NAMED_IAM \
  --parameter-overrides "Environment=$ENV" \
  --region "$REGION" \
  --no-fail-on-empty-changeset \
  --tags Project=rusuh ManagedBy=manual Debug=true

echo ""
echo "Done. Try it:"
echo ""
echo "  aws lambda invoke \\"
echo "    --function-name rusuh-${ENV}-redis-debug \\"
echo "    --region $REGION \\"
echo "    --cli-binary-format raw-in-base64-out \\"
echo "    --payload '{\"cmd\":\"PING\"}' \\"
echo "    /dev/stdout"
echo ""
echo "Delete when done: ./destroy.sh"
