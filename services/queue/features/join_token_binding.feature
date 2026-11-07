@step-4 @queue-service @join @BR-09 @security
Feature: Only the owner can use my queue_token
  Guest token: my X-Device-Id must match the device that joined. Logged-in token: my Bearer account must match.
  No match → 401, nothing issued, the owner's token is untouched. Unknown or other-event tokens are still ignored.

  Sources: ADR-005 §12 (amended by ADR-006 §8.9) · api-contracts §3.1, Appendix A

  Background:
    Given event "E1" is "OPEN" with now_serving 31520 and issued 40000
    And the CAPTCHA provider accepts token "cap-ok"
    And a guest with device id "dev-phone-0000000001" joined "E1" earlier with ticket number 32748 and queue_token "qt_G"
    And account "acc-1" joined "E1" earlier from device "dev-phone-0000000001" with ticket number 32800 and queue_token "qt_A"

  # ------------------------------------------------------------ guest token: device

  @integration @redis-cluster
  Scenario: As a guest on my own device I get my place back
    When a guest with device id "dev-phone-0000000001" joins "E1" sending queue_token "qt_G" with captcha "cap-ok"
    Then data has ticket_no 32748, queue_token "qt_G", rejoined true, state "QUEUED"

  @integration @redis-cluster
  Scenario: As a guest on another device I am refused, and the owner keeps the place
    When a guest with device id "dev-laptop-000000002" joins "E1" sending queue_token "qt_G" with captcha "cap-ok"
    Then the response status is 401 with error_code "DEVICE_MISMATCH"
    And queue:{E1}:issued is still 40000
    And token:qt_G field device_hash is still HMAC-SHA256(DEVICE_PEPPER, "dev-phone-0000000001") hex
    And queue:{E1}:by_ticket scores "qt_G" at 32748
    And the metric "DeviceMismatch" is incremented

  @unit
  Scenario: A guest token with no device record is refused
    Given guest token "qt_NOHASH" for "E1" with ticket number 33000 has no device_hash field
    When a guest with device id "dev-phone-0000000001" joins "E1" sending queue_token "qt_NOHASH" with captcha "cap-ok"
    Then the response status is 401 with error_code "DEVICE_MISMATCH"

  # ------------------------------------------------------------ logged-in token: account

  @integration @redis-cluster @BR-07
  Scenario Outline: Outline: As the account owner I get my place back on the same or another device
    When account "acc-1" with device id "<device>" joins "E1" sending queue_token "qt_A" with captcha "cap-ok"
    Then data has ticket_no 32800, queue_token "qt_A", rejoined true

    Examples:
      | device               |
      | dev-phone-0000000001 |
      | dev-laptop-000000002 |

  @integration @redis-cluster
  Scenario Outline: Outline: Someone else cannot use an account holder's token
    When <caller> joins "E1" sending queue_token "qt_A" with captcha "cap-ok"
    Then the response status is 401 with error_code "UNAUTHORIZED"
    And queue:{E1}:issued is still 40000
    And acct:{E1}:acc-1 holds "qt_A"

    Examples:
      | caller                                                 |
      | account "acc-2" with device id "dev-phone-0000000001"  |
      | a guest with device id "dev-phone-0000000001"          |

  @unit
  Scenario: If I log in while holding a guest token, I get my own place
    When account "acc-2" with device id "dev-phone-0000000001" joins "E1" sending queue_token "qt_G" with captcha "cap-ok"
    Then the response is a normal new join for "E1"

  # ------------------------------------------------------------ rejection details

  @unit
  Scenario: When I am refused, my queue_token cookie is cleared
    When a guest with device id "dev-laptop-000000002" joins "E1" sending queue_token "qt_G" with captcha "cap-ok"
    Then the response clears cookie "queue_token" with Path=/events/E1

  @unit
  Scenario: Rate limit and CAPTCHA are checked before token ownership
    Given IP "203.0.113.7" has used its 20 joins in the last 5 minutes
    When a guest from IP "203.0.113.7" with device id "dev-laptop-000000002" joins "E1" sending queue_token "qt_G" with captcha "cap-ok"
    Then the response status is 429

  @unit
  Scenario: The sale state is checked before token ownership
    Given event "E1" is now "CLOSED"
    When a guest with device id "dev-laptop-000000002" joins "E1" sending queue_token "qt_G" with captcha "cap-ok"
    Then the response status is 410 with error_code "SALE_CLOSED"

  @integration @redis-cluster @FR-18
  Scenario: While fully booked, a mismatch is still 401, not 409
    Given event "E1" is now "FULL_BOOKED"
    When a guest with device id "dev-laptop-000000002" joins "E1" sending queue_token "qt_G" with captcha "cap-ok"
    Then the response status is 401 with error_code "DEVICE_MISMATCH"
