@step-6 @sse-server @stream @rule-2
Feature: Following the queue while I wait
  As a waiting customer I get the same live numbers as everyone else and work out my own position.
  The server only repeats what admission-ctl publishes. It never sends anything personal and has no "grant" message.

  Sources: api-contracts §3.2 · 02-queue-status-stream.md · ADR-006 §4.2

  Background:
    Given event "E1" is "OPEN" with now_serving 31600 and rate_per_min 318
    And the clock is frozen at "2026-10-24T13:10:01.004Z"
    And I am connected to "E1"

  # ------------------------------------------------------------ the numbers

  @unit @rule-2
  Scenario: When the queue moves I get the new numbers
    When admission-ctl publishes on sse-broadcast:
      """json
      {"eventId":"E1","nowServing":31604,"state":"OPEN","ratePerMin":318,"serverTime":1792847401}
      """
    Then I receive:
      """
      id: 31604
      data: {"nowServing":31604,"state":"OPEN","ratePerMin":318,"serverTime":"2026-10-24T13:10:01.004Z"}

      """
    # serverTime is this server's clock, so I can sync mine (BR-10); event id and ctl's time never reach me.

  @unit @rule-2 @NFR-06
  Scenario: Everyone watching the event receives identical bytes
    Given 500 other customers are connected to "E1"
    When a broadcast for "E1" is published
    Then all 501 of us receive identical bytes
    And the message was built once, not once per customer

  @unit @rule-2
  Scenario: I only hear about my own event
    Given 2 customers are connected to "E2"
    When a broadcast for "E1" is published
    Then I receive it and the customers of "E2" receive nothing

  @unit @FR-08
  Scenario: The server does not decide my turn; it shows the same number to everyone
    Given my ticket_no is 31500
    When a broadcast with nowServing 31604 is published
    Then I receive nowServing 31604
    And no "grant" message is ever sent to anyone

  @unit
  Scenario: The queue number never goes backwards on my screen
    Given the last nowServing I received was 31610
    When a late broadcast with nowServing 31604 is published
    Then I receive nothing
    But a heartbeat with nowServing 31610 is delivered

  @unit
  Scenario Outline: A broken message from the server side never reaches me
    When admission-ctl publishes on sse-broadcast: <message>
    Then I receive nothing and stay connected
    And the metric "SSEBadMessage" is 1

    Examples:
      | message                                                                          |
      | not json                                                                         |
      | {"nowServing":1,"state":"OPEN","ratePerMin":1,"serverTime":1}                    |
      | {"eventId":"E1","nowServing":1,"state":"PAUSED","ratePerMin":1,"serverTime":1}   |

  # ------------------------------------------------------------ fully booked and closed

  @unit @rule-2
  Scenario: When tickets run out I am told once and I stay connected
    When admission-ctl publishes on sse-status:
      """json
      {"eventId":"E1","state":"FULL_BOOKED","fullBookedAt":1792847380,"serverTime":1792847401}
      """
    Then I receive:
      """
      event: status
      data: {"state":"FULL_BOOKED","fullBookedAt":"2026-10-24T13:09:40.000Z","serverTime":"2026-10-24T13:10:01.004Z"}

      """
    And I am still connected

  @unit @rule-2
  Scenario: When seats come back I am told and my place carries on
    Given "E1" is "FULL_BOOKED"
    When admission-ctl publishes on sse-status state "OPEN" for "E1"
    Then I receive a "status" message with state "OPEN" and fullBookedAt null
    And I am still connected

  @unit
  Scenario: Being told about the same state twice sends one message
    Given "E1" is "FULL_BOOKED"
    When admission-ctl publishes on sse-status state "FULL_BOOKED" for "E1" twice
    Then I receive 0 "status" messages

  @unit @rule-2
  Scenario: When the sale closes I get the final message and the stream ends
    When admission-ctl publishes on sse-status state "CLOSED" for "E1"
    Then I receive, in this order:
      """
      event: status
      data: {"state":"CLOSED","fullBookedAt":null,"serverTime":"2026-10-24T13:10:01.004Z"}

      event: sold-out
      data: {"message":"Tiket sudah habis"}

      """
    And my response ends and I am no longer registered

  @unit
  Scenario: If I connected just as the sale closed, the next update still ends my stream
    Given I registered after the CLOSED message was published
    When a broadcast with state "CLOSED" is published
    Then I receive "status" CLOSED, then "sold-out", and my response ends

  @unit
  Scenario: One event closing does not end another event's streams
    Given 2 customers are connected to "E2"
    When admission-ctl publishes on sse-status state "CLOSED" for "E1"
    Then the customers of "E2" are still connected

  # ------------------------------------------------------------ the server's bookkeeping

  @unit
  Scenario: Leaving frees my spot: no leaks after 1000 connect-and-leave cycles
    When 1000 customers connect to "E1" and leave
    Then "E1" has only my connection
    And the goroutine count is back to its start

  @unit
  Scenario: One customer leaving mid-broadcast does not hurt the others
    Given 100 customers are connected and one leaves during a broadcast
    When a broadcast for "E1" is published
    Then the other 99 receive it

  @unit @NFR-06
  Scenario: A phone that cannot keep up is dropped and the others are not slowed
    Given 100 customers are connected and 1 never reads
    When 50 broadcasts are published
    Then the slow connection is closed and counted in "SSEWriteBackpressure"
    And the other 99 received all 50
    And no broadcast waited more than 50 ms on the slow one

  @unit
  Scenario: A dropped phone reconnects and gets a fresh snapshot
    Given my connection was dropped for being too slow
    When I reconnect
    Then my first message has the current nowServing

  # ------------------------------------------------------------ across servers

  @integration @redis-cluster @NFR-06
  Scenario: Two servers each fan out to their own customers in under a second
    Given 2 sse-servers with 50 customers each connected to "E1"
    When admission-ctl publishes one broadcast
    Then all 100 receive it in under 1 second, with identical bytes

  @integration @redis-cluster @rule-2
  Scenario: CLOSED reaches customers on every server
    Given 2 sse-servers with 5 customers each connected to "E1"
    When admission-ctl publishes sse-status state "CLOSED"
    Then all 10 receive "status", then "sold-out", and their responses end

  @e2e @integration @redis-cluster @postgres @after-step-3 @FR-08
  Scenario: With the real controller, my turn arrives
    Given the real admission-ctl runs for "E1" and my ticket_no is 31650
    When the controller advances now_serving to 31650 or more
    Then I receive a broadcast with nowServing at or above 31650
    And GET /queue/me then answers GRANTED
