@draft @step-1 @pkg-redisscript @integration @redis-cluster
Feature: advance_now_serving.lua moves the queue forward atomically
  The script decides how many ticket numbers to admit in one tick and raises
  now_serving. It never removes tokens from by_ticket: a member whose score is
  <= now_serving is a *pending grant* that admission-ctl finishes and ZREMs later.

  Script contract (ADR-006 §3.4):
    KEYS  queue:{ev} · queue:{ev}:state · queue:{ev}:by_ticket · queue:{ev}:issued
          · queue:{ev}:full_booked_at · queue:{ev}:resume · stock:{ev}:<cat>...
    ARGV  MAX · open (incl. pending) · browsing (incl. pending) · now_serving seen by ctl · now (epoch s)
    RET   {from, to, result, tokens}

  In this file "open" and "browsing" are the values PASSED to the script.
  Adding pending to them is the controller's job (admission_tick.feature).
  Tests run against a real Redis cluster (testcontainers), never a mock.

  Sources: ADR-006 §2, §3 · 05-admission-controller.md §3.1–§3.3, §7

  Background:
    Given a clean Redis cluster
    And event "E1" has stock categories "VIP" and "CAT1"
    And the clock reads epoch 1800000000

  # ---------------------------------------------------------------- formula

  @rule-5 @FR-08
  Rule: deficit = min(MAX − open, ceil(stock / 3) − browsing), and now_serving never passes issued

    Scenario Outline: Admission amount follows the formula
      Given event "E1" is "OPEN" with now_serving 0 and issued 100000
      And customers hold ticket numbers 1 to 2000 in by_ticket
      And total stock is <stock> split as VIP <vip> and CAT1 <cat1>
      When the script runs with MAX <max>, open <open>, browsing <browsing>, seen now_serving 0
      Then the result is "<result>"
      And now_serving is <to>
      And <admitted> tokens are returned

      Examples: ÷ 3 boundary (MAX 100, nobody inside)
        | stock | vip | cat1 | max | open | browsing | result   | to  | admitted |
        | 1000  | 500 | 500  | 100 | 0    | 0        | ADMITTED | 100 | 100      |
        | 300   | 150 | 150  | 100 | 0    | 0        | ADMITTED | 100 | 100      |
        | 298   | 149 | 149  | 100 | 0    | 0        | ADMITTED | 100 | 100      |
        | 297   | 148 | 149  | 100 | 0    | 0        | ADMITTED | 99  | 99       |
        | 4     | 2   | 2    | 100 | 0    | 0        | ADMITTED | 2   | 2        |
        | 3     | 3   | 0    | 100 | 0    | 0        | ADMITTED | 1   | 1        |
        | 1     | 0   | 1    | 100 | 0    | 0        | ADMITTED | 1   | 1        |

      Examples: plan scenarios A, B, C (05 §7)
        | stock | vip  | cat1 | max | open | browsing | result   | to | admitted |
        | 9820  | 4910 | 4910 | 100 | 95   | 40       | ADMITTED | 5  | 5        |
        | 240   | 120  | 120  | 100 | 80   | 10       | ADMITTED | 20 | 20       |
        | 9     | 9    | 0    | 100 | 50   | 3        | NOOP     | 0  | 0        |
        | 9     | 5    | 4    | 100 | 49   | 2        | ADMITTED | 1  | 1        |

      Examples: nothing to admit (deficit <= 0)
        | stock | vip  | cat1 | max | open | browsing | result | to | admitted |
        | 9820  | 4910 | 4910 | 100 | 100  | 0        | NOOP   | 0  | 0        |
        | 9820  | 4910 | 4910 | 100 | 120  | 0        | NOOP   | 0  | 0        |
        | 30    | 15   | 15   | 100 | 0    | 10       | NOOP   | 0  | 0        |
        | 30    | 15   | 15   | 100 | 0    | 50       | NOOP   | 0  | 0        |
        | 9820  | 4910 | 4910 | 0   | 0    | 0        | NOOP   | 0  | 0        |

    Scenario: The ADR-005 tail stall is fixed (scenario B in words)
      # ADR-005 gave min(100, 80) − 80 = 0 and froze the queue while 240 tickets were free.
      Given event "E1" is "OPEN" with now_serving 500 and issued 5000
      And customers hold ticket numbers 501 to 5000 in by_ticket
      And total stock is 240
      When the script runs with MAX 100, open 80, browsing 10, seen now_serving 500
      Then the result is "ADMITTED"
      And now_serving is 520

    Scenario: now_serving stops at the last issued number
      Given event "E1" is "OPEN" with now_serving 0 and issued 10
      And customers hold ticket numbers 1 to 10 in by_ticket
      And total stock is 9000
      When the script runs with MAX 1000, open 0, browsing 0, seen now_serving 0
      Then the result is "ADMITTED"
      And now_serving is 10
      And the returned tokens are tickets 1 to 10 in ascending order

    Scenario: A customer who joins after the cap is picked up by the next run
      Given event "E1" is "OPEN" with now_serving 10 and issued 10
      And total stock is 9000
      When a new customer joins and receives ticket number 11
      And the script runs with MAX 1000, open 10, browsing 10, seen now_serving 10
      Then the result is "ADMITTED"
      And now_serving is 11
      And the returned tokens are ticket 11

    Scenario: Everyone issued is already admitted
      Given event "E1" is "OPEN" with now_serving 10 and issued 10
      And total stock is 9000
      When the script runs with MAX 1000, open 10, browsing 10, seen now_serving 10
      Then the result is "EMPTY"
      And now_serving is 10
      And no key of event "E1" changed

    Scenario: issued has never been set
      Given event "E1" is "OPEN" with now_serving 0 and no issued key
      And total stock is 9000
      When the script runs with MAX 1000, open 0, browsing 0, seen now_serving 0
      Then the result is "EMPTY"
      And now_serving is 0

    Scenario: now_serving field missing is treated as 0
      Given event "E1" is "OPEN" with no now_serving field and issued 3
      And customers hold ticket numbers 1 to 3 in by_ticket
      And total stock is 9000
      When the script runs with MAX 1000, open 0, browsing 0, seen now_serving 0
      Then the result is "ADMITTED"
      And now_serving is 3

  # ---------------------------------------------------------------- pending grants

  @rule-4 @adr-006-2
  Rule: The script never removes tokens; admitted tokens stay in by_ticket as pending grants

    Scenario: Returned tokens are still in by_ticket after the run
      Given event "E1" is "OPEN" with now_serving 0 and issued 5
      And customers hold ticket numbers 1 to 5 in by_ticket
      And total stock is 9000
      When the script runs with MAX 1000, open 0, browsing 0, seen now_serving 0
      Then the returned tokens are tickets 1 to 5 in ascending order
      And by_ticket still holds 5 members
      And ZCOUNT by_ticket -inf now_serving is 5

    Scenario: A second run does not return the same pending tokens again
      Given event "E1" is "OPEN" with now_serving 5 and issued 8
      And customers hold ticket numbers 1 to 8 in by_ticket
      And tickets 1 to 5 are pending grants
      And total stock is 9000
      When the script runs with MAX 1000, open 5, browsing 5, seen now_serving 5
      Then the returned tokens are tickets 6 to 8 in ascending order
      And none of tickets 1 to 5 are returned

    Scenario: Gaps from customers who left are skipped, and now_serving still moves to "to"
      Given event "E1" is "OPEN" with now_serving 0 and issued 5
      And customers hold ticket numbers 1, 2, 4 and 5 in by_ticket
      And total stock is 9000
      When the script runs with MAX 1000, open 0, browsing 0, seen now_serving 0
      Then the result is "ADMITTED"
      And from is 1 and to is 5
      And the returned tokens are tickets 1, 2, 4 and 5
      And now_serving is 5

    Scenario: The returned range is reported as from..to
      Given event "E1" is "OPEN" with now_serving 40 and issued 100
      And customers hold ticket numbers 41 to 100 in by_ticket
      And total stock is 9000
      When the script runs with MAX 1000, open 990, browsing 0, seen now_serving 40
      Then from is 41 and to is 50

  # ---------------------------------------------------------------- CAS fence

  @rule-5 @adr-006-3
  Rule: If now_serving changed since the controller read it, the script does nothing (CAS fence)

    Scenario: Stale now_serving returns STALE and writes nothing
      Given event "E1" is "OPEN" with now_serving 7 and issued 100
      And customers hold ticket numbers 8 to 100 in by_ticket
      And total stock is 9000
      When the script runs with MAX 1000, open 0, browsing 0, seen now_serving 5
      Then the result is "STALE"
      And from is 7 and to is 7
      And no tokens are returned
      And no key of event "E1" changed

    Scenario: The fence wins over a full-booked transition
      Given event "E1" is "OPEN" with now_serving 7 and issued 100
      And total stock is 0
      When the script runs with MAX 1000, open 0, browsing 0, seen now_serving 5
      Then the result is "STALE"
      And the state is still "OPEN"
      And full_booked_at is not set

    Scenario: Two controllers race; only the first advances
      Given event "E1" is "OPEN" with now_serving 0 and issued 100
      And customers hold ticket numbers 1 to 100 in by_ticket
      And total stock is 9000
      And controllers A and B both read now_serving 0
      When controller A runs the script with MAX 10, open 0, browsing 0
      And controller B runs the script with MAX 10, open 0, browsing 0
      Then A gets "ADMITTED" with tickets 1 to 10
      And B gets "STALE"
      And now_serving is 10

  # ---------------------------------------------------------------- state gate

  @FR-05
  Rule: Only OPEN and FULL_BOOKED events can move

    Scenario Outline: Non-moving states return themselves and write nothing
      Given event "E1" has state <state> with now_serving 0 and issued 100
      And total stock is 9000
      When the script runs with MAX 1000, open 0, browsing 0, seen now_serving 0
      Then the result is "<result>"
      And no key of event "E1" changed

      Examples:
        | state         | result    |
        | "SCHEDULED"   | SCHEDULED |
        | "CLOSED"      | CLOSED    |
        | missing       | MISSING   |

  # ---------------------------------------------------------------- stock & fully booked

  @FR-18
  Rule: Stock is summed across categories inside the script; zero stock means FULL_BOOKED, which is temporary

    Scenario: A missing stock key is not zero
      Given event "E1" is "OPEN" with now_serving 0 and issued 100
      And stock VIP is 50 and stock CAT1 key is missing
      When the script runs with MAX 1000, open 0, browsing 0, seen now_serving 0
      Then the result is "STOCK_UNINITIALIZED"
      And no key of event "E1" changed

    Scenario: A missing stock key is reported even when the other keys are zero
      Given event "E1" is "OPEN" with now_serving 0 and issued 100
      And stock VIP is 0 and stock CAT1 key is missing
      When the script runs with MAX 1000, open 0, browsing 0, seen now_serving 0
      Then the result is "STOCK_UNINITIALIZED"
      And the state is still "OPEN"

    Scenario: Stock reaching zero flips OPEN to FULL_BOOKED and stamps the time
      Given event "E1" is "OPEN" with now_serving 300 and issued 5000
      And total stock is 0
      When the script runs with MAX 1000, open 50, browsing 0, seen now_serving 300
      Then the result is "FULL_BOOKED"
      And the state is "FULL_BOOKED"
      And full_booked_at is 1800000000
      And now_serving is 300
      And no tokens are returned

    Scenario: Staying fully booked keeps the first full_booked_at (the CLOSED timer is not reset)
      Given event "E1" is "FULL_BOOKED" since 1799999000 with now_serving 300 and issued 5000
      And total stock is 0
      When the script runs with MAX 1000, open 50, browsing 0, seen now_serving 300
      Then the result is "FULL_BOOKED"
      And full_booked_at is 1799999000

    Scenario: Stock coming back reopens the sale and admits in the same run
      Given event "E1" is "FULL_BOOKED" since 1799999000 with now_serving 300 and issued 5000
      And customers hold ticket numbers 301 to 5000 in by_ticket
      And total stock is 6 split as VIP 3 and CAT1 3
      When the script runs with MAX 1000, open 50, browsing 0, seen now_serving 300
      Then the state is "OPEN"
      And full_booked_at is not set
      And the result is "ADMITTED"
      And now_serving is 302

    Scenario: Reopening still happens when nobody can be admitted yet
      Given event "E1" is "FULL_BOOKED" since 1799999000 with now_serving 300 and issued 5000
      And total stock is 3
      When the script runs with MAX 1000, open 50, browsing 1, seen now_serving 300
      Then the result is "NOOP"
      And the state is "OPEN"
      And full_booked_at is not set
      # The controller detects the OPEN change by reading state, not from the result code.

    @open-question
    Scenario: Reopening does not write the resume flag
      # OPEN: ADR-006 Lua writes queue:{ev}:resume = 1, but pause/resume was removed
      # (ADR-006 §4.3) and nothing reads it. Proposal: remove the write. If you keep it,
      # flip this scenario to "Then queue:{E1}:resume is 1".
      Given event "E1" is "FULL_BOOKED" since 1799999000 with now_serving 300 and issued 5000
      And total stock is 6
      When the script runs with MAX 1000, open 50, browsing 0, seen now_serving 300
      Then the key "queue:{E1}:resume" does not exist

  # ---------------------------------------------------------------- input safety

  Rule: Bad input fails before any write (Redis does not roll back a half-run script)

    @open-question
    Scenario Outline: Invalid arguments are rejected up front
      # OPEN: ADR-006 Lua validates nothing. A non-numeric MAX after a FULL_BOOKED → OPEN
      # write would error mid-script and leave the state half-changed.
      # Proposal: validate all ARGV first and return an error.
      Given event "E1" is "FULL_BOOKED" since 1799999000 with now_serving 300 and issued 5000
      And total stock is 6
      When the script runs with raw arguments <args>
      Then the script returns an error containing "invalid argument"
      And no key of event "E1" changed

      Examples:
        | args                            |
        | "abc", "0", "0", "300", "1800000000" |
        | "100", "x", "0", "300", "1800000000" |
        | "100", "0", "", "300", "1800000000"  |
        | "100", "0", "0", "300", "now"        |
        | "-1", "0", "0", "300", "1800000000"  |

    @open-question
    Scenario: No stock category keys is a configuration error, not a sell-out
      # OPEN: with zero stock keys the ADR-006 Lua sums 0 and marks the event FULL_BOOKED.
      # Proposal: return "STOCK_UNINITIALIZED" instead.
      Given event "E1" is "OPEN" with now_serving 0 and issued 100
      When the script runs with no stock keys
      Then the result is "STOCK_UNINITIALIZED"
      And the state is still "OPEN"

  # ---------------------------------------------------------------- loading & cluster

  Rule: The script is loaded once and called by SHA on a cluster

    Scenario: All keys in one call share a slot, so the cluster accepts the call
      Given event "E1" is "OPEN" with now_serving 0 and issued 1
      And customers hold ticket numbers 1 to 1 in by_ticket
      And total stock is 10
      When the script runs with MAX 10, open 0, browsing 0, seen now_serving 0
      Then no CROSSSLOT error is raised

    Scenario: A script flushed from Redis is reloaded and retried once
      Given the script is loaded and its SHA is known
      And event "E1" is "OPEN" with now_serving 0 and issued 1
      And customers hold ticket numbers 1 to 1 in by_ticket
      And total stock is 10
      When Redis runs SCRIPT FLUSH
      And the script runs with MAX 10, open 0, browsing 0, seen now_serving 0
      Then the loader gets NOSCRIPT, loads the script again and the call succeeds
      And now_serving is 1

    Scenario: The SHA is stable across loads
      When the script is loaded twice
      Then both loads return the same SHA1
