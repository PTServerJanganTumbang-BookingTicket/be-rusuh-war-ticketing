@step-3 @admission-ctl
Feature: Being let in from the queue
  As a customer in the queue, I am let in when there is room inside and enough tickets left, in ticket order,
  within half a second of a place freeing up.
  admission-ctl is the only thing that lets people in. Every 500 ms it counts who is inside (Aurora), moves
  now_serving forward (advance_now_serving.lua), writes my GRANTED booking, and only then tells everyone the new number.
  The Lua also records free_slots, and the grant is copied into my token hash, so join can send a customer who fits
  straight to the ticket page without the waiting room (ADR-006 §8.11).

  Sources: 05-admission-controller.md §2, §3 · ADR-006 §2–§4 · README §0 rules 1–5

  Background:
    Given event "E1" is "OPEN" with MAX_CONCURRENT_SLOTS 100
    And total stock is 9000
    And the booking TTL is 5 minutes
    And GRANT_TOKEN_TTL_SECONDS is 1080
    And the clock is frozen at "2026-11-10T19:00:00Z"

  # ------------------------------------------------------------ I get in

  @integration @postgres @redis-cluster @FR-08
  Scenario: When my turn comes I get a booking with 5 minutes to use it
    Given customers "c1", "c2", "c3" joined with ticket numbers 1, 2, 3 and issued is 3
    When one tick runs
    Then bookings has exactly 3 rows for "E1" with status "GRANTED"
    And each row has the customer's account_id and queue_token
    And each row has a unique grant_token
    And each row has booking_expires_at "2026-11-10T19:05:00Z"
    And grant:<grant_token> exists for each row with a TTL of 1080 seconds
    And token:<queue_token> of each customer holds that row's grant_token, booking_id and booking_expires_at
    And by_ticket no longer holds "c1", "c2", "c3"
    And now_serving is 3

  @integration @redis-cluster @adr-006-8-9
  Scenario Outline: My grant carries what booking needs to check that it is me
    # Booking and payment read these fields through pkg/grantauth. A guest has device_hash and no account_id,
    # a logged-in customer the other way round (ADR-006 §8.9: same device, no rebind). Absent fields are not written.
    Given customer "c1" is a <kind> with ticket number 1 and issued is 1
    When one tick runs
    Then grant:<grant_token> holds exactly the fields <fields>
    And event_id is "E1", queue_token is c1's queue_token and booking_id is the GRANTED row's id
    And account_id and device_hash are copied from token:<queue_token>, never supplied by the client

    Examples:
      | kind               | fields                                                 |
      | guest              | event_id, booking_id, queue_token, device_hash         |
      | logged-in customer | event_id, booking_id, queue_token, account_id          |

  @integration @redis-cluster @adr-005-6
  Scenario: My grant lasts long enough to finish paying
    # booking 300 s + payment 600 s + grace 30 s = 930 s is the longest trip; the grant hash must outlive it (ADR-005 §6).
    Given customer "c1" was granted at "2026-11-10T19:00:00Z"
    When the clock reads "2026-11-10T19:15:00Z"
    Then grant:<grant_token> still exists
    And the TTL of grant:<grant_token> was set from GRANT_TOKEN_TTL_SECONDS, not from the booking deadline

  @unit @rule-5
  Scenario: My booking exists before anyone is told it is my turn
    Given customers hold ticket numbers 1 to 3 in by_ticket and issued is 3
    When one tick runs
    Then the call log is, in order:
      | step      | call                                         |
      | RECOVER   | ZRANGEBYSCORE by_ticket -inf now_serving     |
      | SNAPSHOT  | HGET queue:{E1} now_serving                  |
      | SNAPSHOT  | ZCOUNT by_ticket -inf <snapshot now_serving> |
      | COUNT     | SELECT open, browsing FROM bookings          |
      | ADVANCE   | EVALSHA advance_now_serving                  |
      | FINISH    | HMGET token:<queue_token> account_id device_hash |
      | FINISH    | INSERT bookings ... ON CONFLICT DO NOTHING   |
      | FINISH    | HSET grant:<token>                           |
      | FINISH    | HSET token:<queue_token> (if it exists)      |
      | FINISH    | ZREM by_ticket                               |
      | BROADCAST | PUBLISH sse-broadcast                        |
    # The app calls GET /queue/me when the broadcast shows nowServing >= its ticket_no, so the row must already be there.
    # Join polls token:<queue_token> for grant_token, so the grant hash must exist before that field appears.

  # ------------------------------------------------------------ straight in, without the waiting room

  @unit @adr-006-8-11 @FR-05
  Scenario: An event that has not opened yet is ticked too, so the first batch can go straight in at sale start
    Given event "E2" is "SCHEDULED" and opens at "2026-11-10T19:00:01Z"
    When one tick runs
    Then COUNT and ADVANCE run for "E2" with open 0 and browsing 0
    And no INSERT is attempted for "E2"
    # The Lua answers SCHEDULED and writes only free_slots (advance_now_serving.feature).

  @integration @postgres @redis-cluster @adr-006-8-11
  Scenario: My grant never creates a token hash that would live forever
    Given customer "c1" is waiting with ticket number 1 and issued is 1
    And token:<c1> expires right after the INSERT of "c1"
    When one tick runs
    Then "c1" has a "GRANTED" row
    And no token:<c1> hash exists
    # HSET only if the hash exists (a plain HSET would recreate it without a TTL).

  # ------------------------------------------------------------ a place frees up

  @integration @postgres @rule-3 @FR-08
  Scenario: When someone inside finishes, the next person gets in on the next tick
    Given 100 customers are inside with status "GRANTED"
    And 50 more customers are waiting
    When one tick runs
    Then nobody is admitted
    When 1 booking moves to "CONFIRMED"
    And the next tick runs
    Then exactly 1 customer is admitted

  @unit @rule-1
  Scenario: Nobody is let in early; the next regular tick does it
    Given the tick interval is 500 ms
    When a booking is confirmed 100 ms after a tick
    Then ADVANCE runs again only at the next 500 ms tick
    # Join, payment webhook, outbox and cancel never grant and never wake the controller.

  @unit @rule-3 @rule-4
  Scenario: People let in but not yet written down still count as inside
    Given Aurora counts open 60 and browsing 20 for "E1"
    And 5 tokens are pending grants
    When one tick runs
    Then ADVANCE is called with open 65 and browsing 25
    And ADVANCE is called with the now_serving read in SNAPSHOT

  # ------------------------------------------------------------ outages

  @unit @adr-006-4-3
  Scenario Outline: A Dukcapil or payment outage does not stop the queue
    Given breaker "<breaker>" is "OPEN"
    And customers hold ticket numbers 1 to 3 in by_ticket and issued is 3
    When one tick runs
    Then COUNT and ADVANCE both run
    And 3 customers are admitted

    Examples:
      | breaker                       |
      | breaker:dukcapil:state        |
      | breaker:payment-gateway:state |

  @unit @rule-5
  Scenario: If the database is unreachable, nobody is let in and nobody is lost
    Given the Aurora COUNT fails with "connection refused"
    And customers hold ticket numbers 1 to 3 in by_ticket and issued is 3
    When one tick runs
    Then ADVANCE is not called
    And now_serving is unchanged
    And the metric "CountFailures" is incremented
    And free_slots_at is not refreshed, so joiners stop waiting after 2 s
    And the heartbeat broadcast still goes out on schedule
    # OPEN: 05 §7 says "no broadcast change". Proposal: keep the 10 s heartbeat so
    # waiting customers don't see the stale indicator just because Aurora blipped.

  @unit
  Scenario: One event's trouble never holds up another event
    Given events "E1" and "E2" are "OPEN"
    And the Aurora COUNT fails only for "E2"
    When one tick runs
    Then "E1" admits as usual
    And "E2" skips ADVANCE

  @unit
  Scenario: A slow tick never overlaps the next one
    Given one tick takes 800 ms
    When the ticker fires at 500 ms
    Then the second tick starts only after the first one finishes
    And the metric "TickOverrun" is incremented

  # ------------------------------------------------------------ when the queue cannot move

  @unit @rule-5
  Scenario: If another controller moved the queue first, this one backs off
    Given SNAPSHOT reads now_serving 10
    And another writer raises now_serving to 12 before ADVANCE
    When one tick runs
    Then ADVANCE returns "STALE"
    And no INSERT is attempted this tick
    And the metric "AdvanceStale" is incremented

  @unit
  Scenario Outline: Nothing to do is not an error
    Given ADVANCE returns "<result>"
    When one tick runs
    Then no INSERT is attempted
    And the tick does not fail

    Examples:
      | result      |
      | NOOP        |
      | EMPTY       |
      | FULL_BOOKED |
      | SCHEDULED   |
      | CLOSED      |

  @unit
  Scenario Outline: A badly set-up event lets nobody in and alerts the team
    Given ADVANCE returns "<result>"
    When one tick runs
    Then no INSERT is attempted
    And the metric "<metric>" is incremented

    Examples:
      | result              | metric             |
      | STOCK_UNINITIALIZED | StockUninitialized |
      | MISSING             | EventStateMissing  |
