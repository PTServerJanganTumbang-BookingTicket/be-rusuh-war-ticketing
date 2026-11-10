@step-2 @admission-ctl @integration @postgres
Feature: I get one booking per place, and counting who is inside stays fast
  As a customer, my place in line turns into exactly one booking, however often the controller retries.
  As everyone in the line, I need the "who is inside" count to stay fast with 200k bookings in the table.
  Migration 0006 (built with join) already has UNIQUE (event_id, queue_token) and a unique grant_token.
  Step 2 adds 0007: a partial index on the active statuses for the count, and one on booking_expires_at for GRANTED rows (the LAPSED sweep, ADR-006 §8.12).
  Tests run on a real Postgres (testcontainers) with every migration applied.

  Sources: 05-admission-controller.md §3.1, §3.3 · 04-queue README §3.2, §5 step 2 · migrations/0006

  Background:
    Given a Postgres database with all migrations applied
    And event "E1" exists

  # ------------------------------------------------------------ one booking each

  @rule-4
  Scenario: Saving my booking twice keeps one row and gives back the first
    When the store inserts a GRANTED booking for "E1" with queue_token "q1", booking_id "B1", grant_token "G1"
    And the store inserts a GRANTED booking for "E1" with queue_token "q1", booking_id "B2", grant_token "G2"
    Then bookings has exactly 1 row for "E1" and queue_token "q1"
    And both calls return booking_id "B1" and grant_token "G1"

  Scenario: The same queue_token in two different events gives two bookings
    When the store inserts a GRANTED booking for "E1" with queue_token "q1"
    And the store inserts a GRANTED booking for "E2" with queue_token "q1"
    Then both inserts succeed

  Scenario: Nobody can be handed my grant_token
    Given a booking with grant_token "G1" exists
    When the store inserts a different booking with grant_token "G1"
    Then the insert fails with a unique violation on grant_token

  # ------------------------------------------------------------ who is inside

  @rule-3
  Scenario Outline: Only active bookings take a place, and only the first two steps count as browsing
    Given "E1" has one booking with status "<status>"
    When the store counts admission for "E1"
    Then open is <open> and browsing is <browsing>

    Examples:
      | status               | open | browsing |
      | GRANTED              | 1    | 1        |
      | SELECTING            | 1    | 1        |
      | HELD                 | 1    | 0        |
      | AWAITING_PAYMENT     | 1    | 0        |
      | PENDING_CANCELLATION | 1    | 0        |
      | CONFIRMED            | 0    | 0        |
      | EXPIRED              | 0    | 0        |
      | LAPSED               | 0    | 0        |
      | ABANDONED            | 0    | 0        |

  @rule-3
  Scenario: Another event's crowd does not take my places
    Given "E1" has 3 GRANTED bookings and "E2" has 7 GRANTED bookings
    When the store counts admission for "E1"
    Then open is 3 and browsing is 3

  # ------------------------------------------------------------ fast and current

  @NFR-02
  Scenario: The count stays cheap with a big table
    Given "E1" and 20 other events have 200000 bookings, of which 100 are active for "E1"
    When I EXPLAIN the admission count for "E1"
    Then the plan uses the partial index on (event_id, status)
    And the plan does not contain a sequential scan on bookings

  Scenario: The count reads the writer, so a place freed a moment ago is seen
    When the store counts admission for "E1"
    Then the query is sent through the writer connection pool, not the reader pool

  Scenario: The new migration applies cleanly and rolls back
    Given an empty database
    When all migrations are applied, rolled back and applied again
    Then no step fails
