@step-4 @pkg-apinizer @unit
Feature: Every response uses the contract envelope
  Today's WriteEnvelope writes only meta.code and meta.message, and writes "data": null
  for nil. The contract requires meta.server_time on every response, meta.error_code on
  every 4xx/5xx, and data always present ({} when empty, never null).
  This change is shared by every service (booking included).

  Sources: api-contracts "Response Structure", Appendix A · BR-10

  Background:
    Given the clock is frozen at "2026-11-10T02:00:05Z"

  Scenario: A success response
    When a handler writes status 200, message "Success" and data {"a": 1}
    Then the body is:
      """json
      {"meta": {"code": 200, "message": "Success", "server_time": "2026-11-10T02:00:05Z"}, "data": {"a": 1}}
      """
    And the Content-Type is "application/json"

  Scenario: An error response carries the machine code
    When a handler writes the app error 409 "SALE_NOT_OPEN" "Penjualan belum dibuka" with data {"queue_state": "NOT_OPEN"}
    Then the body is:
      """json
      {"meta": {"code": 409, "message": "Penjualan belum dibuka", "error_code": "SALE_NOT_OPEN", "server_time": "2026-11-10T02:00:05Z"}, "data": {"queue_state": "NOT_OPEN"}}
      """

  Scenario: Nil data becomes an empty object
    When a handler writes status 200 with nil data
    Then data is {}

  Scenario: An unexpected error never leaks internals
    When a handler returns a plain Go error "pq: connection refused at 10.0.0.5"
    Then the status is 500 with error_code "INTERNAL_ERROR"
    And the message does not contain "10.0.0.5"
    # OPEN: Appendix A has no 500 code. Proposal: add INTERNAL_ERROR.

  Scenario: Existing booking service responses keep working
    When the booking service test suite runs against the new envelope
    Then it passes, and its responses now also carry meta.server_time
