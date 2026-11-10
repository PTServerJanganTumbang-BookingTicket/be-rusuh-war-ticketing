@step-3 @admission-ctl @integration @e2e @postgres @redis-cluster
Feature: A real sale, end to end
  As a crowd of customers, we are let in in order, the sale fills up and closes, and nobody is lost when
  the controller, Postgres or Redis has a bad moment.
  Starts the real admission-ctl binary (cmd/worker) with a 500 ms tick against the docker compose Redis cluster
  and Postgres. A test helper joins customers with the same pkg/rediskeys calls as the join service (this module
  cannot import services/queue/internal); the real join → controller path is covered by the
  "after-step-3" scenarios in join_empty_queue.feature. Bookings move on through a test helper.

  Sources: 05-admission-controller.md · README §2, §5 · ADR-006

  Background:
    Given the docker compose stack is up with migrations applied
    And event "E1" is seeded "OPEN" with stock VIP 30 and CAT1 30
    And admission-ctl runs with MAX_CONCURRENT_SLOTS 10
    And a test client is subscribed to sse-broadcast and sse-status

  # ------------------------------------------------------------ a normal sale

  Scenario: We are let in in order, never more than 10 at once
    When 100 customers join
    Then within 2 s exactly 10 customers are GRANTED, holding tickets 1 to 10
    And the broadcast shows nowServing 10
    When 4 of them complete payment
    Then within 1 s 4 more are GRANTED, holding tickets 11 to 14

  Scenario: The tickets run out, then the sale closes
    Given admission-ctl runs with FULL_BOOKED_CLOSE_SECONDS 3 for this test
    When 100 customers join and every granted customer buys 3 tickets
    Then the state becomes "FULL_BOOKED" once 60 tickets are confirmed
    And sse-status announced "FULL_BOOKED" exactly once
    And within 4 s the state is "CLOSED" and sse-status announced "CLOSED" once
    # OPEN: 930 s needs to be configurable for tests; proposal: config FULL_BOOKED_CLOSE_SECONDS.

  # ------------------------------------------------------------ bad moments

  @fault
  Scenario: Killing the controller mid-sale loses nobody
    When 100 customers join
    And admission-ctl is killed with SIGKILL after its 2nd tick
    And admission-ctl is started again
    Then within 2 s every ticket <= nowServing has exactly one GRANTED-or-later row
    And no customer has two rows

  @fault
  Scenario: A Postgres outage freezes the queue, then it moves again
    When 100 customers join and 10 are GRANTED
    And Postgres is paused for 3 s while 5 customers complete payment
    Then nowServing does not move while Postgres is paused
    And within 2 s after Postgres resumes, 5 more customers are GRANTED

  @fault @slow
  Scenario: A Redis failover does not lose anyone who was being let in
    When 100 customers join
    And the Redis primary that owns "E1"'s slot fails over during a tick
    Then within 5 s every ticket <= nowServing has exactly one row
    # Needs a 3-master / 3-replica compose cluster.

  Scenario: A planned restart finishes letting people in before it stops
    When 100 customers join
    And admission-ctl receives SIGTERM during FINISH
    Then it exits after the current tick with exit code 0
    And no token is left pending after it stops
