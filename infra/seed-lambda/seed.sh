#!/usr/bin/env bash
# Invoke the seed Lambda.
# Usage:
#   ./seed.sh           # upsert seed data (safe to run multiple times)
#   ./seed.sh --reset   # delete Redis keys first, then seed

set -euo pipefail
ENV="${SEED_ENV:-prod}"
REGION="${AWS_REGION:-ap-southeast-1}"
FUNCTION="rusuh-${ENV}-seed"

PAYLOAD='{}'
if [ "${1:-}" = "--reset" ]; then
  PAYLOAD='{"reset":true}'
  echo "Reset mode: will delete Redis event keys before seeding"
fi

echo "Invoking $FUNCTION..."

aws lambda invoke \
  --function-name "$FUNCTION" \
  --region "$REGION" \
  --cli-binary-format raw-in-base64-out \
  --payload "$PAYLOAD" \
  /tmp/seed-out.json > /dev/null

echo ""
cat /tmp/seed-out.json | python3 -m json.tool 2>/dev/null || cat /tmp/seed-out.json
echo ""
