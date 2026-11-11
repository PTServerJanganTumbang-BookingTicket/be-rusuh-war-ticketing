# 02-accounts › tickets

What a customer sees of a ticket once it exists: their list, and one e-ticket.
Apidog groups these under *Accounts › Tickets* (previously a top-level *User*
folder).

**Service**: `services/user` (Lambda + API Gateway) — **reads only.**
booking-service writes `tickets` when payment-api confirms a booking
(`docs/07-payment/05-payment-webhook.md` §8); nothing here creates, changes or
revokes one.

**Sources**: api-contracts §6.1, §6.2 · ADR-005 §8 · requirements FR-26, FR-27,
FR-32c, FR-32d.

## Files

| # | File | Apidog request | Status |
|---|---|---|---|
| 01 | [01-get-my-tickets.md](01-get-my-tickets.md) | `GET /users/me/tickets` | done |
| 02 | [02-get-eticket.md](02-get-eticket.md) | `GET /tickets/{ticket_id}/eticket` | **partial** — the `?t=` token path is missing |

## 1. One ownership rule, in one place

Both endpoints bind a ticket to its caller with the same SQL clause, and
neither re-checks afterwards in Go:

```sql
  (t.purchaser_account_id = $1
   OR t.nik_hmac = (SELECT nik_hmac FROM accounts WHERE id = $1 AND nik_hmac IS NOT NULL))
```

Two ways to own a ticket — you bought it, or your own registered NIK holds it —
and `saved_niks` is deliberately **not** a third. Joining through it would put
a stranger's purchase into your list the moment you saved that NIK, and make
its e-ticket readable by you. It also keeps FR-32d honest: removing a saved NIK
changes nothing here, because neither query touches that table.

Keeping the rule in the `WHERE` clause rather than in a check after the read is
what makes "someone else's ticket" and "no such ticket" the same answer: a 404,
never a 403, because a 403 confirms the id is real (§9).

## 2. Nothing here holds an identity

- The read model carries `nik_masked` and **not** `nik_hmac`. Nothing in these
  views needs the hash, and not carrying it means it cannot reach a response by
  accident (FR-32c).
- `qr_value` is `{ticket_id}.{qr_nonce}` — an opaque reference, not the holder.
  The gate resolves the ticket from it and matches the attendee's physical KTP
  against the stored hash.
- There is no name field, here or anywhere in this service: the account stores
  none (FR-15a). The only `name` on these wires is the **event**'s.

## 3. Open decisions

| # | Decision |
|---|---|
| 1 | **OPEN:** the `?t={eticket_token}` path of §6.2 is not implemented, so a forwarded e-ticket link answers 401 for anyone who is not the account owner — which is the delivery path FR-26 depends on. Steps in [02-get-eticket.md](02-get-eticket.md) §7 |
| 2 | **OPEN:** `qr_code` repeats `qr_value` instead of the `data:image/png;base64,…` §6.2 shows. Either render one or drop the field from the contract |
| 3 | **OPEN:** the §6.2 example `qr_value` embeds a full unmasked NIK, contradicting FR-32c. The implementation deliberately differs; the contract example needs correcting |
| 4 | **OPEN:** `limit` is capped at 50, which the contract does not state — it gives only the default of 10. Worth writing into §6.1 |
| 5 | **OPEN:** no ticket is ever marked `USED`. The `tickets_status` CHECK allows it and the `past` filter already reads it, but gate check-in is out of POC scope (ADR-005 §8), so nothing writes it |
