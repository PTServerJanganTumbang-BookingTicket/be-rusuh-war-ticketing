@step-4 @queue-service @join @FR-04 @NFR-05
Feature: Join the queue (POST /events/{event_id}/queue/join), happy path
  A customer gets a first-come-first-served ticket number and a queue_token.
  Join only writes to Redis. It never grants, never runs the advance Lua,
  never publishes, and never writes to Aurora (ADR-006 §4, rule 1).

  queue_token format (proposal, matches the contract's "qt_…" example):
  "qt_" + ULID from pkg/idgen. Redis key token:<queue_token>; by_ticket member = queue_token.

  Sources: api-contracts §3.1 · Apidog "Join Queue" · 01-join-queue.md · impl-plan §7.2

  Background:
    Given event "E1" named "Konser A — Jakarta" is "OPEN"
    And now_serving is 31500 and issued is 32747
    And QUEUE_CAPACITY is 300000 and SALE_WINDOW_SECONDS is 21600
    And the CAPTCHA provider accepts token "cap-ok"
    And the clock is frozen at "2026-11-10T02:00:05Z"

  @unit
  Scenario: A guest joins and gets the next ticket number
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200
    And the response body matches:
      """json
      {
        "meta": {"code": 200, "message": "Successfully joined queue", "server_time": "2026-11-10T02:00:05Z"},
        "data": {
          "ticket_no": 32748,
          "now_serving": 31500,
          "position": 1248,
          "queue_token": "<any qt_ token>",
          "sse_url": "<configured stream url for E1>",
          "event": {"id": "E1", "name": "Konser A — Jakarta"}
        }
      }
      """
    And the response has no "estimated_wait_minutes" field
    And meta has no "error_code"

  @unit
  Scenario: The queue_token is returned both in the body and in a cookie for the sale window
    # Decided 2026-11-06 (Erma): body for mobile (X-Queue-Token), HttpOnly cookie for web
    # (EventSource cannot send headers). Join is on api.rusuhtiket.com, the stream on
    # events.rusuhtiket.com, so the cookie is set for the parent domain (config COOKIE_DOMAIN).
    Given COOKIE_DOMAIN is "rusuhtiket.com"
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then data.queue_token is a "qt_" token
    And the response sets cookie "queue_token" to the same value as data.queue_token
    And the cookie is HttpOnly, Secure, SameSite=Strict, Path=/ and Max-Age=21600
    And the cookie Domain is "rusuhtiket.com"

  @integration @redis-cluster
  Scenario: A guest join writes exactly these Redis keys
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then queue:{E1}:issued is 32748
    And token:<queue_token> is a hash with exactly these fields:
      | field       | value                                        |
      | ticket_no   | 32748                                        |
      | event_id    | E1                                           |
      | device_hash | HMAC-SHA256(DEVICE_PEPPER, "dev-aaaaaaaaaaaaaaaa") hex |
      | joined_at   | 2026-11-10T02:00:05Z                         |
    And token:<queue_token> expires in 21600 seconds
    And queue:{E1}:by_ticket scores <queue_token> at 32748
    And no acct:{E1}:* key exists
    And the raw device id is stored nowhere in Redis

  @integration @redis-cluster @BR-07
  Scenario: A logged-in customer's join is tied to the account, not the device
    Given account "acc-1" has a valid access token
    When account "acc-1" without a device id joins "E1" with captcha "cap-ok"
    Then the response status is 200
    And token:<queue_token> has field account_id "acc-1" and no device_hash field
    And acct:{E1}:acc-1 holds <queue_token> and expires in 21600 seconds

  @integration @redis-cluster @postgres @rule-1
  Scenario: Join touches nothing but Redis on a non-empty queue
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then no SQL statement was sent to Postgres
    And no PUBLISH was sent to Redis
    And no Lua script was run
    And queue:{E1} now_serving is still 31500

  @unit
  Scenario: Position is ticket_no minus now_serving
    Given now_serving is 100 and issued is 149
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then data.ticket_no is 150 and data.position is 50

  @integration @redis-cluster
  Scenario: Two guests on the same device get two separate places
    # Guests are not covered by BR-07: no stable identity (contract §3.1).
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    And a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then both responses have status 200
    And the ticket numbers are 32748 and 32749
    And the two queue_tokens differ

  @unit
  Scenario: The same handler serves Lambda and the local HTTP server
    When the join request goes through the API Gateway Lambda proxy adapter
    And the same request goes through the local HTTP server
    Then both return the same status, body shape and Set-Cookie header

  @integration @contract
  Scenario: Every documented response matches the Apidog schema
    When each documented join response is produced (200, 200 rejoined, 200 granted, 400, 403, 409, 429, 503)
    Then each body validates against its schema in documentations/api/rusuh-booking-apidog.json
    # OPEN: Apidog has no responses for 400 DEVICE_ID_REQUIRED, 400 VALIDATION_ERROR, 401,
    # 410 SALE_CLOSED and 503 not-seeded. Proposal: add them to Apidog (shared repo, ask first).

  @unit @security
  Scenario: Secrets never reach the logs
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then no log line contains "cap-ok", the queue_token, "dev-aaaaaaaaaaaaaaaa" or the access token
