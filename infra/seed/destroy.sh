#!/usr/bin/env bash
# Delete the seed stack.
# Usage: ./destroy.sh [prod|dev]

set -euo pipefail
ENV="${1:-prod}"
REGION="${AWS_REGION:-ap-southeast-1}"
STACK="rusuh-${ENV}-seed"

echo "Deleting $STACK..."
aws cloudformation delete-stack --stack-name "$STACK" --region "$REGION"
aws cloudformation wait stack-delete-complete --stack-name "$STACK" --region "$REGION"
echo "✓ Deleted."
