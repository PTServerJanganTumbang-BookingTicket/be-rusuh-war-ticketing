# Seed Stack

Seeds the Rusuh War Ticketing environment with sample data:
- **Postgres** — events and ticket categories (via Go Lambda)
- **Redis** — queue state and stock (via Python Lambda)

Both Lambdas run inside the VPC, so they can reach Aurora and ElastiCache.

## Quick Start

```bash
# Deploy the stack (creates both Lambdas)
./deploy.sh

# Seed Postgres (events catalog)
./seed-postgres.sh

# Seed Redis (queue state + stock)
./seed-redis.sh

# Verify
./redis.sh DBSIZE
./redis.sh KEYS '*'

# Cleanup when done
./destroy.sh
```

## Commands

### Seeding

```bash
# Seed Postgres (safe to run multiple times - upserts)
./seed-postgres.sh

# Seed Redis (uses HSETNX/SETNX - won't overwrite existing)
./seed-redis.sh

# Reset Redis keys first, then seed
./seed-redis.sh --reset
```

### Redis CLI

```bash
# Basic commands
./redis.sh PING
./redis.sh DBSIZE
./redis.sh KEYS '*'
./redis.sh INFO keyspace

# Read data
./redis.sh GET 'stock:{evt_jkt_istora_nightfall}:cat_istora_vip'
./redis.sh HGETALL 'queue:{evt_jkt_istora_nightfall}'
./redis.sh HGET 'queue:{evt_jkt_istora_nightfall}' state

# Write data
./redis.sh SET mykey myvalue
./redis.sh HSET 'queue:{evt_jkt_gbk_garuda_fest}' state OPEN
./redis.sh DEL mykey
```

## Sample Events

| Event ID | Name | State |
|---|---|---|
| `evt_jkt_istora_nightfall` | Nightfall Live di Istora | OPEN |
| `evt_jkt_gbk_garuda_fest` | Garuda Stadium Fest | SCHEDULED |
| `evt_jkt_tim_wayang` | Malam Wayang Kontemporer | FULL_BOOKED |
| `evt_jkt_ancol_sunset` | Ancol Sunset Sessions | OPEN |
| `evt_jkt_jcc_creator_expo` | Jakarta Creator Expo | CLOSED |

## Redis Key Patterns

| Key | Type | Description |
|---|---|---|
| `queue:{event_id}` | Hash | `state`, `issued`, `now_serving`, `name`, `sale_starts_at` |
| `stock:{event_id}:{category_id}` | String | Available ticket count |
| `nik:valid:{hmac}` | String | Cached NIK verification (24h TTL) |
| `acct:{event_id}:{account_id}` | String | Queue token for an account |

## Debugging Tips

```bash
# Check all event states
for evt in evt_jkt_istora_nightfall evt_jkt_gbk_garuda_fest evt_jkt_tim_wayang evt_jkt_ancol_sunset evt_jkt_jcc_creator_expo; do
  echo -n "$evt: "
  ./redis.sh HGET "queue:{$evt}" state
done

# Open a scheduled sale
./redis.sh HSET 'queue:{evt_jkt_gbk_garuda_fest}' state OPEN

# Reset stock for a category
./redis.sh SET 'stock:{evt_jkt_istora_nightfall}:cat_istora_vip' 300

# Check NIK cache
./redis.sh KEYS 'nik:valid:*'
```

## Files

| File | Purpose |
|---|---|
| `template.yaml` | CloudFormation stack (both Lambdas) |
| `deploy.sh` | Build & deploy the stack |
| `destroy.sh` | Delete the stack |
| `seed-postgres.sh` | Seed Postgres via Lambda |
| `seed-redis.sh` | Seed Redis via Lambda |
| `redis.sh` | Redis CLI wrapper |
