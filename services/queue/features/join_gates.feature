@step-4 @queue-service @join
Feature: Join says no early and in a fixed order, and a refusal never costs me a ticket number
  Order: request shape → identity → rate limit → CAPTCHA → sale state → rejoin → fully booked → capacity → issue ticket.
  Rate limit before CAPTCHA: refused attempts never cost an external call.
  Fully booked after rejoin: people already in line keep their place; only new places are refused.
  Errors use the standard envelope (meta.code, message, error_code, server_time; data {}).

  Sources: api-contracts §3.1, Appendix A · 01-join-queue.md §2, §3, §5 · impl-plan §7.2

  Background:
    Given event "E1" is "OPEN" with now_serving 100 and issued 149
    And QUEUE_CAPACITY is 300000
    And the CAPTCHA provider accepts token "cap-ok" and rejects token "cap-bad"

  # ------------------------------------------------------------ order

  @unit
  Scenario Outline: Outline: The first failing check decides the answer; later checks are not run
    Given the request fails gate "<gate>"
    And every later gate would also fail
    When a guest joins "E1"
    Then the response status is <status> with error_code "<code>"
    And no gate after "<gate>" was called
    And queue:{E1} issued is still 149

    Examples:
      | gate              | status | code               |
      | request shape     | 400    | VALIDATION_ERROR   |
      | identity          | 400    | DEVICE_ID_REQUIRED |
      | rate limit        | 429    | TOO_MANY_REQUESTS  |
      | CAPTCHA required  | 400    | CAPTCHA_REQUIRED   |
      | CAPTCHA           | 403    | CAPTCHA_FAILED     |
      | sale state        | 409    | SALE_NOT_OPEN      |
      | fully booked          | 409    | FULL_BOOKED           |
      | capacity          | 503    | QUEUE_FULL         |

  @unit
  Scenario: If I am rate limited, the CAPTCHA provider is never called
    Given IP "203.0.113.7" has used its 20 joins in the last 5 minutes
    When a guest from IP "203.0.113.7" joins "E1" with captcha "cap-ok"
    Then the response status is 429
    And the CAPTCHA provider was not called

  # ------------------------------------------------------------ request shape

  @unit
  Scenario Outline: Outline: A malformed request is refused
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" posts body <body> to join "E1"
    Then the response status is <status> with error_code "<code>"

    Examples:
      | body                      | status | code             |
      | "not json"                | 400    | VALIDATION_ERROR |
      | "{\"captcha_token\": 5}"  | 400    | VALIDATION_ERROR |
      | "{}"                      | 400    | CAPTCHA_REQUIRED |
      | "{\"captcha_token\": \"\"}" | 400  | CAPTCHA_REQUIRED |

  @unit
  Scenario: An oversized request is refused without being read
    When a guest posts a 1 MB body to join "E1"
    Then the response status is 400 with error_code "VALIDATION_ERROR"

  @unit
  Scenario Outline: Outline: An event id that cannot exist is "not found"
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins event "<event_id>" with captcha "cap-ok"
    Then the response status is 404 with error_code "NOT_FOUND"

    Examples:
      | event_id |
      | E{1      |
      | E}1      |

  # ------------------------------------------------------------ identity

  @unit
  Scenario: As a guest I must send a device id
    When a guest without a device id joins "E1" with captcha "cap-ok"
    Then the response status is 400 with error_code "DEVICE_ID_REQUIRED"

  @unit
  Scenario: As a logged-in customer I do not need a device id
    Given account "acc-1" has a valid access token
    When account "acc-1" without a device id joins "E1" with captcha "cap-ok"
    Then the response status is 200

  @unit
  Scenario Outline: Any non-empty device id is accepted (no format check)
    When a guest with device id "<device_id>" joins "E1" with captcha "cap-ok"
    Then the response status is 200

    Examples:
      | device_id         |
      | short             |
      | has space inside! |

  @unit
  Scenario Outline: Outline: A bad login token is an error, never a silent guest join
    When a caller with <token> and device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 401 with error_code "<code>"

    Examples:
      | token                    | code          |
      | an expired access token  | TOKEN_EXPIRED |
      | a malformed access token | TOKEN_INVALID |

  # ------------------------------------------------------------ rate limits

  @integration @redis-cluster
  Scenario: One IP may join 20 times per 5 minutes
    When a guest from IP "203.0.113.7" makes 20 join attempts within 1 minute
    Then all 20 are answered without 429
    When the same IP makes a 21st attempt
    Then the response status is 429 with error_code "TOO_MANY_REQUESTS"
    And data.retry_after is a positive number of seconds
    And the response has a Retry-After header with the same value

  @integration @redis-cluster
  Scenario: One account may join 3 times per minute, on top of the IP limit
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
  Scenario: My IP is what the gateway saw, not a header I can fake
    Given the gateway reports source IP "203.0.113.7"
    When a guest sends "X-Forwarded-For: 198.51.100.1" and joins "E1"
    Then the rate limit is charged to "203.0.113.7"

  @unit
  Scenario: If the rate limiter is down, I can still join
    Given the rate limiter errors
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200
    And the metric "RateLimitFailOpen" is incremented

  # ------------------------------------------------------------ CAPTCHA

  @unit
  Scenario: A failed CAPTCHA costs me no ticket number
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-bad"
    Then the response status is 403 with error_code "CAPTCHA_FAILED"
    And queue:{E1} issued is still 149

  @unit @NFR-13
  Scenario Outline: Outline: If the CAPTCHA provider has trouble, I can still join
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
  Scenario: Logged-in customers pass the CAPTCHA too
    Given account "acc-1" has a valid access token
    When account "acc-1" joins "E1" with captcha "cap-bad"
    Then the response status is 403 with error_code "CAPTCHA_FAILED"

  # ------------------------------------------------------------ sale state

  @unit
  Scenario: The sale has not opened yet
    Given event "E1" is "SCHEDULED" with sale_starts_at "2026-11-10T02:00:00Z"
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 409 with error_code "SALE_NOT_OPEN"
    And data is {"queue_state": "NOT_OPEN", "sale_starts_at": "2026-11-10T02:00:00Z"}
    # Decided: sale-ops seeds both as fields of queue:{E1}, next to now_serving.

  @unit
  Scenario: The sale is closed
    Given event "E1" is "CLOSED"
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 410 with error_code "SALE_CLOSED"
    And the message is "Tiket sudah habis"
    And data is {"queue_state": "CLOSED"}

  @integration @redis-cluster @FR-18
  Scenario: While fully booked, a new customer is refused and no number is spent
    Given event "E1" is "FULL_BOOKED" with full_booked_at "2026-11-10T02:40:12Z"
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 409 with error_code "FULL_BOOKED"
    And data is {"queue_state": "FULL_BOOKED", "full_booked_at": "2026-11-10T02:40:12Z"}
    And queue:{E1} issued is still 149

  @integration @redis-cluster @FR-18
  Scenario: Joining works again once seats come back
    Given event "E1" was "FULL_BOOKED" and is now "OPEN"
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200
    And data.ticket_no is 150

  @unit
  Scenario Outline: Outline: An event that was never set up is not shown as "not open"
    Given event "E1" has <state>
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 503 with error_code "QUEUE_NOT_READY"
    And queue:{E1} issued is still 149

    Examples:
      | state                       |
      | no state key                |
      | the unknown state "PAUSED"  |

  # ------------------------------------------------------------ capacity

  @integration @redis-cluster
  Scenario: A full queue refuses me without spending a number
    Given QUEUE_CAPACITY is 49
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 503 with error_code "QUEUE_FULL"
    And data is {"queue_state": "FULL", "queue_capacity": 49}
    And queue:{E1} issued is still 149

  @integration @redis-cluster
  Scenario: The last place is still given out
    Given QUEUE_CAPACITY is 50
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200 and data.ticket_no is 150

  @integration @redis-cluster
  Scenario: If Redis is down, I am told to retry
    Given Redis is unreachable
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 503
    And the body is a valid error envelope
