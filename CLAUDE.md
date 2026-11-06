# CLAUDE.md — be-rusuh-war-ticketing

Go backend monorepo (`go.work`, hexagonal `services/*`, shared `pkg/*`). Loaded automatically by
Claude Code in this repo.

## Development flow (mandatory)

Every change follows [`docs/DEVELOPMENT-FLOW.md`](docs/DEVELOPMENT-FLOW.md):
**Gherkin spec (`@draft`) → human review → red → green → `make verify` → independent review → commit.**

- Never implement a feature file that still carries `@draft`.
- Feature files live next to the code (`<module>/features/*.feature`) and run with godog, strict mode.
- Every scenario has exactly one level tag (`@unit` or `@integration`; `make spec` enforces it). Add a trace
  tag (`@rule-N`, `@FR-xx`, …) on the feature or scenario wherever a requirement or ADR rule applies (not enforced).
- Encode unresolved `OPEN:` items as `@open-question` scenarios holding the proposed behaviour.
- Never claim "done" or "passing" without the command output that shows it.

## Commands

| Command | Use |
|---|---|
| `make spec` | parse + tag-lint every `.feature` |
| `make step STEP=N` | run one step's approved features (red → green loop) |
| `make verify-fast` | fmt, vet, lint, unit tests, `@unit` features (Stop hook runs this) |
| `make verify` | everything, incl. `@integration` on real Redis cluster + Postgres (docker compose) |
| `make coverage` | coverage profiles for Sonar |

## Rules

- Behaviour comes from the `documentations` repo (requirements → ADR-005 as amended by ADR-006 →
  Apidog JSON / api-contracts). `docs/` here plans the build; when they disagree, the source wins.
- All Redis keys come from `pkg/rediskeys`. Never hand-write a key string. Never copy ADR-005's Lua.
- Integration tests use real Redis/Postgres, never mocks of them.
- Commits: one branch per build step, local commits allowed, **never push or open a PR without asking**.
- Backend docs and specs are in English.
