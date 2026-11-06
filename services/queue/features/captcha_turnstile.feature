@step-4 @queue-service @captcha @NFR-13
Feature: Turnstile adapter behind the Captcha port
  Invisible Cloudflare Turnstile, server-side siteverify only. An explicit "invalid"
  rejects. Any provider trouble fails open (fairness, not correctness, is at stake).
  Tests run against a local fake siteverify server, never the real Cloudflare.

  Sources: 01-join-queue.md §6 · api-contracts §3.1 · ADR-006 §8

  Background:
    Given a fake siteverify server
    And TURNSTILE_SECRET_KEY is "test-secret"
    And the siteverify timeout is 1 s

  @integration
  Scenario: A valid token passes
    Given siteverify answers {"success": true}
    When the adapter verifies token "cap-ok" for client IP "203.0.113.7"
    Then the result is "valid"
    And siteverify received secret "test-secret", response "cap-ok" and remoteip "203.0.113.7"

  @integration
  Scenario Outline: An explicit rejection is invalid
    Given siteverify answers {"success": false, "error-codes": ["<code>"]}
    When the adapter verifies token "cap-bad"
    Then the result is "invalid"

    Examples:
      | code                 |
      | invalid-input-response |
      | timeout-or-duplicate |

  @integration
  Scenario: Our own misconfiguration is not the customer's fault
    # OPEN: "invalid-input-secret" means OUR secret is wrong. Rejecting would block every
    # customer. Proposal: treat as provider trouble (fail open) and alarm loudly.
    Given siteverify answers {"success": false, "error-codes": ["invalid-input-secret"]}
    When the adapter verifies token "cap-ok"
    Then the result is "unavailable"
    And the metric "CaptchaMisconfigured" is incremented

  @integration
  Scenario Outline: Provider trouble is "unavailable"
    Given siteverify <trouble>
    When the adapter verifies token "cap-ok"
    Then the result is "unavailable" within 1.2 s

    Examples:
      | trouble                      |
      | sleeps for 3 s               |
      | answers HTTP 500             |
      | answers HTTP 429             |
      | closes the connection        |
      | answers "<html>oops</html>"  |

  @unit
  Scenario: The mock adapter used by tests and k6 never calls out
    Given the CAPTCHA adapter is the mock
    When it verifies 1000 tokens
    Then no network call was made

  @unit @security
  Scenario: The secret comes from configuration, never from the repo
    Then no file in the repository contains a Turnstile secret
    And the service fails to start in a non-local environment without TURNSTILE_SECRET_KEY
