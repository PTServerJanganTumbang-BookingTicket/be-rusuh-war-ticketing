# notification

Consumes the notification queue and sends email through SES. The last hop of
`outbox → SQS → SES` (ADR-005 §4, §9 step 4).

Owns the queue and its DLQ — see `template.yaml` outputs, which
`outbox-publisher` takes as inputs.

## Event types

| Type | Goes to | Requirement |
|---|---|---|
| `auth.magic_link` | the address that asked to sign in | contract §1.1 |
| `account.verify` | the new account's address | FR-31 |
| `ticket.issue` | **each NIK holder**, one email per seat | FR-26, FR-27 |
| `booking.expired` | the buyer | FR-28 |
| `payment.failed_notice` | the buyer | FR-28a, FR-28b |

`auth.magic_link` is not in ADR-005 §9's list. The ADR covers
`account.verify` (registration), but contract §1.1 also specifies a
passwordless login link, which needs the same channel. **Worth adding to the
ADR's type table** so the next reader does not treat it as an invention —
api-contracts.md Appendix C carries the same note.

`payment.failed_notice` renders three different bodies, because FR-28a names
three conditions and they are not interchangeable:

- gateway declined → "you have not been charged"
- TTL plus grace elapsed with no callback → "you have not been charged"
- **disputed** → "your funds were received and a refund is being processed"

Telling the third customer that payment did not complete would be false and
alarming. FR-28b makes that email mandatory, not a log line.

## What a return value means

`Deliver` returning an error is what puts a message back on the queue, so the
distinction is load-bearing:

| Returns | Meaning | SQS does |
|---|---|---|
| `nil` | delivered, duplicate, or unroutable | deletes the message |
| error | transient — SES throttled, network | redelivers; DLQ after 5 attempts |

Unroutable means permanent: an unknown type (a producer deployed ahead of this
consumer), a malformed payload, or no recipient. Those return `nil`. Returning
an error would make them poison messages — redelivered five times to reach the
DLQ anyway, having burned every retry.

Per-message failures are reported through `ReportBatchItemFailures`. Without
it, one failing message in a batch of ten redelivers all ten and nine
recipients get a duplicate.

## Deduplication

`email:sent:{outbox_id}` in Redis, `SET NX` with a 7-day TTL.

Needed because SQS is at-least-once and the publisher's lease guard does not
prevent a repeat after a crash mid-row (ADR-005 §9 step 3). SES has no
deduplication of its own.

The claim is taken **before** sending and released if the send fails. Claiming
after would let two concurrent deliveries both pass the check; not releasing on
failure would make the retry look like a duplicate and drop it — losing the
message precisely because the first attempt failed.

If Redis is unavailable the email is sent anyway. A duplicate is a nuisance; a
missing e-ticket is a customer who cannot get into the venue.

## Templates

Indonesian and English (FR-32), selected by `locale` on the payload so the
language is decided where the customer's preference is known. Indonesian is the
default.

HTML bodies go through `html/template`, which escapes interpolated values —
event names and ticket categories originate in user input and would otherwise
be an injection vector in the recipient's inbox. Every email carries a text
part too: it is what makes the mail readable in clients that strip HTML, and
text-only mail scores better with spam filters.

Emailed links point at `APP_BASE_URL`, the **frontend**. The verify endpoint
reads its token from a cookie (contract §1.3), so the app route sets the cookie
and then calls the API; a link straight to the API would 400.

## Configuration

| Variable | Required | Notes |
|---|---|---|
| `EMAIL_FROM_ADDRESS` | yes | Must be a verified SES identity in the region. |
| `APP_BASE_URL` | yes | Frontend base. Validated as https outside local — these links carry single-use login tokens. |
| `REDIS_ADDR` | yes | Dedupe store. |
| `SES_CONFIGURATION_SET` | no, but | Routes bounces and complaints to SNS. Without it a bouncing address is invisible and SES eventually throttles the account. |
| `MAGIC_TOKEN_TTL_MINUTES` | no | 15. Mirrors `entity.MagicTokenTTL` in `services/user`; if that changes, change this too. |

## No HTTP server

SQS-driven only, so there is no `cmd/server`, no `make run` and no
`Dockerfile` — there is no long-running binary to containerise, which is also
why this service is left out of the hadolint trigger in CI.

To exercise it locally, invoke the handler with `sam local invoke` or call
`Deliver` from a test.
