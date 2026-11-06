@step-1 @pkg-rediskeys
Feature: Central Redis key names (pkg/rediskeys)
  Every Redis key is built by pkg/rediskeys, never hand-written.
  Only "{ev}" is a real Redis hash tag. Keys touched by one Lua script share
  that tag, so Redis cluster puts them in the same slot.

  Sources: impl-plan §3.1 · queue README §4 · ADR-006 §5

  @unit
  Scenario Outline: Queue-domain key names are exact
    When I build the "<builder>" key for event "E1" with "<arg>"
    Then the key is "<key>"

    Examples:
      | builder    | arg    | key                     |
      | QueueHash  |        | queue:{E1}              |
      | QueueState |        | queue:{E1}:state        |
      | QueueIssued|        | queue:{E1}:issued       |
      | ByTicket   |        | queue:{E1}:by_ticket    |
      | SoldOutAt  |        | queue:{E1}:sold_out_at  |
      | Expiries   |        | expiries:{E1}           |
      | Stock      | VIP    | stock:{E1}:VIP          |
      | Acct       | acc-42 | acct:{E1}:acc-42        |

  @unit
  Scenario Outline: Per-customer keys carry no event hash tag
    When I build the "<builder>" key with "<arg>"
    Then the key is "<key>"

    Examples:
      | builder | arg                                  | key                                        |
      | Token          | qt_01JC3Z8K4M6N7P8Q9R0S1T2V3W | token:qt_01JC3Z8K4M6N7P8Q9R0S1T2V3W |
      | Grant          | gt_abc123                     | grant:gt_abc123                     |
      | JoinRateLimit  | 203.0.113.7                   | rl:join:203.0.113.7                 |
      | JoinAcctLimit  | acc-1                         | rl:joinacct:acc-1                   |

  @unit
  Scenario: Pub/sub channel names are constants
    Then the broadcast channel is "sse-broadcast"
    And the status channel is "sse-status"

  @unit @adr-006
  Scenario: Keys removed by ADR-006 do not exist
    Then pkg/rediskeys exports no builder named "SlotsOpen"
    And pkg/rediskeys exports no builder named "Regrant"
    And pkg/rediskeys exports no builder named "Pause"
    And pkg/rediskeys exports no builder named "PauseGraceApplied"
    And pkg/rediskeys exports no channel named "sse-grant"
    # OPEN: impl-plan §3.1 still lists ResumeFlag (queue:{ev}:resume). The ADR-006 Lua
    # still writes it, but no reader is documented since pause was removed.
    # Proposal: drop it from the Lua and from pkg/rediskeys. See advance_now_serving.feature.

  @unit
  Scenario Outline: An event id that would break the hash tag is rejected
    When I build the "QueueHash" key for event "<event_id>"
    Then building fails with "invalid event id"

    Examples:
      | event_id |
      |          |
      | E{1      |
      | E}1      |
      | E 1      |

  @integration @redis-cluster
  Scenario: Every key one script touches lands in the same cluster slot
    Given a Redis cluster with 3 masters
    When I compute CLUSTER KEYSLOT for every event key of "E1", including stock for "VIP", "CAT1", "CAT2"
    Then all of them return the same slot
    And the keys of event "E2" may return a different slot
