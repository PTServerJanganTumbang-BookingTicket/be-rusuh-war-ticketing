@step-6 @sse-server @stream
Feature: Opening the waiting room
  As a customer in the queue, I open the live stream and see where I stand straight away.
  Checks run in order: rate limit → sale state → my token. Web sends the queue_token cookie, mobile sends X-Queue-Token.

  Sources: api-contracts §3.2 · 02-queue-status-stream.md

  Background:
    Given event "E1" is "OPEN" with now_serving 31600 and rate_per_min 318
    And the clock is frozen at "2026-10-24T13:10:00.412Z"
    And queue token "qt_alice" exists for event "E1" with ticket_no 31700

  # ------------------------------------------------------------ I get in

  @unit @FR-10
  Scenario: I see the current numbers immediately
    When I open the stream with cookie queue_token=qt_alice
    Then the first thing I receive is:
      """
      id: 31600
      data: {"nowServing":31600,"state":"OPEN","ratePerMin":318,"serverTime":"2026-10-24T13:10:00.412Z"}

      """

  @unit
  Scenario Outline: Web (cookie) and mobile (header) both work
    When I open the stream with <credential>
    Then I am connected to a "text/event-stream" with no caching or buffering

    Examples:
      | credential                     |
      | cookie queue_token=qt_alice    |
      | header X-Queue-Token: qt_alice |

  @unit @FR-10
  Scenario: After a refresh I get today's numbers, not what I saw before
    Given I was connected when now_serving was 31600 and now_serving is now 31750
    When I reopen the stream with Last-Event-ID 31600
    Then my first message has nowServing 31750

  @unit @FR-08
  Scenario: If my turn already came, the first message shows it
    Given queue token "qt_early" exists for event "E1" with ticket_no 31500
    When I open the stream with cookie queue_token=qt_early
    Then my first message has nowServing 31600
    # The app then sees nowServing >= ticket_no and calls GET /queue/me. The server does not decide "my turn".

  @unit
  Scenario: My first message holds nothing personal
    When two customers with different tokens open the stream
    Then both first messages are identical except for serverTime
    And neither contains a ticket number, token or position

  @unit
  Scenario: A sale where admission-ctl has not set rate_per_min yet still connects
    Given the hash queue:{E1} has no rate_per_min field
    When I open the stream with cookie queue_token=qt_alice
    Then my first message has ratePerMin 0

  # ------------------------------------------------------------ the sale state

  @unit @adr-006-4
  Scenario Outline: Fully booked keeps me in; not started or closed turns me away
    Given event "E1" is "<state>"
    When I open the stream with cookie queue_token=qt_alice
    Then the response status is <status>

    Examples:
      | state     | status |
      | OPEN      | 200    |
      | FULL_BOOKED  | 200    |
      | SCHEDULED | 409    |
      | CLOSED    | 410    |

  @unit
  Scenario: A closed sale answers 410 before it looks at my token
    Given event "E1" is "CLOSED"
    When I open the stream with no credential at all
    Then the response status is 410 with error_code "SALE_CLOSED"
    And the message is "Tiket sudah habis"

  @unit
  Scenario: An event that was never set up is not found
    Given event "E9" has no queue state in Redis
    When I open the stream of "E9" with cookie queue_token=qt_alice
    Then the response status is 404 with error_code "NOT_FOUND"

  # ------------------------------------------------------------ my token

  @unit
  Scenario Outline: A missing, unknown or other-event token is refused
    Given queue token "qt_other" exists for event "E2" with ticket_no 5
    When I open the stream with <credential>
    Then the response status is 401 with error_code "UNAUTHORIZED"
    And no connection is registered for "E1"

    Examples:
      | credential                           |
      | no credential at all                 |
      | cookie queue_token=qt_nobody         |
      | cookie queue_token=qt_other          |
      | header Authorization: Bearer qt_alice |

  @unit @security
  Scenario: A token in the URL is ignored
    When I open the stream with query "?queue_token=qt_alice" and no other credential
    Then the response status is 401

  @unit @security
  Scenario: The stream does not check device or account (it carries only public numbers)
    Given queue token "qt_alice" was issued to a guest on device "dev-phone-0000000001"
    When I open the stream with header X-Queue-Token: qt_alice and header "X-Device-Id: dev-other-000000009"
    Then the response status is 200

  @unit @security
  Scenario: My token never appears in logs or metrics
    When I open the stream with cookie queue_token=qt_alice
    Then no log line and no metric label contains "qt_alice"

  # ------------------------------------------------------------ limits and outages

  @unit @NFR-14
  Scenario: The sixth connection from one IP in a second is refused; other IPs are fine
    Given IP "203.0.113.7" opened 5 streams in the last second
    When I open the stream from IP "203.0.113.7" with cookie queue_token=qt_alice
    Then the response status is 429 with error_code "TOO_MANY_REQUESTS"
    And a Retry-After header is present
    And I can still open the stream from IP "198.51.100.9"

  @unit
  Scenario: If the rate limiter is down I still get in
    Given the rate limiter cannot reach Redis
    When I open the stream with cookie queue_token=qt_alice
    Then the response status is 200

  @unit
  Scenario: If Redis is down I am told to retry, not left on a blank screen
    Given Redis is unreachable
    When I open the stream with cookie queue_token=qt_alice
    Then the response status is 503 with error_code "SERVICE_UNAVAILABLE"
    And a Retry-After header is present
