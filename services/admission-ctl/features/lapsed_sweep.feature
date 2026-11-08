@step-8 @admission-ctl @FR-10
Feature: If I don't use my turn within 5 minutes, I lose it
  As a customer whose turn came, I have 5 minutes to start choosing seats. If I don't, my booking moves
  GRANTED → LAPSED for good (no second chance) and the next tick gives my place to the next person.
  The queue part of SWEEP does this with one compare-and-set UPDATE on Aurora (no Redis deadline list: ADR-006 §8.12);
  SELECTING and later belong to 05-booking.

  Sources: 05-admission-controller.md §4 · README §3.2 · ADR-006 §8.12 · requirements §5 (no second chance)

  Background:
    Given event "E1" is "OPEN"
    And customer "c1" was granted booking "B1" with deadline "2026-11-10T19:05:00Z"

  # ------------------------------------------------------------ I didn't show up

  @integration @postgres @redis-cluster
  Scenario: At the deadline my unused turn lapses
    Given "B1" is still "GRANTED"
    When the clock reaches "2026-11-10T19:05:00Z" and SWEEP runs
    Then "B1" has status "LAPSED"
    And grant:<G1> no longer exists

  @integration @postgres
  Scenario: One second before the deadline my turn is still mine
    Given "B1" is still "GRANTED"
    When the clock reads "2026-11-10T19:04:59Z" and SWEEP runs
    Then "B1" is still "GRANTED"
    And grant:<G1> still exists

  @integration @postgres @redis-cluster @rule-3
  Scenario: My lapsed place goes to the next person on the next tick
    Given MAX_CONCURRENT_SLOTS is 1 and "B1" is the only active booking
    And customer "c2" is waiting
    When "B1" lapses and the next tick runs
    Then "c2" is granted

  @unit
  Scenario: A lapsed turn stays lapsed
    Given "B1" is "LAPSED"
    When any component tries to move "B1" to another status
    Then the update changes 0 rows

  @integration @postgres @redis-cluster @fault
  Scenario: If the controller dies right after my turn lapses, my grant is still dead
    Given the controller will crash right after the UPDATE that lapses "B1", before it deletes any Redis key
    When the clock reaches "2026-11-10T19:05:00Z" and SWEEP runs and the controller dies
    Then "B1" has status "LAPSED"
    And every use of G1 is rejected, because the booking is "LAPSED"
    # No deadline list to repair: the row is the only record. The grant hash may live on until its
    # 1080 s TTL ends (it must outlive payment), so rejection comes from the status check, not from the hash.

  # ------------------------------------------------------------ I did show up

  @integration @postgres
  Scenario: If I started choosing seats in time, the queue leaves me alone
    Given "B1" moved to "SELECTING"
    When the deadline passes and the queue SWEEP runs
    Then "B1" is still "SELECTING"
    # SELECTING → EXPIRED belongs to 05-booking, which uses the same kind of query for its own statuses.

  @integration @postgres
  Scenario: If my deadline moved later, I keep my turn until the new deadline
    Given "B1" is "GRANTED" with booking_expires_at moved to "2026-11-10T19:07:00Z"
    When the clock reaches "2026-11-10T19:05:00Z" and SWEEP runs
    Then "B1" is still "GRANTED"
    When the clock reaches "2026-11-10T19:07:00Z" and SWEEP runs
    Then "B1" has status "LAPSED"

  # ------------------------------------------------------------ two sweepers

  @integration @postgres @fault
  Scenario: Two sweepers on my booking make exactly one change
    Given "B1" is "GRANTED" and past its deadline
    When sweepers A and B run SWEEP at the same time
    Then exactly one of them is given "B1" back by the UPDATE
    And "B1" has status "LAPSED"

  # ------------------------------------------------------------ cost

  @integration @postgres @NFR-02
  Scenario: Finding due turns stays cheap with a big table
    Given 200000 bookings exist, of which 100 are "GRANTED"
    When I EXPLAIN the sweep query
    Then the plan uses the partial index on booking_expires_at for GRANTED rows
    And the plan does not contain a sequential scan on bookings

  # ------------------------------------------------------------ open

  @integration @redis-cluster @open-question
  Scenario: After my turn lapses my place in line is not given away
    # OPEN: 05 §4 deleted acct:{E1}:<account> and token:<uuid> on lapse, and join treats a missing acct as a fresh
    # join, so the customer would get a new ticket at the back. That conflicts with "LAPSED is final".
    # Proposal (built): on lapse, DEL only grant:<token>. Keep acct and token until the sale-window TTL.
    # Join then answers rejoined: true with state LAPSED (that side is covered in services/queue join_rejoin.feature).
    Given "B1" lapsed for account "acc-1"
    Then grant:<G1> is gone
    And acct:{E1}:acc-1 and token:<c1> still exist
    And issued is unchanged
