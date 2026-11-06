@draft @step-3 @admission-ctl @rule-4 @adr-006-2
Feature: Nobody is lost: pending grants survive crashes and failures
  A token leaves by_ticket only after its GRANTED row and grant hash exist.
  A crash at ANY point between ADVANCE and ZREM is finished by the next tick's
  RECOVER. Fault scenarios stop the controller at an injected point and start
  a fresh controller (same as an ECS task restart) on the same Redis and Postgres.

  Sources: 05 §3.1, §6 · ADR-006 §2

  Background:
    Given event "E1" is "OPEN" with MAX_CONCURRENT_SLOTS 100 and stock 9000
    And customers "c1" to "c5" joined with ticket numbers 1 to 5 and issued is 5

  @fault @integration @postgres @redis-cluster
  Scenario Outline: A crash at any finish step is completed by the next controller
    Given the controller will crash <crash point>
    When one tick runs and the controller dies
    And a new controller starts and runs one tick
    Then bookings has exactly 1 row per customer "c1" to "c5"
    And each customer has exactly one grant:<grant_token> hash
    And by_ticket holds no member with score <= now_serving
    And now_serving is 5
    And the metric "PendingGrantsRecovered" equals the number of tokens still in by_ticket when it crashed

    Examples:
      | crash point                                   |
      | right after EVALSHA, before any INSERT        |
      | after the INSERT of "c3", before its HSET     |
      | after HSET grant for "c3", before ZADD        |
      | after ZADD expiries for "c3", before its ZREM |
      | after the last ZREM, before BROADCAST         |
    # FINISH may be batched (05 §3.4); the outcome must not depend on batching.

  @fault @integration @postgres
  Scenario: A retry reuses the first row's ids and deadlines
    Given the controller crashed after the INSERT of "c1", before its HSET
    And the row for "c1" has booking_id "B1", grant_token "G1" and deadline "2026-11-10T19:05:00Z"
    When the clock moves 30 seconds and a new controller runs one tick
    Then the row for "c1" still has booking_id "B1", grant_token "G1" and deadline "2026-11-10T19:05:00Z"
    And grant:G1 has a TTL of 270 seconds
    # The customer does not get extra time because the controller crashed.

  @unit
  Scenario: An INSERT that keeps failing leaves the token pending
    Given the INSERT for "c2" fails 3 times in a row
    When one tick runs
    Then "c1", "c3", "c4", "c5" are granted
    And "c2" is still in by_ticket with score <= now_serving
    And no grant hash exists for "c2"
    And the metric "GrantInsertFailures" is incremented once
    And the broadcast is still published

  @unit
  Scenario: A pending token is granted once the database recovers
    Given "c2" is pending after failed INSERTs
    When the database recovers and the next tick runs
    Then "c2" is granted by RECOVER before ADVANCE runs

  @unit
  Scenario: RECOVER with nothing pending costs one Redis call
    Given no token is pending
    When RECOVER runs
    Then exactly 1 Redis call is made: ZRANGEBYSCORE by_ticket -inf now_serving
    And no database call is made

  @unit
  Scenario: Pending for too long raises an alert
    Given "c2" stays pending
    When 10 ticks run
    Then the alert "PendingGrantsStuck" is not raised
    When the 11th tick runs
    Then the alert "PendingGrantsStuck" is raised

  @integration @postgres @redis-cluster
  Scenario: Two controllers running RECOVER at once still make one row each
    Given tickets 1 to 5 are pending grants
    When controllers A and B run RECOVER at the same time
    Then bookings has exactly 1 row per customer "c1" to "c5"
    And a double ZREM causes no error

  @integration @postgres @redis-cluster @rule-5
  Scenario: A second controller cannot advance past the first (CAS fence)
    Given controllers A and B read the same snapshot
    When both run ADVANCE
    Then only one of them gets "ADMITTED"
    And the total admitted never exceeds MAX_CONCURRENT_SLOTS

  @unit
  Scenario: by_ticket size is not the queue depth
    Given tickets 1 to 5 are pending and 20 customers wait behind them
    Then the reported queue depth is issued − now_serving
    And it does not use ZCARD by_ticket

  @unit @open-question
  Scenario: The token hash expired before the grant was finished
    # OPEN: FINISH reads account_id from token:<uuid>. If that hash is gone (sale window
    # passed, or deleted), the INSERT can't be written. Proposal: ZREM the token,
    # count metric "GrantTokenMissing", and log it. The customer has already left.
    Given "c4" is pending and token:<c4> no longer exists
    When one tick runs
    Then no row is written for "c4"
    And "c4" is removed from by_ticket
    And the metric "GrantTokenMissing" is incremented
