@step-4 @queue-service @join @FR-05 @rule-1
Feature: If there is room inside, I go straight to the ticket page
  As a customer who joins while places are free (the first batch at sale start, or a quiet moment later),
  I get "granted" straight from join and never see the waiting room.
  Join never grants. Each tick admission-ctl writes free_slots (places still free after that tick) and
  free_slots_at into queue:{ev}. If my ticket_no fits, join waits up to JOIN_GRANT_WAIT_MS (1 s), polling
  token:<my queue_token> in Redis every 100 ms for the grant_token admission-ctl writes when it grants me.
  It fits when free_slots_at is at most JOIN_FREE_SLOTS_MAX_AGE_SECONDS (2) old and
  ticket_no <= now_serving + min(free_slots, JOIN_DIRECT_MAX). Otherwise I get the queued answer at once.
  Until step 3, integration scenarios use a stand-in controller; e2e scenarios use the real admission-ctl.

  Sources: api-contracts §3.1 (FR-05) · 01-join-queue.md §2, §6 · ADR-006 §4.1, §8.11

  Background:
    Given event "E1" is "OPEN" with now_serving 500 and issued 500
    And admission-ctl wrote free_slots 10 for "E1" 0 seconds ago
    And JOIN_GRANT_WAIT_MS is 1000
    And JOIN_DIRECT_MAX is 500
    And JOIN_FREE_SLOTS_MAX_AGE_SECONDS is 2
    And the CAPTCHA provider accepts token "cap-ok"

  # ------------------------------------------------------------ I fit

  @integration @redis-cluster @postgres
  Scenario: If my turn is granted within the wait, I go straight in
    Given the stand-in controller grants every joiner that fits after 300 ms
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200 with message "No queue — access granted"
    And data is exactly granted true, grant_token, queue_token and booking_expires_at from token:<queue_token>
    And the response sets cookie "queue_token" to data.queue_token
    And the response took between 300 ms and 500 ms

  @integration @redis-cluster @postgres @adr-006-8-11
  Scenario: At sale start the whole first batch goes straight in
    Given event "E1" is "OPEN" with now_serving 0 and issued 0
    And admission-ctl wrote free_slots 5 for "E1" 0 seconds ago
    And the stand-in controller grants every joiner that fits after 300 ms
    When 6 guests join "E1" one after another
    Then the guests with ticket_no 1 to 5 got "No queue — access granted"
    And the guest with ticket_no 6 got "Successfully joined queue" in less than 100 ms

  @unit @adr-006-8-11
  Scenario Outline: Who waits for the grant
    Given event "E1" is "OPEN" with now_serving 500 and issued <issued>
    And admission-ctl wrote free_slots <free> for "E1" <age> seconds ago
    And JOIN_DIRECT_MAX is <cap>
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then data.ticket_no is <ticket>
    And join <waits> for the grant

    Examples: fits
      | issued | free | age | cap | ticket | waits        |
      | 500    | 10   | 0   | 500 | 501    | waits        |
      | 509    | 10   | 0   | 500 | 510    | waits        |
      | 500    | 10   | 2   | 500 | 501    | waits        |

    Examples: does not fit
      | issued | free | age | cap | ticket | waits        |
      | 510    | 10   | 0   | 500 | 511    | does not wait |
      | 500    | 0    | 0   | 500 | 501    | does not wait |
      | 501    | 10   | 0   | 1   | 502    | does not wait |
      | 500    | 10   | 3   | 500 | 501    | does not wait |

  @unit
  Scenario: Without free_slots from admission-ctl, I do not wait
    Given queue:{E1} has no free_slots field
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200 with message "Successfully joined queue"
    And join does not wait for the grant

  # ------------------------------------------------------------ the wait

  @integration @redis-cluster @postgres
  Scenario: If it is not granted in time, I get the normal queued answer
    Given the stand-in controller does not grant
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200 with message "Successfully joined queue"
    And data.ticket_no is 501 and data.position is 1
    And the response took between 1000 ms and 1150 ms
    And the join read token:<queue_token> at most 11 times while waiting

  @integration @redis-cluster @postgres
  Scenario: While waiting, join only reads Redis and sends no SQL
    Given the stand-in controller grants every joiner that fits after 300 ms
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then no SQL statement was sent
    And join wrote nothing to Redis after ZADD by_ticket

  @integration @redis-cluster @postgres
  Scenario: If people are already waiting, I do not wait
    Given now_serving is 500 and issued is 520
    And admission-ctl wrote free_slots 0 for "E1" 0 seconds ago
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then data.ticket_no is 521
    And no SQL statement was sent
    And the response took less than 100 ms

  @integration @redis-cluster @postgres
  Scenario: Two joiners who both fit both go straight in
    Given the stand-in controller grants every joiner that fits after 300 ms
    When two guests join "E1" at the same time
    Then both responses say "No queue — access granted"
    And they hold ticket_no 501 and 502

  @integration @redis-cluster
  Scenario: If Redis fails during the wait, I get the queued answer
    Given the stand-in controller does not grant
    And reads of token hashes fail after the join wrote my place
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200 with message "Successfully joined queue"
    And the place is kept in by_ticket

  @unit
  Scenario: If I hang up, the wait stops
    Given the stand-in controller does not grant
    When the client cancels the request after 200 ms
    Then the join stops polling within 100 ms
    And the place is kept in by_ticket

  @unit
  Scenario: The Lambda timeout leaves room for the wait
    Then the configured function timeout is at least 3 times JOIN_GRANT_WAIT_MS

  # ------------------------------------------------------------ with the real controller

  @e2e @integration @redis-cluster @postgres @after-step-3
  Scenario: With the real admission-ctl, a joiner who fits goes straight in
    Given admission-ctl is running for "E1" with free capacity and stock
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then within 1 s the response says granted true
    And the GRANTED row was written by admission-ctl, not by join

  @e2e @integration @redis-cluster @postgres @after-step-3
  Scenario: With admission-ctl stopped, I get the queued answer at once
    Given admission-ctl is stopped and its last free_slots is 3 seconds old
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response says "Successfully joined queue" in less than 100 ms
    And no bookings row exists for the caller
