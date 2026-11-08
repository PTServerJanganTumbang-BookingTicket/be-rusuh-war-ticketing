# admission-ctl

The one process that lets customers in from the queue. Every 500 ms, per event: finish half-done admissions
(RECOVER), count who is inside in Aurora (COUNT), run `advance_now_serving.lua` (ADVANCE), write each GRANTED booking
and grant (FINISH), then publish the news on `sse-broadcast` / `sse-status`. It also lapses unused grants (SWEEP).
Plan: [`docs/04-queue/05-admission-controller.md`](../../docs/04-queue/05-admission-controller.md).

## Run it locally

From the repository root (`be-rusuh-war-ticketing/`):

```bash
make deps-up                                   # Redis cluster :16379 + Postgres :15432
for f in migrations/*.up.sql; do               # idempotent; skip if already applied
  PGPASSWORD=rusuh-local-only psql -h 127.0.0.1 -p 15432 -U rusuh -d rusuh -q -f "$f"
done

# seed one sale (sale-ops does not exist yet, so by hand). Redis runs in cluster mode: use -c.
rcli() { docker compose exec -T redis redis-cli -c "$@"; }
rcli HSET 'queue:{evt_local}' state OPEN now_serving 0 issued 0
rcli SET  'stock:{evt_local}:VIP'  50
rcli SET  'stock:{evt_local}:CAT1' 50

cd services/admission-ctl
cp example.env .env                            # first time only
go run ./cmd/worker
```

Watch the news it publishes (second terminal):

```bash
docker compose exec -T redis redis-cli SUBSCRIBE sse-broadcast sse-status
```

Put someone in line by hand, like join does (token hash, a place in `by_ticket`, `issued` + 1), and watch them get in:

```bash
rcli() { docker compose exec -T redis redis-cli -c "$@"; }
N=$(rcli HINCRBY 'queue:{evt_local}' issued 1)
rcli HSET token:qt-demo ticket_no "$N" account_id acc-demo event_id evt_local
rcli ZADD 'queue:{evt_local}:by_ticket' "$N" qt-demo
# within ~0.5 s:
rcli HGETALL token:qt-demo                       # now holds grant_token, booking_id, booking_expires_at
rcli HGET 'queue:{evt_local}' now_serving
```

`bookings.account_id` references `accounts`, so for a logged-in demo customer insert an account first, or use a guest
token (`device_hash` instead of `account_id`).

## Configuration

See `example.env`. `DATABASE_URL` and `REDIS_ADDR` are required; the worker refuses to start otherwise.

**Which sales it ticks:** none are configured. Every `EVENT_REFRESH_SECONDS` (default 30) the worker scans Redis for `queue:{ev}`
hashes that have a `state` and are not `CLOSED`, and reads each sale's stock categories from the `stock:{ev}:<category>` keys.
So a sale you seed below is picked up on the first tick (the first scan runs immediately), and a sale seeded later within 30 s.

## Tests

```bash
make test-module MODULE=services/admission-ctl   # unit + integration (needs make deps-up)
make step STEP=3                                 # the frozen Gherkin features for the tick
```
