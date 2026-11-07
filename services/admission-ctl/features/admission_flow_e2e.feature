@draft @step-3 @admission-ctl @integration @e2e @postgres @redis-cluster
Feature: The real controller process admits a crowd end to end
  Starts the real admission-ctl binary (cmd/worker) with a 500 ms tick against the
  docker compose Redis cluster and Postgres. Joins are written by a test helper
  using the same pkg/rediskeys calls as the join service, until step 4 replaces
  it with the real join endpoint. Bookings move on through a test helper.

  Sources: 05 · README §2, §5 · ADR-006

  Background:
    Given the docker compose stack is up with migrations applied
    And event "E1" is seeded "OPEN" with stock VIP 30 and CAT1 30
    And admission-ctl runs with MAX_CONCURRENT_SLOTS 10
    And a test client is subscribed to sse-broadcast and sse-status

  Scenario: A crowd is admitted in order, never more than MAX at once
    When 100 customers join
    Then within 2 s exactly 10 customers are GRANTED, holding tickets 1 to 10
    And the broadcast shows nowServing 10
    When 4 of them complete payment
    Then within 1 s 4 more are GRANTED, holding tickets 11 to 14

  Scenario: The sale sells out, then closes
    Given admission-ctl runs with FULL_BOOKED_CLOSE_SECONDS 3 for this test
    When 100 customers join and every granted customer buys 3 tickets
    Then the state becomes "FULL_BOOKED" once 60 tickets are confirmed
    And sse-status announced "FULL_BOOKED" exactly once
    And within 4 s the state is "CLOSED" and sse-status announced "CLOSED" once
    # OPEN: 930 s needs to be configurable for tests; proposal: config FULL_BOOKED_CLOSE_SECONDS.

  Scenario: Killing the controller mid-sale loses nobody
    When 100 customers join
    And admission-ctl is killed with SIGKILL after its 2nd tick
    And admission-ctl is started again
    Then within 2 s every ticket <= nowServing has exactly one GRANTED-or-later row
    And no customer has two rows

  Scenario: A Postgres outage freezes the queue and then it resumes
    When 100 customers join and 10 are GRANTED
    And Postgres is paused for 3 s while 5 customers complete payment
    Then nowServing does not move while Postgres is paused
    And within 2 s after Postgres resumes, 5 more customers are GRANTED

  Scenario: A Redis primary failover does not lose pending grants
    When 100 customers join
    And the Redis primary that owns "E1"'s slot fails over during a tick
    Then within 5 s every ticket <= nowServing has exactly one row
    # Needs a 3-master / 3-replica compose cluster; may be tagged @slow.

  Scenario: Graceful shutdown finishes the tick in progress
    When 100 customers join
    And admission-ctl receives SIGTERM during FINISH
    Then it exits after the current tick with exit code 0
    And no token is left pending after it stops
