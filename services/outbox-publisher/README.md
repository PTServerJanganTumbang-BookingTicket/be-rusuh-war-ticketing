# outbox-publisher

Drains the transactional `outbox` table onto SQS (ADR-005 §9 step 4).

## Why the outbox exists

Producers write an event row **in the same transaction** as the state change it
describes. That is what makes the two inseparable: an account that exists with
no verification email queued, or an email for an account that rolled back, are
both impossible once they commit together.

Publishing is then a separate, retryable step — which is what lets a broker
outage leave a registration intact (NFR-10).

## One pass

```
claim a batch: WHERE published_at IS NULL ORDER BY created_at, id
               FOR UPDATE SKIP LOCKED LIMIT 500
for each row:
    take the Redis lease, or skip — someone else has it, or it is done
    publish to SQS
    on success remember the id; on failure release the lease
mark the successful ids published, commit
```

Two ordering decisions carry the correctness:

**Publish before marking published.** The reverse loses events: a crash between
the mark and the publish retires a row nobody ever sent. This way a crash
leaves the row unpublished and the next pass retries it — a duplicate at worst,
and the consumer dedupes on `outbox_id`.

**A failed publish does not fail the pass.** One malformed row must not block
every row behind it. Failures are counted, the rest proceed, and `OutboxLag`
reports what is left.

## The lease is at-least-once, not exactly-once

`processed:{outbox_id}` with `SET NX EX 30`. It prevents two workers processing
one row at the same time. It does **not** prevent a repeat after a crash
mid-row (ADR-005 §9 step 3).

The TTL is the point. A permanent flag leaks: a worker that claims a row and
then dies makes every later pass skip it, and the event is lost forever. The
lease expiring is what lets the next pass retry.

The key is shared with `payment-api`, which claims the same rows on its inline
fast path — hence `OutboxInlineHitRate`. Do not rename it in one place.

## Cadence: one second, or one minute?

ADR-005 says every second. Server mode does that. As a scheduled Lambda the
floor is **one minute**, so each invocation drains in a loop inside
`DRAIN_BUDGET_SECONDS` instead, stopping early once a pass publishes nothing.

That is acceptable because this is the **fallback** path: the latency-sensitive
side effects (`payment.confirmed` → `DECR slots:open`) run inline in the
service that owns them. Email can wait a few seconds; nobody notices against
SES and inbox delivery time.

If `OutboxLag` is routinely non-zero between invocations, run this as an ECS
task with `make run` instead of raising the Lambda's concurrency — a second
worker takes a disjoint slice but also takes a database connection.

## Configuration

| Variable | Required | Notes |
|---|---|---|
| `DATABASE_URL` | yes | Shared Aurora. Front it with RDS Proxy. |
| `NOTIFICATION_QUEUE_URL` | yes | Startup fails without it: there is nowhere to publish. |
| `REDIS_ADDR` | yes | Lease guard. |
| `PUBLISH_INTERVAL_MS` | no | 1000 (server mode). |
| `PUBLISH_BATCH_SIZE` | no | 500. |
| `DRAIN_BUDGET_SECONDS` | no | 45 (Lambda mode). Keep under the function timeout. |
| `OUTBOX_LEASE_SECONDS` | no | 30. Validated to be at least the pass interval — a shorter lease lets a second worker claim a row the first is still publishing. |
| `DATABASE_MAX_CONNS` | no | 4. |

## Database

Reads `outbox` and writes only its `published_at`. The rows belong to whichever
service produced them. Owns no table of its own.
