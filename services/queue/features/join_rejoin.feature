@step-4 @queue-service @join @BR-07 @BR-09
Feature: Rejoining returns the existing place instead of a second one
  Two paths lead to "rejoined" (contract §3.1):
    BR-09: the caller brings back a queue_token (cookie on web, X-Queue-Token on mobile)
    BR-07: the caller is logged in and the account already has a place for this event
  Neither creates a second place or a new ticket number. Guests are not covered by BR-07.

  Sources: api-contracts §3.1, §3.3 · impl-plan §7.2 step 6 · 01-join-queue.md §6

  Background:
    Given event "E1" is "OPEN" with now_serving 31520 and issued 40000
    And the CAPTCHA provider accepts token "cap-ok"

  @integration @redis-cluster @BR-07
  Scenario: The same account joining from another device gets its place back
    Given account "acc-1" joined "E1" earlier with ticket number 32748 and queue_token "qt_A"
    When account "acc-1" joins "E1" again from another device with captcha "cap-ok"
    Then the response status is 200 with message "Rejoined existing queue position"
    And data has ticket_no 32748, now_serving 31520, position 1228, queue_token "qt_A", rejoined true, state "QUEUED"
    And the response sets cookie "queue_token" to "qt_A"
    And queue:{E1}:issued is still 40000
    And the metric "Br07JoinRecovered" is incremented

  @integration @redis-cluster @BR-09
  Scenario Outline: Bringing the queue_token back returns the same place
    Given a guest joined "E1" earlier with ticket number 32748 and queue_token "qt_G"
    When the guest joins "E1" again sending queue_token "qt_G" as <carrier> with captcha "cap-ok"
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
  Scenario Outline: Rejoining after the turn came reports the booking state
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
  Scenario: The account key outlived its token, so this is a fresh join
    Given acct:{E1}:acc-1 points to "qt_GONE" but token:qt_GONE does not exist
    When account "acc-1" joins "E1" with captcha "cap-ok"
    Then acct:{E1}:acc-1 now points to the new queue_token
    And data.ticket_no is 40001
    And data has no rejoined field

  @integration @redis-cluster @open-question
  Scenario: A customer already in the queue is not refused when the queue fills up
    # OPEN: impl-plan §7.2 checks capacity (step 5) BEFORE BR-07 (step 6), so a logged-in
    # customer switching devices would get QUEUE_FULL although they already have a place.
    # Proposal: run both rejoin checks before the capacity gate (order in join_gates.feature).
    Given account "acc-1" joined "E1" earlier with ticket number 32748 and queue_token "qt_A"
    And the queue is now full
    When account "acc-1" joins "E1" again with captcha "cap-ok"
    Then the response status is 200 with rejoined true

  @integration @redis-cluster @fault @open-question
  Scenario: A join that crashed between HSET token and ZADD is repaired on rejoin
    # OPEN: join writes INCR → HSET token → ZADD by_ticket, not atomically (token:<qt> has no
    # {ev} tag, so MULTI across them is not allowed in cluster mode). A crash after HSET leaves
    # a token admission-ctl can never see; BR-07 would keep returning it as QUEUED forever.
    # Proposal: on rejoin, if state is QUEUED and the token is missing from by_ticket,
    # ZADD NX it back with its own ticket_no and count metric "JoinRepaired".
    Given account "acc-1" has token "qt_A" with ticket number 32748 but it is not in by_ticket
    When account "acc-1" joins "E1" again with captcha "cap-ok"
    Then queue:{E1}:by_ticket scores "qt_A" at 32748
    And the metric "JoinRepaired" is incremented

  @integration @redis-cluster
  Scenario: Concurrent joins of one account create one place
    Given account "acc-1" has a valid access token
    When account "acc-1" sends 10 joins for "E1" at the same time
    Then exactly one new ticket number is issued
    And all successful responses carry the same queue_token
    And acct:{E1}:acc-1 points to that queue_token

  @integration @redis-cluster
  Scenario: The CLOSED gate still applies to rejoiners
    Given account "acc-1" joined "E1" earlier
    And event "E1" is now "CLOSED"
    When account "acc-1" joins "E1" again with captcha "cap-ok"
    Then the response status is 410 with error_code "SALE_CLOSED"

  @integration @redis-cluster @FR-18
  Scenario: A rejoiner keeps their place while SOLD_OUT
    # Sold out refuses only NEW places; seats may come back to those already in line.
    Given account "acc-1" joined "E1" earlier and got ticket 40000
    And event "E1" is now "SOLD_OUT"
    When account "acc-1" joins "E1" again with captcha "cap-ok"
    Then the response status is 200 with message "Rejoined existing queue position"
    And data has rejoined true, state "QUEUED" and ticket_no 40000

  @integration @redis-cluster @open-question
  Scenario: A lapsed customer gets the LAPSED state, not a new place
    # Same OPEN as lapsed_sweep.feature: LAPSED is final (no second chance).
    Given account "acc-1" had booking status "LAPSED" for "E1"
    When account "acc-1" joins "E1" again with captcha "cap-ok"
    Then data has rejoined true and state "LAPSED"
    And queue:{E1}:issued is still 40000
