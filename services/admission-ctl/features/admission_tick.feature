@draft @step-3 @admission-ctl
Feature: One admission tick (RECOVER → SNAPSHOT → COUNT → ADVANCE → FINISH → BROADCAST)
  admission-ctl is the only component that grants. Every 500 ms it counts who is
  inside from Aurora, runs advance_now_serving.lua, writes GRANTED rows and then
  broadcasts. Unit scenarios use fakes with a recorded call log; @integration
  scenarios use a real Redis cluster and a real Postgres with the bookings migration.

  Sources: 05-admission-controller.md §2, §3 · ADR-006 §2–§4 · README §0 rules 1–5

  Background:
    Given event "E1" is "OPEN" with MAX_CONCURRENT_SLOTS 100
    And total stock is 9000
    And the booking TTL is 5 minutes
    And the clock is frozen at "2026-11-10T19:00:00Z"

  @unit @rule-5
  Scenario: Steps run in the fixed order
    Given customers hold ticket numbers 1 to 3 in by_ticket and issued is 3
    When one tick runs
    Then the call log is, in order:
      | step     | call                                         |
      | RECOVER  | ZRANGEBYSCORE by_ticket -inf now_serving      |
      | SNAPSHOT | HGET queue:{E1} now_serving                   |
      | SNAPSHOT | ZCOUNT by_ticket -inf <snapshot now_serving>  |
      | COUNT    | SELECT open, browsing FROM bookings           |
      | ADVANCE  | EVALSHA advance_now_serving                   |
      | FINISH   | INSERT bookings ... ON CONFLICT DO NOTHING    |
      | FINISH   | HSET grant:<token>                            |
      | FINISH   | ZADD expiries:{E1}                            |
      | FINISH   | ZREM by_ticket                                |
      | BROADCAST| PUBLISH sse-broadcast                         |

  @unit @rule-3 @rule-4
  Scenario: Pending grants are added to both open and browsing before ADVANCE
    Given Aurora counts open 60 and browsing 20 for "E1"
    And 5 tokens are pending grants
    When one tick runs
    Then ADVANCE is called with open 65 and browsing 25
    And ADVANCE is called with the now_serving read in SNAPSHOT

  @unit @rule-5
  Scenario: A grant made by someone else after SNAPSHOT is caught by the CAS fence
    Given SNAPSHOT reads now_serving 10
    And another writer raises now_serving to 12 before ADVANCE
    When ADVANCE runs with seen now_serving 10
    Then ADVANCE returns "STALE"
    And no INSERT is attempted this tick

  @integration @postgres @redis-cluster @FR-08
  Scenario: Admitted tokens become GRANTED rows with a 5-minute deadline
    Given customers "c1", "c2", "c3" joined with ticket numbers 1, 2, 3 and issued is 3
    When one tick runs
    Then bookings has exactly 3 rows for "E1" with status "GRANTED"
    And each row has the customer's account_id, queue_token and ticket_no
    And each row has a unique grant_token
    And each row has booking_expires_at "2026-11-10T19:05:00Z"
    And grant:<grant_token> exists for each row with a TTL of 300 seconds
    And expiries:{E1} scores each booking_id at the deadline epoch
    And by_ticket no longer holds "c1", "c2", "c3"
    And now_serving is 3

  @integration @postgres @rule-3 @FR-08
  Scenario: Counts come from Aurora, so a booking that ends frees a place on the next tick
    Given 100 customers are inside with status "GRANTED"
    And 50 more customers are waiting
    When one tick runs
    Then nobody is admitted
    When 1 booking moves to "CONFIRMED"
    And the next tick runs
    Then exactly 1 customer is admitted

  @unit @rule-1
  Scenario: Nothing outside the tick triggers an early ADVANCE
    Given the tick interval is 500 ms
    When a booking is confirmed 100 ms after a tick
    Then ADVANCE runs again only at the next 500 ms tick

  @unit @adr-006-4-3
  Scenario Outline: A dependency outage never pauses admission
    Given breaker "<breaker>" is "OPEN"
    And customers hold ticket numbers 1 to 3 in by_ticket and issued is 3
    When one tick runs
    Then COUNT and ADVANCE both run
    And 3 customers are admitted

    Examples:
      | breaker                      |
      | breaker:dukcapil:state       |
      | breaker:payment-gateway:state|

  @unit @rule-5
  Scenario: Aurora writer unreachable skips ADVANCE
    Given the Aurora COUNT fails with "connection refused"
    And customers hold ticket numbers 1 to 3 in by_ticket and issued is 3
    When one tick runs
    Then ADVANCE is not called
    And now_serving is unchanged
    And the metric "CountFailures" is incremented
    And the heartbeat broadcast still goes out on schedule
    # OPEN: 05 §7 says "no broadcast change". Proposal: keep the 10 s heartbeat so
    # clients don't show the stale indicator just because Aurora blipped.

  @unit
  Scenario: Result STALE ends the tick without FINISH
    Given ADVANCE returns "STALE"
    When one tick runs
    Then no INSERT is attempted
    And the metric "AdvanceStale" is incremented

  @unit
  Scenario Outline: Non-admitting results end ADVANCE quietly
    Given ADVANCE returns "<result>"
    When one tick runs
    Then no INSERT is attempted
    And the tick does not fail

    Examples:
      | result              |
      | NOOP                |
      | EMPTY               |
      | FULL_BOOKED            |
      | SCHEDULED           |
      | CLOSED              |

  @unit
  Scenario Outline: Configuration problems raise an alarm
    Given ADVANCE returns "<result>"
    When one tick runs
    Then no INSERT is attempted
    And the metric "<metric>" is incremented

    Examples:
      | result              | metric             |
      | STOCK_UNINITIALIZED | StockUninitialized |
      | MISSING             | EventStateMissing  |

  @unit
  Scenario: Several events are ticked independently
    Given events "E1" and "E2" are "OPEN"
    And the Aurora COUNT fails only for "E2"
    When one tick runs
    Then "E1" admits as usual
    And "E2" skips ADVANCE

  @unit
  Scenario: A slow tick does not overlap the next one
    Given one tick takes 800 ms
    When the ticker fires at 500 ms
    Then the second tick starts only after the first one finishes
    And the metric "TickOverrun" is incremented
