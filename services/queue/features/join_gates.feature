@step-4 @queue-service @join
Feature: Join rejects early, in a fixed order, and a rejected call never burns a ticket number
  Gate order (approved 2026-11-06):
    1 request shape → 2 identity (device / Bearer) → 3 rate limit → 4 CAPTCHA →
    5 sale state (SCHEDULED / CLOSED / unseeded) → 6 rejoin (BR-09 token, BR-07 account) →
    7 sold out → 8 capacity → 9 issue ticket
  Rate limit comes before CAPTCHA so rejected attempts never cost an external call.
  Sold out comes after rejoin: customers already in line keep their place while stock may
  come back; only NEW places are refused (like a waiting room's "reject" mode).

  Every error uses the envelope: meta.code, meta.message, meta.error_code, meta.server_time, data {}.

  Sources: api-contracts §3.1, Appendix A · 01-join-queue.md §2, §3, §5 · impl-plan §7.2 steps 1–5

  Background:
    Given event "E1" is "OPEN" with now_serving 100 and issued 149
    And QUEUE_CAPACITY is 300000
    And the CAPTCHA provider accepts token "cap-ok" and rejects token "cap-bad"

  # ------------------------------------------------------------ order

  @unit
  Scenario Outline: The first failing gate decides the answer, and later gates are not called
    Given the request fails gate "<gate>"
    And every later gate would also fail
    When a guest joins "E1"
    Then the response status is <status> with error_code "<code>"
    And no gate after "<gate>" was called
    And queue:{E1}:issued is still 149

    Examples:
      | gate              | status | code               |
      | request shape     | 400    | VALIDATION_ERROR   |
      | identity          | 400    | DEVICE_ID_REQUIRED |
      | rate limit        | 429    | TOO_MANY_REQUESTS  |
      | CAPTCHA required  | 400    | CAPTCHA_REQUIRED   |
      | CAPTCHA           | 403    | CAPTCHA_FAILED     |
      | sale state        | 409    | SALE_NOT_OPEN      |
      | sold out          | 409    | SOLD_OUT           |
      | capacity          | 503    | QUEUE_FULL         |

  @unit
  Scenario: A rate-limited caller never reaches the CAPTCHA provider
    Given IP "203.0.113.7" has used its 20 joins in the last 5 minutes
    When a guest from IP "203.0.113.7" joins "E1" with captcha "cap-ok"
    Then the response status is 429
    And the CAPTCHA provider was not called

  # ------------------------------------------------------------ request shape

  @unit
  Scenario Outline: A malformed body is rejected
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" posts body <body> to join "E1"
    Then the response status is <status> with error_code "<code>"

    Examples:
      | body                      | status | code             |
      | "not json"                | 400    | VALIDATION_ERROR |
      | "{\"captcha_token\": 5}"  | 400    | VALIDATION_ERROR |
      | "{}"                      | 400    | CAPTCHA_REQUIRED |
      | "{\"captcha_token\": \"\"}" | 400  | CAPTCHA_REQUIRED |

  @unit
  Scenario: An oversized body is rejected without parsing
    When a guest posts a 1 MB body to join "E1"
    Then the response status is 400 with error_code "VALIDATION_ERROR"

  @unit
  Scenario Outline: An event id that cannot be a key is not found
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins event "<event_id>" with captcha "cap-ok"
    Then the response status is 404 with error_code "NOT_FOUND"

    Examples:
      | event_id |
      | E{1      |
      | E}1      |

  # ------------------------------------------------------------ identity

  @unit
  Scenario: A guest without a device id is rejected
    When a guest without a device id joins "E1" with captcha "cap-ok"
    Then the response status is 400 with error_code "DEVICE_ID_REQUIRED"

  @unit
  Scenario: A logged-in customer does not need a device id
    Given account "acc-1" has a valid access token
    When account "acc-1" without a device id joins "E1" with captcha "cap-ok"
    Then the response status is 200

  @unit @open-question
  Scenario Outline: A bad device id is rejected
    # OPEN: the contract says "random 128-bit value" but sets no format.
    # Proposal: 16–128 chars of [A-Za-z0-9_-], else 400 VALIDATION_ERROR.
    When a guest with device id "<device_id>" joins "E1" with captcha "cap-ok"
    Then the response status is 400 with error_code "VALIDATION_ERROR"

    Examples:
      | device_id         |
      | short             |
      | has space inside! |

  @unit @open-question
  Scenario Outline: A bad access token is an error, not a silent guest join
    # OPEN: not specified. Treating it as a guest would let a caller dodge BR-07 by
    # sending garbage. Proposal: reject with the session codes from Appendix A.
    When a caller with <token> and device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 401 with error_code "<code>"

    Examples:
      | token                    | code          |
      | an expired access token  | TOKEN_EXPIRED |
      | a malformed access token | TOKEN_INVALID |

  # ------------------------------------------------------------ rate limits

  @integration @redis-cluster
  Scenario: Per-IP limit is 20 joins per 5 minutes
    When a guest from IP "203.0.113.7" makes 20 join attempts within 1 minute
    Then all 20 are answered without 429
    When the same IP makes a 21st attempt
    Then the response status is 429 with error_code "TOO_MANY_REQUESTS"
    And data.retry_after is a positive number of seconds
    And the response has a Retry-After header with the same value

  @integration @redis-cluster
  Scenario: Per-account limit is 3 joins per minute, on top of the IP limit
    Given account "acc-1" has a valid access token
    When account "acc-1" makes 3 join attempts within 1 minute from 3 different IPs
    And account "acc-1" makes a 4th attempt from a 4th IP
    Then the 4th response status is 429

  @integration @redis-cluster
  Scenario: The limit refills over time
    Given IP "203.0.113.7" has used its 20 joins
    When 15 seconds pass
    Then IP "203.0.113.7" may join once more
    # 20 per 300 s = one token every 15 s.

  @unit
  Scenario: The client IP comes from the gateway, not from a spoofable header
    Given the gateway reports source IP "203.0.113.7"
    When a guest sends "X-Forwarded-For: 198.51.100.1" and joins "E1"
    Then the rate limit is charged to "203.0.113.7"

  @unit
  Scenario: Rate limiter outage fails open
    # OPEN: not specified. Fairness, not correctness, is at stake (same rule as CAPTCHA).
    # Proposal: allow the join and emit metric "RateLimitFailOpen".
    Given the rate limiter errors
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200
    And the metric "RateLimitFailOpen" is incremented

  # ------------------------------------------------------------ CAPTCHA

  @unit
  Scenario: An invalid CAPTCHA token costs no ticket number
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-bad"
    Then the response status is 403 with error_code "CAPTCHA_FAILED"
    And queue:{E1}:issued is still 149

  @unit @NFR-13
  Scenario Outline: CAPTCHA provider trouble fails open
    Given the CAPTCHA provider <trouble>
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200
    And the metric "CaptchaFailOpenSeconds" is recorded

    Examples:
      | trouble                            |
      | times out after the 1 s deadline    |
      | answers HTTP 500                    |
      | is unreachable                      |
      | answers a body that is not JSON     |

  @unit
  Scenario: Logged-in customers must pass CAPTCHA too
    Given account "acc-1" has a valid access token
    When account "acc-1" joins "E1" with captcha "cap-bad"
    Then the response status is 403 with error_code "CAPTCHA_FAILED"

  # ------------------------------------------------------------ sale state

  @unit
  Scenario: Sale not open yet
    Given event "E1" is "SCHEDULED" with sale_starts_at "2026-11-10T02:00:00Z"
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 409 with error_code "SALE_NOT_OPEN"
    And data is {"queue_state": "NOT_OPEN", "sale_starts_at": "2026-11-10T02:00:00Z"}
    # OPEN: where join reads sale_starts_at (and the event name for the 200 body).
    # Proposal: sale-ops seeds both as fields of queue:{E1}, next to now_serving.

  @unit
  Scenario: Sale closed
    Given event "E1" is "CLOSED"
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 410 with error_code "SALE_CLOSED"
    And data is {"queue_state": "CLOSED"}

  @integration @redis-cluster @FR-18
  Scenario: A new customer is refused while SOLD_OUT, without burning a number
    # Decided 2026-11-06 (Erma). New code SOLD_OUT, to be added to Appendix A + Apidog.
    Given event "E1" is "SOLD_OUT" with sold_out_at "2026-11-10T02:40:12Z"
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 409 with error_code "SOLD_OUT"
    And data is {"queue_state": "SOLD_OUT", "sold_out_at": "2026-11-10T02:40:12Z"}
    And queue:{E1}:issued is still 149

  @integration @redis-cluster @FR-18
  Scenario: Joining works again once stock comes back
    Given event "E1" was "SOLD_OUT" and is now "OPEN"
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200
    And data.ticket_no is 150

  @unit @open-question
  Scenario Outline: An event that was never seeded is not treated as "not open"
    # OPEN: impl-plan says 503, but Appendix A has no code for it.
    # Proposal: add QUEUE_NOT_READY (503) to Appendix A.
    Given event "E1" has <state>
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 503 with error_code "QUEUE_NOT_READY"
    And queue:{E1}:issued is still 149

    Examples:
      | state                       |
      | no state key                |
      | the unknown state "PAUSED"  |

  # ------------------------------------------------------------ capacity

  @integration @redis-cluster
  Scenario: A full queue is refused without issuing a number
    Given QUEUE_CAPACITY is 49
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 503 with error_code "QUEUE_FULL"
    And data is {"queue_state": "FULL", "queue_capacity": 49}
    And queue:{E1}:issued is still 149

  @integration @redis-cluster
  Scenario: One place left is still given out
    Given QUEUE_CAPACITY is 50
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200 and data.ticket_no is 150

  @integration @redis-cluster
  Scenario: Redis down fails closed
    Given Redis is unreachable
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 503
    And the body is a valid error envelope
