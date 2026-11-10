@step-4 @pkg-ratelimit @integration @redis-cluster
Feature: Token-bucket rate limiter in Redis (pkg/ratelimit)
  One atomic Lua call per check: refill by elapsed time, then take one token.
  Capacity N and refill rate r tokens per second. Uses Redis TIME, not the caller's
  clock, so Lambdas with skewed clocks agree.

  Sources: impl-plan §7.2 step 2 · 01-join-queue.md §4

  Background:
    Given a clean Redis cluster

  Scenario: A full bucket allows N calls, then refuses
    Given a limiter with capacity 20 and refill 20 per 300 s on key "rl:join:203.0.113.7"
    When 20 checks run at once
    Then all 20 are allowed
    When a 21st check runs
    Then it is refused with retry_after 15 s

  Scenario: Tokens refill with time and never exceed capacity
    Given a limiter with capacity 3 and refill 3 per 60 s on key "rl:joinacct:acc-1"
    And the bucket was emptied
    When 20 seconds pass
    Then exactly 1 check is allowed
    When 1 hour passes
    Then exactly 3 checks are allowed

  Scenario: Parallel checks never over-allow
    Given a limiter with capacity 20 and refill 20 per 300 s on key "rl:join:203.0.113.7"
    When 500 checks run in parallel
    Then exactly 20 are allowed

  Scenario: Idle buckets expire
    Given a limiter with capacity 20 and refill 20 per 300 s on key "rl:join:203.0.113.7"
    When one check runs
    Then the key expires within 300 s

  Scenario: Separate keys are separate buckets
    Given IP "203.0.113.7" has an empty bucket
    Then IP "203.0.113.8" is still allowed
