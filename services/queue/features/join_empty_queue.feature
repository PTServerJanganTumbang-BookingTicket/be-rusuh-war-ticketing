@step-4 @queue-service @join @FR-05 @rule-1
Feature: On an empty queue, join waits up to 1 s for admission-ctl to grant it
  Join never grants. When the caller is first in line (ticket_no == now_serving + 1
  right after INCR), join polls Aurora's writer for the GRANTED row every 100 ms,
  for at most JOIN_GRANT_WAIT_MS (1000). If the row shows up, the caller goes
  straight to the ticket page. If not, it gets the normal queued answer.

  Until step 3 lands, @integration scenarios use a stand-in controller: a test helper
  that writes the GRANTED row after a set delay. @e2e scenarios use the real admission-ctl.

  Sources: api-contracts §3.1 (FR-05 response) · 01-join-queue.md §2, §6 · ADR-006 §4.1

  Background:
    Given event "E1" is "OPEN" with now_serving 500 and issued 500
    And JOIN_GRANT_WAIT_MS is 1000
    And the CAPTCHA provider accepts token "cap-ok"

  @integration @redis-cluster @postgres
  Scenario: The grant arrives within the wait
    Given the stand-in controller grants the next joiner after 300 ms
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200 with message "No queue — access granted"
    And data is exactly granted true, grant_token, queue_token and booking_expires_at from the row
    And the response sets cookie "queue_token" to data.queue_token
    And the response took between 300 ms and 500 ms

  @integration @redis-cluster @postgres
  Scenario: No grant within the wait falls back to the queued answer
    Given the stand-in controller does not grant
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200 with message "Successfully joined queue"
    And data.ticket_no is 501 and data.position is 1
    And the response took between 1000 ms and 1150 ms
    And the join made at most 11 reads of bookings

  @integration @redis-cluster @postgres
  Scenario: The wait reads only, on the writer
    Given the stand-in controller grants the next joiner after 300 ms
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then every SQL statement the join sent was a SELECT on the writer pool
    And the join sent no INSERT, UPDATE or DELETE

  @integration @redis-cluster @postgres
  Scenario: A non-empty queue does not wait at all
    Given now_serving is 500 and issued is 520
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then data.ticket_no is 521
    And no SQL statement was sent
    And the response took less than 100 ms

  @integration @redis-cluster @postgres
  Scenario: Two simultaneous first joiners both wait; the controller grants who fits
    Given the stand-in controller grants both joiners after 300 ms
    When two guests join "E1" at the same time
    Then one of them got ticket 501 and waited
    And the other got ticket 502 and did not wait
    # Only ticket_no == now_serving + 1 waits. 502 learns its turn from the broadcast + /me.

  @integration @redis-cluster @postgres
  Scenario: Aurora errors during the wait fall back to the queued answer
    Given Postgres fails every query
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then the response status is 200 with message "Successfully joined queue"
    And the place is kept in by_ticket

  @unit
  Scenario: The client hanging up stops the wait
    Given the stand-in controller does not grant
    When the client cancels the request after 200 ms
    Then the join stops polling within 100 ms
    And the place is kept in by_ticket

  @unit
  Scenario: The Lambda timeout leaves room for the wait
    Then the configured function timeout is at least 3 times JOIN_GRANT_WAIT_MS

  @e2e @integration @redis-cluster @postgres @after-step-3
  Scenario: With the real admission-ctl, an empty queue answers granted
    Given admission-ctl is running for "E1" with free capacity and stock
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then within 1 s the response says granted true
    And the GRANTED row was written by admission-ctl, not by join

  @e2e @integration @redis-cluster @postgres @after-step-3
  Scenario: With admission-ctl stopped, an empty queue answers queued after the wait
    Given admission-ctl is stopped
    When a guest with device id "dev-aaaaaaaaaaaaaaaa" joins "E1" with captcha "cap-ok"
    Then after at most 1.15 s the response says "Successfully joined queue"
    And no bookings row exists for the caller
