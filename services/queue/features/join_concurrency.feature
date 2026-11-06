@step-4 @queue-service @join @integration @redis-cluster @NFR-05
Feature: Ticket numbers stay unique and gap-free under a crowd
  Sources: 01-join-queue.md §7 · NFR-05 (FCFS fairness)

  Background:
    Given event "E1" is "OPEN" with now_serving 0 and issued 0
    And the CAPTCHA adapter is the mock that accepts every token
    And rate limits are disabled for this test

  Scenario: 10,000 parallel guest joins
    When 10000 guests with distinct device ids join "E1" in parallel from 200 workers
    Then every response status is 200
    And the ticket numbers are exactly 1 to 10000, each once
    And queue:{E1}:by_ticket holds 10000 members
    And there are 10000 token:* hashes for "E1"
    And every by_ticket score equals the ticket_no in its token hash

  Scenario: Arrival order is kept for sequential callers
    When guests "g1", "g2", "g3" join "E1" one after another
    Then "g1" < "g2" < "g3" by ticket number

  @open-question
  Scenario: The capacity gate may overshoot by at most the joins in flight
    # OPEN: the gate reads issued − now_serving, then INCRs: not atomic. Under a race up to
    # (parallel joins − 1) extra numbers can be issued past QUEUE_CAPACITY.
    # Proposal: accept for POC (the cap is a safety net; the home page already shows FULL).
    # Alternative: a 2-key Lua (check + INCR) on queue:{E1} and queue:{E1}:issued.
    Given QUEUE_CAPACITY is 100
    When 300 guests join "E1" in parallel from 50 workers
    Then at least 100 joins succeed
    And at most 149 joins succeed
    And the rest get 503 "QUEUE_FULL"

  @fault
  Scenario: A join that dies after INCR leaves a harmless gap
    Given the join will crash after INCR, before HSET
    When a guest joins "E1" and the join dies
    And another guest joins "E1"
    Then the second guest gets ticket number 2
    And admission-ctl later skips number 1 as a gap
