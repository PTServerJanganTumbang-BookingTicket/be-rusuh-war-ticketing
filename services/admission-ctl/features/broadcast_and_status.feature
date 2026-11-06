@draft @step-3 @admission-ctl @rule-2
Feature: One broadcast for everyone, and one status event per sale-state change
  After FINISH, the controller publishes the same payload to every waiting client
  on sse-broadcast. Clients compute their own position. There is no grant event.
  Sale-state changes go once to sse-status. SOLD_OUT turns into CLOSED after 930 s.

  Sources: 05 §2, §3.4 · 02-queue-status-stream.md · ADR-006 §4.2

  Background:
    Given event "E1" is "OPEN"
    And the clock is frozen at epoch 1800000000

  @unit
  Scenario: The broadcast payload has exactly the documented fields
    Given now_serving is 120 and rate_per_min is 900
    When a tick publishes the broadcast
    Then the sse-broadcast message for "E1" is:
      """json
      {"eventId":"E1","nowServing":120,"state":"OPEN","ratePerMin":900,"serverTime":1800000000}
      """
    And the message contains no ticket_no, queue_token or grant_token

  @unit
  Scenario: Broadcast only after every INSERT of the tick
    Given 3 tokens are admitted this tick
    When the tick runs
    Then PUBLISH sse-broadcast comes after the 3rd ZREM in the call log

  @unit
  Scenario: Unchanged ticks publish nothing until the 10 s heartbeat
    Given the last broadcast was at epoch 1800000000 with now_serving 120
    When ticks run for 9.5 s with now_serving still 120
    Then no broadcast is published
    When the tick at 10 s runs
    Then one heartbeat broadcast is published with now_serving 120

  @unit
  Scenario: A change publishes on the same tick
    Given the last broadcast had now_serving 120
    When a tick moves now_serving to 130
    Then one broadcast is published with now_serving 130

  @unit @FR-18
  Scenario Outline: Each state change is published once on sse-status
    Given the last published state for "E1" is "<from>"
    When a tick reads state "<to>"
    Then exactly one sse-status message is published with state "<to>"
    When the next tick reads state "<to>" again
    Then no sse-status message is published

    Examples:
      | from     | to       |
      | OPEN     | SOLD_OUT |
      | SOLD_OUT | OPEN     |
      | SOLD_OUT | CLOSED   |

  @unit
  Scenario: The SOLD_OUT status message carries soldOutAt
    When "E1" goes SOLD_OUT at epoch 1800000000
    Then the sse-status message is:
      """json
      {"eventId":"E1","state":"SOLD_OUT","soldOutAt":1800000000,"serverTime":1800000000}
      """

  @unit
  Scenario: A restarted controller publishes the current state once
    Given the controller just started and "E1" is "SOLD_OUT"
    When the first tick runs
    Then one sse-status message is published with state "SOLD_OUT"

  @unit @FR-18
  Scenario Outline: SOLD_OUT closes after 930 seconds
    Given "E1" is "SOLD_OUT" since epoch 1800000000
    When the clock reads epoch <now> and the tick runs
    Then the state is "<state>"

    Examples:
      | now        | state    |
      | 1800000929 | SOLD_OUT |
      | 1800000930 | CLOSED   |
      | 1800005000 | CLOSED   |
    # OPEN: 05 §2 says "older than 930 s". Proposal: close at >= 930 s, as above.

  @integration @redis-cluster
  Scenario: Stock coming back resets the CLOSED timer
    Given "E1" is "SOLD_OUT" since epoch 1800000000
    When an expired hold returns 3 tickets at epoch 1800000500 and the tick runs
    And "E1" sells out again at epoch 1800000600
    Then "E1" is still "SOLD_OUT" at epoch 1800001500
    And "E1" is "CLOSED" at epoch 1800001530

  @integration @redis-cluster
  Scenario: CLOSED is terminal even if stock comes back
    Given "E1" is "CLOSED"
    When an expired hold returns 3 tickets and the tick runs
    Then the state is still "CLOSED"
    And no customer is admitted
