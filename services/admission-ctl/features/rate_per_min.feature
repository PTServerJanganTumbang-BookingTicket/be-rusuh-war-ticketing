@draft @step-7 @admission-ctl @FR-09
Feature: Seeing how fast the queue moves
  As a waiting customer, my app shows "about N minutes" from ratePerMin: ceil(position / ratePerMin).
  The number should be honest: a sensible guess at the start, the real speed after a minute, 0 when the queue is stuck.
  rate_per_min = ticket numbers now_serving passed per minute over the last 60 s of OPEN time.
  Until 60 s of OPEN samples exist, the seed MAX_CONCURRENT_SLOTS / ETA_SEED_SLOT_MINUTES is used.

  Sources: 05-admission-controller.md §3.5 · README §8 · ADR-006 §6.1

  Background:
    Given MAX_CONCURRENT_SLOTS is 1000 and ETA_SEED_SLOT_MINUTES is 5
    And event "E1" opened at epoch 1800000000

  # ------------------------------------------------------------ the first minute

  @unit
  Scenario: In the first minute I see a sensible guess
    When ticks run for 59.5 s and now_serving moves from 0 to 4000
    Then rate_per_min is 200

  @unit
  Scenario: The opening rush does not make my wait look shorter than it is
    Given the first tick admitted 1000 at once
    And afterwards now_serving moved by 5 per tick
    When 120 s have passed
    Then rate_per_min is 600

  # ------------------------------------------------------------ after that

  @unit
  Scenario: After a minute I see the real speed
    Given now_serving moved steadily by 25 per tick for 60 s
    Then rate_per_min is 3000

  @unit
  Scenario: A stuck queue shows a rate of 0, not a hopeful guess
    Given the window covers 60 s and now_serving did not move
    Then rate_per_min is 0

  @unit
  Scenario: While fully booked my estimate does not jump around
    Given rate_per_min is 600 after 2 minutes OPEN
    When "E1" is FULL_BOOKED for 3 minutes
    Then rate_per_min stays 600 for the whole FULL_BOOKED time
    And no sample was added while FULL_BOOKED
    When "E1" reopens and now_serving moves by 5 per tick
    Then rate_per_min is computed from OPEN samples only

  @unit
  Scenario: After a controller restart I see the guess again until it has measured a minute
    Given rate_per_min was 600 before the controller restarted
    When the new controller has run for 10 s
    Then rate_per_min is 200

  # ------------------------------------------------------------ where I read it

  @unit
  Scenario: The same whole number reaches /queue/me and the stream
    Given the measured rate is 612.6 per minute
    When the tick runs
    Then queue:{E1} field rate_per_min is "613"
    And the next broadcast carries ratePerMin 613
