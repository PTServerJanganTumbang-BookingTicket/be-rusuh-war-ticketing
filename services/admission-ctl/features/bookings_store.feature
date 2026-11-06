@draft @step-2 @admission-ctl @integration @postgres
Feature: bookings table supports idempotent grants and a cheap admission count
  The migration adds UNIQUE (event_id, queue_token) and a partial index on the
  active statuses. Tests run on a real Postgres (testcontainers) with the real migrations.

  Sources: 05 §3.1, §3.3 · queue README §3.2, §5 step 2

  Background:
    Given a Postgres database with all migrations applied
    And event "E1" exists

  @rule-4
  Scenario: Inserting the same grant twice keeps one row and returns the first
    When the store inserts a GRANTED booking for "E1" with queue_token "q1", booking_id "B1", grant_token "G1"
    And the store inserts a GRANTED booking for "E1" with queue_token "q1", booking_id "B2", grant_token "G2"
    Then bookings has exactly 1 row for "E1" and queue_token "q1"
    And both calls return booking_id "B1" and grant_token "G1"

  Scenario: The same queue_token may exist in two different events
    When the store inserts a GRANTED booking for "E1" with queue_token "q1"
    And the store inserts a GRANTED booking for "E2" with queue_token "q1"
    Then both inserts succeed

  Scenario: A grant_token is unique across all bookings
    Given a booking with grant_token "G1" exists
    When the store inserts a different booking with grant_token "G1"
    Then the insert fails with a unique violation on grant_token

  @rule-3
  Scenario Outline: Only active statuses count, and only two count as browsing
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
  Scenario: The count is per event
    Given "E1" has 3 GRANTED bookings and "E2" has 7 GRANTED bookings
    When the store counts admission for "E1"
    Then open is 3 and browsing is 3

  @NFR-02
  Scenario: The count uses the partial index
    Given "E1" and 20 other events have 200000 bookings, of which 100 are active for "E1"
    When I EXPLAIN the admission count for "E1"
    Then the plan uses the partial index on (event_id, status)
    And the plan does not contain a sequential scan on bookings

  Scenario: The count runs on the writer pool
    When the store counts admission for "E1"
    Then the query is sent through the writer connection pool, not the reader pool

  Scenario: Migrations apply cleanly and roll back
    Given an empty database
    When all migrations are applied, rolled back and applied again
    Then no step fails
    # OPEN: the existing migrations 0001–0004 are on branch
    # feature/user-notif-nikverify-outboxpublisher. The new one must number after them.
