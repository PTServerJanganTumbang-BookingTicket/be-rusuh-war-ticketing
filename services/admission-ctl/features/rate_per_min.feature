@draft @step-7 @admission-ctl @FR-09
Feature: rate_per_min tells waiting clients how fast now_serving moves
  rate_per_min = ticket numbers that now_serving passes per minute, over the last
  60 s of OPEN time. Until 60 s of OPEN samples exist, a seed is used:
  MAX_CONCURRENT_SLOTS / ETA_SEED_SLOT_MINUTES.

  Sources: 05 §3.5 · README §8 · ADR-006 §6.1

  Background:
    Given MAX_CONCURRENT_SLOTS is 1000 and ETA_SEED_SLOT_MINUTES is 5
    And event "E1" opened at epoch 1800000000

  @unit
  Scenario: The seed is used while the window covers less than 60 s
    When ticks run for 59.5 s and now_serving moves from 0 to 4000
    Then rate_per_min is 200

  @unit
  Scenario: After 60 s the measured rate replaces the seed
    Given now_serving moved steadily by 25 per tick for 60 s
    Then rate_per_min is 3000

  @unit
  Scenario: The T0 burst stays out of the measured rate
    Given the first tick admitted 1000 at once
    And afterwards now_serving moved by 5 per tick
    When 120 s have passed
    Then rate_per_min is 600

  @unit
  Scenario: SOLD_OUT time is not sampled and the rate is kept
    Given rate_per_min is 600 after 2 minutes OPEN
    When "E1" is SOLD_OUT for 3 minutes
    Then rate_per_min stays 600 for the whole SOLD_OUT time
    And no sample was added while SOLD_OUT
    When "E1" reopens and now_serving moves by 5 per tick
    Then rate_per_min is computed from OPEN samples only

  @unit
  Scenario: A restarted controller starts from the seed
    Given rate_per_min was 600 before the controller restarted
    When the new controller has run for 10 s
    Then rate_per_min is 200

  @unit
  Scenario: The value is rounded and stored for /me and the stream
    Given the measured rate is 612.6 per minute
    When the tick runs
    Then queue:{E1} field rate_per_min is "613"
    And the next broadcast carries ratePerMin 613

  @unit
  Scenario: A stalled queue shows a rate of 0, not the seed
    Given the window covers 60 s and now_serving did not move
    Then rate_per_min is 0
