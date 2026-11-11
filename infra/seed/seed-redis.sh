#!/usr/bin/env bash
#
# Seed Redis with the Jakarta sample events via Lambda.
#
# Usage:
#   ./seed-redis.sh           # seed only (won't overwrite existing keys)
#   ./seed-redis.sh --reset   # delete event keys first, then seed

set -euo pipefail

ENV="${REDIS_ENV:-prod}"
REGION="${AWS_REGION:-ap-southeast-1}"
FUNCTION="rusuh-${ENV}-redis-debug"
RESET="${1:-}"

# Helper: run Redis command via Lambda
redis_cmd() {
  local cmd="$1"
  aws lambda invoke \
    --function-name "$FUNCTION" \
    --region "$REGION" \
    --cli-binary-format raw-in-base64-out \
    --payload "$(python3 -c "import json; print(json.dumps({'cmd': '''$cmd'''}))")" \
    /tmp/redis-seed-out.json > /dev/null 2>&1
  
  python3 -c "
import json
data = json.load(open('/tmp/redis-seed-out.json'))
if 'error' in data:
    print('ERROR:', data['error'])
    exit(1)
print(data.get('result', ''))
"
}

# Helper: run multiple commands
redis_cmds() {
  local cmds_json="$1"
  aws lambda invoke \
    --function-name "$FUNCTION" \
    --region "$REGION" \
    --cli-binary-format raw-in-base64-out \
    --payload "{\"cmds\": $cmds_json}" \
    /tmp/redis-seed-out.json > /dev/null 2>&1
}

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Redis Seeding"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Verify connection
echo "Testing Redis connection..."
result=$(redis_cmd "PING")
if [ "$result" != "PONG" ]; then
  echo "ERROR: PING failed: $result"
  exit 1
fi
echo "✓ Connected"
echo ""

# Event definitions (mirrors pkg/seed/data.go)
declare -A EVENTS=(
  ["evt_jkt_istora_nightfall"]="Nightfall Live di Istora|OPEN|2026-11-10T03:00:00Z"
  ["evt_jkt_gbk_garuda_fest"]="Garuda Stadium Fest|SCHEDULED|2026-12-01T03:00:00Z"
  ["evt_jkt_tim_wayang"]="Malam Wayang Kontemporer|FULL_BOOKED|2026-11-09T03:00:00Z"
  ["evt_jkt_ancol_sunset"]="Ancol Sunset Sessions|OPEN|2026-11-10T03:00:00Z"
  ["evt_jkt_jcc_creator_expo"]="Jakarta Creator Expo|CLOSED|2026-09-01T03:00:00Z"
)

declare -A CATEGORIES=(
  ["evt_jkt_istora_nightfall"]="cat_istora_vip:300 cat_istora_festival:3000 cat_istora_tribune:3000"
  ["evt_jkt_gbk_garuda_fest"]="cat_garuda_vip:1000 cat_garuda_cat1:8000 cat_garuda_cat2:20000"
  ["evt_jkt_tim_wayang"]="cat_wayang_vip:0 cat_wayang_reguler:0"
  ["evt_jkt_ancol_sunset"]="cat_ancol_vip:200 cat_ancol_festival:2500"
  ["evt_jkt_jcc_creator_expo"]="cat_expo_oneday:0"
)

# Reset if requested
if [ "$RESET" = "--reset" ]; then
  echo "Resetting existing keys..."
  for evt_id in "${!EVENTS[@]}"; do
    redis_cmd "DEL queue:{$evt_id}" > /dev/null
    redis_cmd "DEL by_ticket:{$evt_id}" > /dev/null
    for cat_entry in ${CATEGORIES[$evt_id]}; do
      cat_id="${cat_entry%%:*}"
      redis_cmd "DEL stock:{$evt_id}:$cat_id" > /dev/null
    done
  done
  echo "✓ Reset complete"
  echo ""
fi

NOW_UNIX=$(date +%s)

# Seed each event
echo "Seeding events..."
for evt_id in "${!EVENTS[@]}"; do
  IFS='|' read -r name state sale_starts <<< "${EVENTS[$evt_id]}"
  
  # Build commands
  cmds="["
  cmds+="\"HSETNX queue:{$evt_id} state $state\","
  cmds+="\"HSETNX queue:{$evt_id} issued 0\","
  cmds+="\"HSETNX queue:{$evt_id} now_serving 0\","
  cmds+="\"HSETNX queue:{$evt_id} name $name\","
  cmds+="\"HSETNX queue:{$evt_id} sale_starts_at $sale_starts\""
  
  if [ "$state" = "FULL_BOOKED" ]; then
    cmds+=",\"HSETNX queue:{$evt_id} full_booked_at $NOW_UNIX\""
  fi
  
  for cat_entry in ${CATEGORIES[$evt_id]}; do
    cat_id="${cat_entry%%:*}"
    available="${cat_entry##*:}"
    cmds+=",\"SETNX stock:{$evt_id}:$cat_id $available\""
  done
  
  cmds+="]"
  redis_cmds "$cmds"
  echo "  ✓ $evt_id ($state)"
done

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
result=$(redis_cmd "DBSIZE")
echo "  ✓ Done. Total keys: $result"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
