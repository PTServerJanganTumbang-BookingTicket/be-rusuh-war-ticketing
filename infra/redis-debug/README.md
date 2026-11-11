# Redis Debug Lambda

Run Redis commands against the prod ElastiCache cluster from your local machine — no bastion needed.

## Setup

```bash
# Deploy the Lambda (once)
./deploy.sh
```

## Usage

```bash
# Use redis.sh as a Redis CLI
./redis.sh PING
./redis.sh DBSIZE
./redis.sh KEYS '*'
./redis.sh GET mykey
./redis.sh SET mykey myvalue
./redis.sh SETEX mykey 3600 myvalue
./redis.sh DEL mykey
./redis.sh HGETALL 'queue:{evt_jkt_istora_nightfall}'
./redis.sh HGET 'queue:{evt_jkt_istora_nightfall}' state
./redis.sh INFO keyspace
```

## Example Output

```bash
$ ./redis.sh KEYS '*'
queue:{evt_jkt_ancol_sunset}
queue:{evt_jkt_gbk_garuda_fest}
queue:{evt_jkt_istora_nightfall}
queue:{evt_jkt_jcc_creator_expo}
queue:{evt_jkt_tim_wayang}
stock:{evt_jkt_ancol_sunset}:cat_ancol_festival
stock:{evt_jkt_ancol_sunset}:cat_ancol_vip
...

$ ./redis.sh HGETALL 'queue:{evt_jkt_istora_nightfall}'
state
OPEN
issued
0
now_serving
0
name
Nightfall Live di Istora
sale_starts_at
2026-11-10T03:00:00Z

$ ./redis.sh GET 'stock:{evt_jkt_istora_nightfall}:cat_istora_vip'
300

$ ./redis.sh DBSIZE
17
```

## Multiple Commands

For multiple commands in one call (avoids Lambda cold start per command):

```bash
aws lambda invoke \
  --function-name rusuh-prod-redis-debug \
  --region ap-southeast-1 \
  --cli-binary-format raw-in-base64-out \
  --payload '{"cmds":["DBSIZE","KEYS *"]}' \
  /tmp/out.json > /dev/null && cat /tmp/out.json | python3 -m json.tool
```

## Common Redis Keys in This Project

| Key Pattern | Type | Description |
|---|---|---|
| `queue:{event_id}` | Hash | Queue state: `state`, `issued`, `now_serving`, `name`, `sale_starts_at` |
| `stock:{event_id}:{category_id}` | String | Available ticket count |
| `nik:valid:{hmac}` | String | Cached NIK verification (24h TTL) |
| `acct:{event_id}:{account_id}` | String | Queue token for an account |

## Useful Commands for Debugging

```bash
# Check all event queue states
./redis.sh KEYS 'queue:*'

# Check a specific event
./redis.sh HGETALL 'queue:{evt_jkt_istora_nightfall}'

# Check stock for an event
./redis.sh KEYS 'stock:{evt_jkt_istora_nightfall}*'

# Check NIK cache entries
./redis.sh KEYS 'nik:valid:*'

# Update queue state (e.g. open a sale)
./redis.sh HSET 'queue:{evt_jkt_gbk_garuda_fest}' state OPEN

# Reset stock for a category
./redis.sh SET 'stock:{evt_jkt_istora_nightfall}:cat_istora_vip' 300

# Delete a key
./redis.sh DEL 'queue:{evt_jkt_istora_nightfall}'
```

## Cleanup

```bash
# Delete the Lambda when done
./destroy.sh
```

## Files

| File | Purpose |
|---|---|
| `template.yaml` | CloudFormation stack |
| `deploy.sh` | Deploy the Lambda |
| `destroy.sh` | Delete the Lambda stack |
| `redis.sh` | CLI wrapper — `./redis.sh CMD args` |
