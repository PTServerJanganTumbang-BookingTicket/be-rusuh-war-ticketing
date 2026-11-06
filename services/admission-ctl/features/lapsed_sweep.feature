@draft @step-8 @admission-ctl @FR-10
Feature: A granted customer who never shows up lapses after the booking TTL
  The queue-owned part of SWEEP moves GRANTED → LAPSED with a compare-and-set
  update. LAPSED is final. The freed place is found by the next tick's COUNT.

  Sources: 05 §4 · README §3.2 · requirements §5 (no second chance)

  Background:
    Given event "E1" is "OPEN"
    And customer "c1" was granted booking "B1" with deadline "2026-11-10T19:05:00Z"
    And expiries:{E1} scores "B1" at that deadline

  @integration @postgres @redis-cluster
  Scenario: A no-show lapses at the deadline
    Given "B1" is still "GRANTED"
    When the clock reaches "2026-11-10T19:05:00Z" and SWEEP runs
    Then "B1" has status "LAPSED"
    And grant:<G1> no longer exists
    And expiries:{E1} no longer holds "B1"

  @integration @postgres
  Scenario: Not yet due
    Given "B1" is still "GRANTED"
    When the clock reads "2026-11-10T19:04:59Z" and SWEEP runs
    Then "B1" is still "GRANTED"
    And expiries:{E1} still holds "B1"

  @integration @postgres
  Scenario: The customer claimed the booking in time
    Given "B1" moved to "SELECTING"
    When the deadline passes and the queue SWEEP runs
    Then "B1" is still "SELECTING"
    And the queue SWEEP does not remove "B1" from expiries:{E1}
    # SELECTING → EXPIRED belongs to 05-booking, which handles the same expiries entry.

  @integration @postgres
  Scenario: The deadline moved later, so the entry is rescored
    Given "B1" is "GRANTED" with booking_expires_at moved to "2026-11-10T19:07:00Z"
    And expiries:{E1} still scores "B1" at "2026-11-10T19:05:00Z"
    When the clock reaches "2026-11-10T19:05:00Z" and SWEEP runs
    Then "B1" is still "GRANTED"
    And expiries:{E1} scores "B1" at "2026-11-10T19:07:00Z"

  @integration @postgres @fault
  Scenario: Two sweepers on the same row make exactly one transition
    Given "B1" is "GRANTED" and past its deadline
    When sweepers A and B update "B1" at the same time
    Then exactly one of them reports 1 row changed
    And "B1" has status "LAPSED"

  @integration @postgres @redis-cluster @rule-3
  Scenario: A lapse frees a place on the next tick without any counter
    Given MAX_CONCURRENT_SLOTS is 1 and "B1" is the only active booking
    And customer "c2" is waiting
    When "B1" lapses and the next tick runs
    Then "c2" is granted

  @unit
  Scenario: LAPSED is final
    Given "B1" is "LAPSED"
    When any component tries to move "B1" to another status
    Then the update changes 0 rows

  @integration @redis-cluster @open-question
  Scenario: A lapsed customer cannot join the same sale again
    # OPEN: 05 §4 deletes acct:{E1}:<account> and token:<uuid> on lapse, and join treats a
    # missing acct as a fresh join, so the customer would get a new ticket at the back.
    # That conflicts with "LAPSED is final, no second chance".
    # Proposal: on lapse, DEL only grant:<token>. Keep acct and token until the sale-window
    # TTL, and let join answer rejoined: true with state LAPSED.
    Given "B1" lapsed for account "acc-1"
    When account "acc-1" joins the queue for "E1" again
    Then join responds rejoined true with state "LAPSED"
    And issued is unchanged
