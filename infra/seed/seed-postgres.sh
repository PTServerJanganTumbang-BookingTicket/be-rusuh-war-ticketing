#!/usr/bin/env bash
# Seed Postgres with sample events via Lambda.
# Usage: ./seed-postgres.sh [prod|dev]

set -euo pipefail
ENV="${1:-prod}"
REGION="${AWS_REGION:-ap-southeast-1}"
FUNCTION="rusuh-${ENV}-seed"

echo "Seeding Postgres via $FUNCTION..."

aws lambda invoke \
  --function-name "$FUNCTION" \
  --region "$REGION" \
  --cli-binary-format raw-in-base64-out \
  --payload '{}' \
  /tmp/seed-postgres-out.json > /dev/null

echo ""
python3 -c "
import json
data = json.load(open('/tmp/seed-postgres-out.json'))
if 'errorMessage' in data:
    print('ERROR:', data['errorMessage'])
    exit(1)
print('✓ Seeded', data.get('events', '?'), 'events to Postgres')
print('  Message:', data.get('message', ''))
"
