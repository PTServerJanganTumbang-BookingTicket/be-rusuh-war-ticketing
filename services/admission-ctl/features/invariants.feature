@draft @step-3 @admission-ctl @property @integration @postgres @redis-cluster
Feature: The queue keeps its promises to every customer, whatever happens
  As any customer, these promises hold no matter how joins, payments, expiries, crashes and outages interleave.
  A seeded random simulator drives the real Lua script and the real Postgres store with random customer actions,
  controller ticks, injected crashes and a second controller, and checks every promise after EVERY step.
  On failure it prints the seed and the shortest list of steps that reproduces it.

  Sources: 05-admission-controller.md §3.3, §7 · README §0 rules 1–5 · ADR-006 §2, §3

  Background:
    Given a simulator with MAX_CONCURRENT_SLOTS between 1 and 50
    And total stock between 0 and 300 over 1 to 3 categories
    And between 0 and 500 customers joining over time
    And these actions, chosen at random each step:
      | action            | effect                                             |
      | join              | issue the next ticket number                       |
      | tick              | one full controller tick                           |
      | crash             | stop the controller at a random point, restart it  |
      | second controller | run one tick from a second controller concurrently |
      | claim             | GRANTED → SELECTING                                |
      | hold              | SELECTING → HELD, takes 1 to 3 tickets from stock  |
      | pay               | HELD → AWAITING_PAYMENT → CONFIRMED                |
      | expire            | HELD or AWAITING_PAYMENT → EXPIRED, returns stock  |
      | lapse             | advance clock past a GRANTED deadline, sweep       |
      | db outage         | fail every Postgres call for 1 to 5 steps          |

  # ------------------------------------------------------------ inside

  Scenario: It is never more crowded inside than allowed
    When the simulator runs 2000 random steps for each of 200 seeds
    Then after every step: active bookings + pending grants <= MAX_CONCURRENT_SLOTS

  Scenario: Everyone let in can still buy a full order of 3
    When the simulator runs 2000 random steps for each of 200 seeds
    Then after every tick: 3 × (browsing + pending) <= total stock + 2

  # ------------------------------------------------------------ the line

  Scenario: The number on my screen only goes up, and never past the last ticket given out
    When the simulator runs 2000 random steps for each of 200 seeds
    Then after every step: now_serving >= its previous value
    And after every step: now_serving <= issued

  @rule-4
  Scenario: Nobody is lost
    When the simulator runs 2000 random steps for each of 200 seeds
    And then runs healthy ticks until nothing is pending
    Then every ticket number <= now_serving whose customer did not leave has exactly one bookings row
    And no member of by_ticket has a score <= now_serving

  @NFR-05
  Scenario: First come, first served
    When the simulator runs 2000 random steps for each of 200 seeds
    Then rows are granted in ticket_no order, skipping only customers who left

  # ------------------------------------------------------------ my booking

  @rule-4
  Scenario: I never get two bookings for one place in line
    When the simulator runs 2000 random steps for each of 200 seeds
    Then no (event_id, queue_token) pair has more than one bookings row

  Scenario: A finished booking stays finished
    When the simulator runs 2000 random steps for each of 200 seeds
    Then no booking ever leaves CONFIRMED, EXPIRED, LAPSED or ABANDONED
    And no booking ever moves back into GRANTED or SELECTING

  # ------------------------------------------------------------ for the team

  Scenario: A broken promise can be replayed exactly
    Given a seed that fails an invariant
    When the simulator replays that seed
    Then it fails at the same step with the same invariant
