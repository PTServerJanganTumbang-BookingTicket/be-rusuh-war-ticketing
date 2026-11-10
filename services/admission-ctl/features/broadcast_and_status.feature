@step-3 @admission-ctl @rule-2
Feature: Everyone waiting hears the same news
  As a waiting customer, I hear every move of the queue and every change of the sale (fully booked, seats back,
  closed) through the stream. I work out my own turn from nowServing; nobody sends me a personal "grant".
  The controller publishes each message once (sse-broadcast, sse-status); the SSE server only repeats it.
  A sale that stays fully booked for 930 seconds closes for good.

  Sources: 05-admission-controller.md §2, §3.4 · 02-queue-status-stream.md · ADR-006 §4.2

  Background:
    Given event "E1" is "OPEN"
    And the clock is frozen at epoch 1800000000

  # ------------------------------------------------------------ the number

  @unit
  Scenario: The update holds only public numbers
    Given now_serving is 120 and rate_per_min is 900
    When a tick publishes the broadcast
    Then the sse-broadcast message for "E1" is:
      """json
      {"eventId":"E1","nowServing":120,"state":"OPEN","ratePerMin":900,"serverTime":1800000000}
      """
    And the message contains no ticket_no, queue_token or grant_token

  @unit
  Scenario: I hear the new number only after the people it lets in have their bookings
    Given 3 tokens are admitted this tick
    When the tick runs
    Then PUBLISH sse-broadcast comes after the 3rd ZREM in the call log

  @unit
  Scenario: When the queue moves, I hear it on the same tick
    Given the last broadcast had now_serving 120
    When a tick moves now_serving to 130
    Then one broadcast is published with now_serving 130

  @unit
  Scenario: When nothing moves, I still hear a heartbeat every 10 seconds
    Given the last broadcast was at epoch 1800000000 with now_serving 120
    When ticks run for 9.5 s with now_serving still 120
    Then no broadcast is published
    When the tick at 10 s runs
    Then one heartbeat broadcast is published with now_serving 120
    # The stream shows "stale" after 30 s of silence, so the heartbeat keeps a quiet queue looking alive.

  # ------------------------------------------------------------ the sale state

  @unit @FR-18
  Scenario Outline: I hear each change of the sale exactly once
    Given the last published state for "E1" is "<from>"
    When a tick reads state "<to>"
    Then exactly one sse-status message is published with state "<to>"
    When the next tick reads state "<to>" again
    Then no sse-status message is published

    Examples:
      | from        | to          |
      | OPEN        | FULL_BOOKED |
      | FULL_BOOKED | OPEN        |
      | FULL_BOOKED | CLOSED      |

  @unit
  Scenario Outline: A sale-state message says since when it is fully booked, and nothing else
    Given "E1" was "<from>" since epoch 1799999000
    When "E1" becomes "<state>" at epoch 1800000000
    Then the sse-status message is:
      """json
      {"eventId":"E1","state":"<state>","fullBookedAt":<fullBookedAt>,"serverTime":1800000000}
      """

    Examples:
      | from        | state       | fullBookedAt |
      | OPEN        | FULL_BOOKED | 1800000000   |
      | FULL_BOOKED | OPEN        | null         |
      | FULL_BOOKED | CLOSED      | null         |
    # The OPEN and CLOSED rows are new: the SSE server already reads fullBookedAt null as "none".

  @unit
  Scenario: After a controller restart I hear the current state once more
    Given the controller just started and "E1" is "FULL_BOOKED"
    When the first tick runs
    Then one sse-status message is published with state "FULL_BOOKED"
    # Harmless: the stream drops a state it already sent.

  # ------------------------------------------------------------ closing

  @unit @FR-18
  Scenario Outline: A sale fully booked for 930 seconds closes
    Given "E1" is "FULL_BOOKED" since epoch 1800000000
    When the clock reads epoch <now> and the tick runs
    Then the state is "<state>"

    Examples:
      | now        | state       |
      | 1800000929 | FULL_BOOKED |
      | 1800000930 | CLOSED      |
      | 1800005000 | CLOSED      |
    # OPEN: 05 §2 says "older than 930 s". Proposal: close at >= 930 s, as above.

  @integration @redis-cluster
  Scenario: Seats coming back restart the closing clock
    Given "E1" is "FULL_BOOKED" since epoch 1800000000
    When an expired hold returns 3 tickets at epoch 1800000500 and the tick runs
    And "E1" sells out again at epoch 1800000600
    Then "E1" is still "FULL_BOOKED" at epoch 1800001500
    And "E1" is "CLOSED" at epoch 1800001530

  @integration @redis-cluster
  Scenario: A closed sale stays closed, even if seats come back
    Given "E1" is "CLOSED"
    When an expired hold returns 3 tickets and the tick runs
    Then the state is still "CLOSED"
    And no customer is admitted
