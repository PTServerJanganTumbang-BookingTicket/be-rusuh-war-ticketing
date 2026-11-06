@draft @step-3 @admission-ctl @property @integration @postgres @redis-cluster
Feature: Queue invariants hold under random interleavings and crashes
  A seeded random simulator drives the real Lua script and the real Postgres
  store with random customer actions, controller ticks, injected crashes and a
  second controller. After EVERY step it checks the invariants below.
  On failure it prints the seed and the shortest step list that reproduces it.

  Sources: 05 §3.3, §7 · README §0 rules 1–5 · ADR-006 §2, §3

  Background:
    Given a simulator with MAX_CONCURRENT_SLOTS between 1 and 50
    And total stock between 0 and 300 over 1 to 3 categories
    And between 0 and 500 customers joining over time
    And these actions, chosen at random each step:
      | action                | effect                                             |
      | join                  | issue the next ticket number                       |
      | tick                  | one full controller tick                           |
      | crash                 | stop the controller at a random point, restart it  |
      | second controller     | run one tick from a second controller concurrently |
      | claim                 | GRANTED → SELECTING                                |
      | hold                  | SELECTING → HELD, takes 1 to 3 tickets from stock  |
      | pay                   | HELD → AWAITING_PAYMENT → CONFIRMED                |
      | expire                | HELD or AWAITING_PAYMENT → EXPIRED, returns stock  |
      | lapse                 | advance clock past a GRANTED deadline, sweep       |
      | db outage             | fail every Postgres call for 1 to 5 steps          |

  Scenario: Capacity is never exceeded
    When the simulator runs 2000 random steps for each of 200 seeds
    Then after every step: active bookings + pending grants <= MAX_CONCURRENT_SLOTS

  Scenario: Every admitted browsing customer can still buy a full order
    When the simulator runs 2000 random steps for each of 200 seeds
    Then after every tick: 3 × (browsing + pending) <= total stock + 2

  Scenario: now_serving only moves forward and never passes issued
    When the simulator runs 2000 random steps for each of 200 seeds
    Then after every step: now_serving >= its previous value
    And after every step: now_serving <= issued

  Scenario: Nobody is lost
    When the simulator runs 2000 random steps for each of 200 seeds
    And then runs healthy ticks until nothing is pending
    Then every ticket number <= now_serving whose customer did not leave has exactly one bookings row
    And no member of by_ticket has a score <= now_serving

  Scenario: First come, first served
    When the simulator runs 2000 random steps for each of 200 seeds
    Then rows are granted in ticket_no order, skipping only customers who left

  Scenario: One row per queue token
    When the simulator runs 2000 random steps for each of 200 seeds
    Then no (event_id, queue_token) pair has more than one bookings row

  Scenario: Final statuses stay final
    When the simulator runs 2000 random steps for each of 200 seeds
    Then no booking ever leaves CONFIRMED, EXPIRED, LAPSED or ABANDONED
    And no booking ever moves back into GRANTED or SELECTING

  Scenario: A failing seed is reproducible
    Given a seed that fails an invariant
    When the simulator replays that seed
    Then it fails at the same step with the same invariant
