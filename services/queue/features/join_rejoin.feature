@step-4 @queue-service @join @BR-07 @BR-09
Feature: If I come back, I get my place back, not a second one
  Two ways back (contract §3.1): BR-09 I bring my queue_token (cookie on web, X-Queue-Token on mobile);
  BR-07 I am logged in and my account already has a place. Neither creates a second place or number.
  Guests are not covered by BR-07. The token must be mine: join_token_binding.feature.

  Sources: api-contracts §3.1, §3.3 · impl-plan §7.2 step 6 · 01-join-queue.md §6

  Background:
    Given event "E1" is "OPEN" with now_serving 31520 and issued 40000
    And the CAPTCHA provider accepts token "cap-ok"

  @integration @redis-cluster @BR-07
  Scenario: I log in on another device and get my place back
    Given account "acc-1" joined "E1" earlier with ticket number 32748 and queue_token "qt_A"
    When account "acc-1" joins "E1" again from another device with captcha "cap-ok"
    Then the response status is 200 with message "Rejoined existing queue position"
    And data has ticket_no 32748, now_serving 31520, position 1228, queue_token "qt_A", rejoined true, state "QUEUED"
    And the response sets cookie "queue_token" to "qt_A"
    And queue:{E1}:issued is still 40000
    And the metric "Br07JoinRecovered" is incremented

  @integration @redis-cluster @BR-09
  Scenario Outline: Outline: I bring my queue_token back from the same device and get the same place
    Given a guest with device id "dev-gggggggggggggggg" joined "E1" earlier with ticket number 32748 and queue_token "qt_G"
    When the guest joins "E1" again from the same device sending queue_token "qt_G" as <carrier> with captcha "cap-ok"
    Then data has ticket_no 32748, queue_token "qt_G", rejoined true, state "QUEUED"
    And queue:{E1}:issued is still 40000

    Examples:
      | carrier                 |
      | the queue_token cookie  |
      | the X-Queue-Token header |

  @integration @redis-cluster @BR-09
  Scenario: A queue_token from another event is ignored
    Given a guest holds queue_token "qt_X" for event "E2"
    When the guest joins "E1" sending queue_token "qt_X" with captcha "cap-ok"
    Then the response is a normal new join for "E1"

  @integration @redis-cluster @BR-09
  Scenario: An unknown or expired queue_token is ignored
    When a guest joins "E1" sending queue_token "qt_UNKNOWN" with captcha "cap-ok"
    Then the response is a normal new join for "E1"

  @integration @redis-cluster @postgres
  Scenario Outline: Outline: If I return after my turn came, I am told my booking state
    Given account "acc-1" holds ticket number 31000 with booking status "<status>"
    When account "acc-1" joins "E1" again with captcha "cap-ok"
    Then data has rejoined true and state "<status>"
    And the frontend routes by state (position fields are ignored)

    Examples:
      | status           |
      | GRANTED          |
      | SELECTING        |
      | AWAITING_PAYMENT |

  @integration @redis-cluster
  Scenario: If my account points to a token that is gone, I join fresh
    Given acct:{E1}:acc-1 points to "qt_GONE" but token:qt_GONE does not exist
    When account "acc-1" joins "E1" with captcha "cap-ok"
    Then acct:{E1}:acc-1 now points to the new queue_token
    And data.ticket_no is 40001
    And data has no rejoined field

  @integration @redis-cluster
  Scenario: If the queue fills up, I am not refused when I come back
    Given account "acc-1" joined "E1" earlier with ticket number 32748 and queue_token "qt_A"
    And the queue is now full
    When account "acc-1" joins "E1" again with captcha "cap-ok"
    Then the response status is 200 with rejoined true

  @integration @redis-cluster @fault
  Scenario: A place left half-written by a crashed join is repaired when I return
    Given account "acc-1" has token "qt_A" with ticket number 32748 but it is not in by_ticket
    When account "acc-1" joins "E1" again with captcha "cap-ok"
    Then queue:{E1}:by_ticket scores "qt_A" at 32748
    And the metric "JoinRepaired" is incremented

  @integration @redis-cluster
  Scenario: Ten joins at once from one account create one place
    Given account "acc-1" has a valid access token
    When account "acc-1" sends 10 joins for "E1" at the same time
    Then exactly one new ticket number is issued
    And all successful responses carry the same queue_token
    And acct:{E1}:acc-1 points to that queue_token

  @integration @redis-cluster
  Scenario: If the sale closed, coming back does not reopen it
    Given account "acc-1" joined "E1" earlier
    And event "E1" is now "CLOSED"
    When account "acc-1" joins "E1" again with captcha "cap-ok"
    Then the response status is 410 with error_code "SALE_CLOSED"

  @integration @redis-cluster @FR-18
  Scenario: While fully booked, I keep my place if I come back
    # Fully booked refuses only NEW places; seats may come back to those already in line.
    Given account "acc-1" joined "E1" earlier and got ticket 40000
    And event "E1" is now "FULL_BOOKED"
    When account "acc-1" joins "E1" again with captcha "cap-ok"
    Then the response status is 200 with message "Rejoined existing queue position"
    And data has rejoined true, state "QUEUED" and ticket_no 40000

  @integration @redis-cluster
  Scenario: If my booking lapsed, I am told so, not given a new place
    # LAPSED is final (no second chance).
    Given account "acc-1" had booking status "LAPSED" for "E1"
    When account "acc-1" joins "E1" again with captcha "cap-ok"
    Then data has rejoined true and state "LAPSED"
    And queue:{E1}:issued is still 40000
