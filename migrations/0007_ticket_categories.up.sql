-- 0007_ticket_categories.up.sql
--
-- Ticket categories per event, read by GET /events/{event_id}/tickets (api-contracts §4.1,
-- docs/05-booking/01-get-ticket-selection-page.md). One table holds capacity (impl-plan §2.2).
--
-- capacity  : immutable baseline during a sale; stock reconciliation measures drift against it (ADR-005 §6).
-- available : snapshot for a cold Redis and for rebuild. The live count is stock:{ev}:{cat} in Redis.
-- sort_order: the order the page lists the categories in (VIP, Festival, Tribune), not the id order.

BEGIN;

CREATE TABLE IF NOT EXISTS ticket_categories (
    id            TEXT        PRIMARY KEY,
    event_id      TEXT        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    name          TEXT        NOT NULL,
    description   TEXT        NOT NULL DEFAULT '',
    price         BIGINT      NOT NULL,
    capacity      BIGINT      NOT NULL,
    available     BIGINT      NOT NULL,
    max_per_order INT         NOT NULL DEFAULT 3,
    sort_order    INT         NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT ticket_categories_price_nonnegative  CHECK (price >= 0),
    CONSTRAINT ticket_categories_capacity_positive  CHECK (capacity > 0),
    CONSTRAINT ticket_categories_available_range    CHECK (available BETWEEN 0 AND capacity),
    CONSTRAINT ticket_categories_max_per_order_range CHECK (max_per_order >= 0)
);

CREATE INDEX IF NOT EXISTS ticket_categories_event_idx
    ON ticket_categories (event_id, sort_order);

COMMIT;
