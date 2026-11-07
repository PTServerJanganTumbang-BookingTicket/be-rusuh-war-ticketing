@step-6 @sse-server @stream
Feature: When something goes wrong, the stream tells me the truth
  The keepalive shows the connection is alive; only fresh broadcasts show the numbers are alive.
  "stale" is about the numbers, is sent once per change, and my app must not reconnect because of it.

  Sources: api-contracts §3.2 · 02-queue-status-stream.md · impl-plan §8.5–8.6

  Background:
    Given event "E1" is "OPEN" with now_serving 31600 and rate_per_min 318
    And the clock is frozen at "2026-10-24T13:10:00.000Z"
    And I am connected to "E1"

  # ------------------------------------------------------------ connection alive

  @unit
  Scenario: A quiet connection is kept alive every 15 seconds
    When 45 seconds pass with no broadcast
    Then I received exactly 3 keepalives, each ": keepalive" and a blank line
    And none of them triggered my app's event handlers

  # ------------------------------------------------------------ numbers frozen

  @unit @FR-07
  Scenario: If admission goes silent for 30 seconds I am told once, and I stay connected
    When 31 seconds pass with no broadcast
    Then I receive:
      """
      event: stale
      data: {"stale":true,"reason":"no_admission_updates"}

      """
    And 90 more seconds pass without a second "stale" message

  @unit
  Scenario: When updates return, I am told the numbers are fresh again
    Given I received "stale" with reason "no_admission_updates"
    When a broadcast with nowServing 31650 is published
    Then I receive exactly:
      """
      event: stale
      data: {"stale":false}

      """
    And then a normal message with nowServing 31650

  @unit
  Scenario: Heartbeats every 10 seconds keep me fresh, even while fully booked
    Given "E1" is "FULL_BOOKED" and nowServing does not move
    When 90 seconds pass with a heartbeat every 10 seconds
    Then I receive 0 "stale" messages

  @unit
  Scenario: Silence before the sale opens is not stale
    Given "E1" is "SCHEDULED"
    When 120 seconds pass with no broadcast
    Then I receive 0 "stale" messages

  @unit
  Scenario: Staleness is per event: a silent event does not dim another
    Given another customer is connected to "E2"
    When only "E2" keeps receiving broadcasts for 40 seconds
    Then I receive "stale" and the "E2" customer does not

  @unit
  Scenario: Joining an event that is already stale shows it straight away
    Given no broadcast arrived for 40 seconds
    When a second customer connects
    Then they receive their snapshot followed by "stale" with reason "no_admission_updates"

  @unit
  Scenario: A server that just started does not call a quiet event stale
    Given the server started 5 minutes ago and nobody watched "E1"
    When a customer connects
    Then they receive no "stale" during their first 29 seconds

  # ------------------------------------------------------------ redis lost

  @unit @NFR-14
  Scenario: If the server loses Redis I am told, and I stay connected
    When the Redis pub/sub connection drops
    Then I receive:
      """
      event: stale
      data: {"stale":true,"reason":"redis_disconnected"}

      """
    And my connection stays open and keepalives continue

  @unit @NFR-14
  Scenario: The server reconnects to Redis with growing, jittered delays
    Given the Redis pub/sub connection dropped
    When Redis comes back
    Then the server subscribes again to "sse-broadcast" and "sse-status"
    And the delay between attempts grows, is capped at 5 seconds and has jitter

  @unit @NFR-14
  Scenario: I am only told "fresh" after a real broadcast
    Given I received "stale" with reason "redis_disconnected"
    When the subscription is restored
    Then I have not yet received "stale" false
    When the next broadcast arrives
    Then I receive "stale" false, then that broadcast

  @unit
  Scenario: If the sale closed while Redis was down, I still get the ending
    Given the Redis pub/sub connection dropped and "E1" became "CLOSED" meanwhile
    When the subscription is restored
    Then I receive "status" CLOSED, then "sold-out", and my response ends

  @unit
  Scenario: A second cause while already stale does not send a second "stale"
    Given I received "stale" with reason "no_admission_updates"
    When the Redis pub/sub connection drops
    Then I received exactly 1 "stale" true

  @unit @NFR-14
  Scenario: A server that cannot reach Redis keeps serving the customers it has
    Given Redis is unreachable for 5 minutes
    Then my connection is still open and I received "stale" once
    And GET /health reports the server as degraded but alive

  # ------------------------------------------------------------ server restarts

  @unit
  Scenario: On shutdown every customer is told to reconnect at a different moment
    Given 100 customers are connected
    When the server receives SIGTERM
    Then each receives one "reconnect" message with a retry between 1000 and 15000 ms
    And no single retry value is held by more than 5 % of them
    And all connections close within 20 seconds and the process exits 0

  @unit
  Scenario: The reconnect message has the documented shape
    When the server receives SIGTERM
    Then I receive:
      """
      event: reconnect
      retry: 7300
      data: {}

      """
    # 7300 is an example; the real value is random in [1000, 15000].
    # Apidog needs a 5th itemSchema branch "QueueStreamReconnect" = {} (documentations, ask first).

  @unit
  Scenario: A server that is shutting down refuses new customers and fails its health check
    Given the server is draining after SIGTERM
    When a new customer connects with a valid token
    Then the response status is 503 with error_code "SERVICE_UNAVAILABLE"
    And GET /health answers 503

  @unit
  Scenario: Customers who ignore the reconnect hint are closed at 20 seconds
    Given 3 customers ignore the reconnect hint
    When 20 seconds pass after SIGTERM
    Then their connections are closed by the server and the process exits 0

  # ------------------------------------------------------------ health and load

  @unit
  Scenario: Health is OK when Redis is reachable and the server is not draining
    When GET /health is called
    Then the response status is 200

  @unit
  Scenario: The server reports how stale the numbers are and how many customers it holds
    When 12 seconds pass with no broadcast
    Then the gauge "CtlHeartbeatAge" for "E1" is 12
    And the gauge "SSEConnections" for "E1" is 1

  @integration @redis-cluster @NFR-14
  Scenario: A real Redis restart: stale, then fresh numbers, nobody disconnected
    Given 20 customers are connected on a real Redis cluster
    When the Redis node is restarted
    Then all 20 receive "stale" redis_disconnected exactly once and stay connected
    When admission-ctl publishes a broadcast after Redis is back
    Then all 20 receive "stale" false, then the broadcast

  @integration @redis-cluster @slow @NFR-06
  Scenario: Fan-out p95 stays under 1 second with 5,000 connections on one server
    Given 5000 customers are connected to "E1" on one server
    When 20 broadcasts are published one second apart
    Then the p95 from PUBLISH to the last byte read is under 1 second
    # Connections per task is a load-test output (ADR-005 §17 #4); this is a smoke bound.
