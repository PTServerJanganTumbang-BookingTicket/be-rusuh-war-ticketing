#!/usr/bin/env bash
#
# Redis CLI wrapper via Lambda.
# Usage: ./redis.sh <COMMAND> [args...]
#
# Note: Quote arguments containing * or {} to prevent shell expansion:
#   ./redis.sh KEYS '*'
#   ./redis.sh HGETALL 'queue:{evt_jkt_istora_nightfall}'
#
# Examples:
#   ./redis.sh PING
#   ./redis.sh DBSIZE
#   ./redis.sh KEYS '*'
#   ./redis.sh GET mykey
#   ./redis.sh SET mykey myvalue
#   ./redis.sh SETEX mykey 3600 myvalue
#   ./redis.sh DEL mykey
#   ./redis.sh HGETALL 'queue:{evt_jkt_istora_nightfall}'
#   ./redis.sh HGET 'queue:{evt_jkt_istora_nightfall}' state
#   ./redis.sh HSET 'queue:{evt_jkt_gbk_garuda_fest}' state OPEN
#   ./redis.sh INFO keyspace

set -euo pipefail

ENV="${REDIS_ENV:-prod}"
REGION="${AWS_REGION:-ap-southeast-1}"
FUNCTION="rusuh-${ENV}-redis-debug"

if [ $# -eq 0 ]; then
  echo "Usage: ./redis.sh <COMMAND> [args...]"
  echo ""
  echo "Examples:"
  echo "  ./redis.sh PING"
  echo "  ./redis.sh KEYS '*'"
  echo "  ./redis.sh GET mykey"
  echo "  ./redis.sh SET mykey myvalue"
  echo "  ./redis.sh DBSIZE"
  echo "  ./redis.sh HGETALL 'queue:{evt_jkt_istora_nightfall}'"
  echo "  ./redis.sh HSET 'queue:{evt_jkt_gbk_garuda_fest}' state OPEN"
  exit 1
fi

# Build JSON payload using python to handle special characters safely
PAYLOAD=$(python3 -c "
import json, sys
args = sys.argv[1:]
cmd = ' '.join(args)
print(json.dumps({'cmd': cmd}))
" "$@")

# Invoke Lambda
aws lambda invoke \
  --function-name "$FUNCTION" \
  --region "$REGION" \
  --cli-binary-format raw-in-base64-out \
  --payload "$PAYLOAD" \
  /tmp/redis-debug-out.json > /dev/null

# Pretty print result
python3 -c "
import json
data = json.load(open('/tmp/redis-debug-out.json'))
if 'error' in data:
    print('ERROR:', data['error'])
    exit(1)
result = data.get('result', data)
if isinstance(result, list):
    for item in result:
        print(item if item is not None else '(nil)')
elif result is None:
    print('(nil)')
else:
    print(result)
"
