# Deployment runbook

The ordered procedure for getting this system into an AWS account and keeping
it there.

This file is the **procedure**: what to run, in what order, and what is
one-time versus every-deploy. [`DEPLOY.md`](DEPLOY.md) is the **reference**:
why the architecture is the way it is, what it costs, how to connect a SQL
client, Dukcapil modes, and troubleshooting. When the two disagree about a
command, this file wins.

---

## What you need first

| | |
|---|---|
| AWS credentials | Administrator, on your own machine. Only for steps 1 and 2. |
| `aws` CLI | v2 |
| `gh` CLI | authenticated (`gh auth status`) |
| An email address | one you can open, to confirm SES |

You do **not** need Go, SAM or Docker locally. The pipeline builds everything.

---

## One-time, per AWS account

### 1. The deploy role

The only stack CI cannot create, because it creates the role CI assumes.

GitHub's **immutable subject claims** put numeric IDs in the OIDC subject, so
ask for the exact prefix rather than constructing it:

```bash
gh api repos/OWNER/REPO/actions/oidc/customization/sub
```

Take `sub_claim_prefix` from the output verbatim — note the delimiter is `@`,
not `-` as older documentation says.

```bash
aws cloudformation deploy \
  --template-file infra/bootstrap/github-oidc-role.yaml \
  --stack-name rusuh-cd-bootstrap \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    GitHubRepo=OWNER/REPO \
    SubClaimPrefix='repo:OWNER@123456/REPO@789012' \
    AllowedBranch=test-deployment \
    AllowedEnvironment=prod \
    CreateOidcProvider=true
```

- `AllowedBranch` must be the branch you will actually push to, **and** that
  branch must appear in `.github/workflows/cicd.yml` under `push.branches` and
  in `resolve-env`'s `if:`. Currently: `main`, `cicd`, `test-deployment`.
- `AllowedEnvironment` matters more than the branch. Deploy jobs declare
  `environment: prod`, which makes their subject claim
  `repo:...:environment:prod` and **replaces** the branch form. A trust policy
  listing only the branch can never match.
- `CreateOidcProvider=false` only if the account already has the GitHub OIDC
  provider: `aws iam list-open-id-connect-providers`.

### 2. An SES identity

Verification needs a click in a mailbox, so it cannot be automated. There is
no AWS-provided sender address — every `From` must be a verified identity, in
the same region you deploy to.

```bash
REGION=ap-southeast-1
FROM_EMAIL=you@example.com

aws sesv2 get-account --region "$REGION" \
  --query '{ProductionAccess:ProductionAccessEnabled,Max24h:SendQuota.Max24HourSend}'

aws sesv2 create-email-identity --email-identity "$FROM_EMAIL" --region "$REGION"

# click the link (check spam), then:
until [ "$(aws sesv2 get-email-identity --email-identity "$FROM_EMAIL" \
            --region "$REGION" --query VerifiedForSendingStatus --output text)" = "True" ]; do
  echo "waiting..."; sleep 10
done

aws sesv2 send-email --region "$REGION" \
  --from-email-address "$FROM_EMAIL" \
  --destination '{"ToAddresses":["success@simulator.amazonses.com"]}' \
  --content '{"Simple":{"Subject":{"Data":"check"},"Body":{"Text":{"Data":"it works"}}}}'
```

A `MessageId` back means sending works.

**`ProductionAccess: false` means recipients must also be verified.** Verify
each tester the same way, or request production access — which has a human in
the loop, so start it early if a demo involves anyone typing their own
address. See *The sandbox is the real constraint* in `DEPLOY.md`.

### 3. Repository configuration

One script sets every variable and secret, creates the `prod` environment, and
restricts that environment to the deploy branch:

```bash
DUKCAPIL_TOKEN='<apidog token>' bash infra/bootstrap/configure-github.sh
```

Edit the constants at the top first — region, from-address, app base URL,
Dukcapil mock URL, allowed DB CIDR. It is idempotent, so it is also how you
change one of those later.

Then the database password:

```bash
gh secret set DB_MASTER_PASSWORD --body '<16-64 letters and digits>'
```

**Alphanumeric only.** The template rejects anything else, because this
password is embedded verbatim in a connection URL that CloudFormation cannot
percent-encode — a `?` or `#` in it silently corrupts the DSN. Leave the
secret unset and the stack generates a safe 40-character one, which is the
right choice for anything real; read it back from
`rusuh-<env>/aurora-master`.

> The `prod` environment's deployment branch policy is a **security control**,
> not housekeeping. Because deploy jobs present an `environment:` subject
> claim with no branch in it, the AWS trust policy cannot restrict by branch —
> that policy is what takes over. `configure-github.sh` sets it.

### 4. First deploy

```bash
git push origin HEAD:test-deployment
```

Roughly 20–25 minutes. Order: quality gate → foundation (VPC, Aurora, Redis,
secrets, artifact bucket, 14 SSM parameters) → preflight → three services →
two services → smoke test. The ECS job (admission-ctl, sse-server) runs
alongside, after the foundation: see the ECS section of `DEPLOY.md`.

`workflow_dispatch` is **not** available from a feature branch — GitHub reads
it only from the default branch — so a push is the only route until `main`
becomes the deploy branch.

### 5. The schema

The pipeline does not migrate. This is deliberate (see the note in
`cicd.yml`), and the consequence is that **a green deploy says nothing about
the schema** — `/sys/ping` touches no tables, so services report healthy
against an empty database.

Connect with a SQL client and run every `migrations/*.up.sql` in order (0001 to
0011 today; `migrations/README.md` lists them). `0002`, `0003` and `0004` all
have foreign keys to `accounts`, and `events` needs 0011. Connection details:

```bash
aws secretsmanager get-secret-value --region ap-southeast-1 \
  --secret-id rusuh-prod/database-url --query SecretString --output text | python3 -m json.tool
```

See *Connecting with DBeaver* in `DEPLOY.md`.

---

## Every deploy after that

```bash
git push origin HEAD:test-deployment
```

Nothing else. The foundation only redeploys when `infra/foundation/**`
changes; services redeploy when their own directory or `pkg/` changes.

### Verifying

```bash
for svc in booking nik-verify user; do
  url=$(aws cloudformation describe-stacks --region ap-southeast-1 \
         --stack-name "rusuh-prod-$svc" \
         --query "Stacks[0].Outputs[?OutputKey=='ApiUrl'].OutputValue" --output text)
  echo "$svc -> $(curl -s -o /dev/null -w '%{http_code}' "$url/sys/ping")"
done
```

All three `200`. The smoke-test job does this automatically and fails the run
if any does not.

---

## The one thing to internalise

**CloudFormation does not re-resolve a `{{resolve:secretsmanager:...}}`
dynamic reference unless the resource is updated for some other reason.** The
resolved value plays no part in change detection.

Two consequences, both of which cost real debugging time to find:

1. **Rotating a secret changes nothing about a running Lambda.** It keeps the
   value baked in at its last deploy — no error, nothing in the console to
   suggest it is stale. The services pass `DeployRevision=<commit sha>` so
   every pipeline deploy is a genuine change and the references are re-read.
   Out of band, use `infra/refresh-lambda-secrets.sh`.

2. **A reference resolves when the change set is created**, which is *before*
   anything in that change set is updated. So a resource reading a secret the
   same deploy is changing gets the previous generation. That is why
   `AuroraMasterSecret`, `AppDatabaseSecret` and `AuroraCluster` all take
   `!Ref DatabasePassword` when a password is given, rather than resolving the
   secret: a parameter has no lag.

   `cfn-lint` W1011 advises the opposite and is suppressed on `AuroraCluster`
   with this reasoning recorded inline.

---

## SCP restriction and the workaround

This AWS account (699475956139) has a Service Control Policy (SCP) that blocks
`cloudformation:CreateChangeSet` on the SAM Serverless transform
(`arn:aws:cloudformation:*:aws:transform/Serverless-2016-10-31`). This means
`sam deploy` fails with:

```
User: arn:aws:sts::699475956139:assumed-role/... is not authorized to perform:
cloudformation:CreateChangeSet on resource: arn:aws:cloudformation:*:aws:transform/Serverless-2016-10-31
```

**The workaround:** All service deployments use raw CloudFormation templates
(`template-cfn.yaml`) instead of SAM templates. The pipeline:

1. Builds the Go binary (`GOOS=linux GOARCH=arm64`)
2. Creates `bootstrap.zip`
3. Uploads to S3 (the artifact bucket from foundation)
4. Deploys with `aws cloudformation deploy` — no SAM transform

**What this means for you:**

- The foundation (`infra/foundation/template.yaml`) is unaffected — it never
  used the SAM transform.
- Service templates now have two files:
  - `template.yaml` — the original SAM template (kept for reference)
  - `template-cfn.yaml` — raw CloudFormation (used by the pipeline)
- The `.github/actions/cfn-deploy/` action handles the build and deploy.
- Manual deployments follow the same pattern:

```bash
cd services/booking
GOWORK=off GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bootstrap ./cmd/lambda/
zip -j bootstrap.zip bootstrap

BUCKET=$(aws ssm get-parameter --name /rusuh/prod/sam/artifact-bucket --query Parameter.Value --output text)
aws s3 cp bootstrap.zip "s3://$BUCKET/booking/bootstrap.zip"

aws cloudformation deploy \
  --template-file template-cfn.yaml \
  --stack-name rusuh-prod-booking \
  --capabilities CAPABILITY_IAM \
  --parameter-overrides \
    Environment=prod \
    LambdaS3Bucket="$BUCKET" \
    LambdaS3Key=booking/bootstrap.zip
```

**If you need to modify a template:** Edit `template-cfn.yaml`, not
`template.yaml`. The SAM templates are now reference only.

---

## Turning a payment method off, mid-sale

A bank fails while a sale is running — BCA's virtual accounts start timing out,
Midtrans itself is fine. The payment breaker will not help: it knows whether
**Midtrans** is answering and nothing about one bank behind it. So customers
keep choosing BCA, keep getting a VA number they cannot pay into, and lose
their seats to the sweep ten minutes later.

One `INSERT` stops it being offered. No deploy.

```sql
INSERT INTO payment_method_switches (method_id, bank_code, available, reason, changed_by, expires_at)
VALUES ('bank_transfer', 'bca', false, 'BCA VA timeouts, confirmed with ops', 'your-name', now() + interval '2 hours');
```

- `bank_code NULL` turns the **whole method** off instead of one bank.
  `method_id` is the Midtrans `payment_type`: `bank_transfer` or `gopay`.
- `reason` and `changed_by` are **required**, and a blank one is refused. You
  are writing for whoever reads this row at 2am, possibly you.
- **Set `expires_at`.** The switch then reverts itself. The real failure mode is
  not forgetting to switch BCA off — it is switching it off during an incident
  and nobody remembering to switch it back, so a bank stays dark for a week and
  the first sign is a conversion graph. `NULL` means "until someone says
  otherwise", which is sometimes right and should be a deliberate choice.

It takes effect within `PAYMENT_SWITCH_CACHE_SECONDS` (default 10). §5.1 stops
offering it and §5.2 refuses a charge for it, so customers already holding the
page are covered too.

**Turning it back on is another INSERT, never a DELETE or an UPDATE:**

```sql
INSERT INTO payment_method_switches (method_id, bank_code, available, reason, changed_by)
VALUES ('bank_transfer', 'bca', true, 'BCA recovered 21:28', 'your-name');
```

The table is append-only by design. A `DELETE` would re-enable the bank and
destroy the answer to the question you will actually be asked afterwards — why
could nobody pay with BCA between 20:14 and 21:30, and who decided that.

**What is switched off right now:**

```sql
SELECT DISTINCT ON (method_id, bank_code) method_id, bank_code, available, reason, changed_by, changed_at, expires_at
FROM payment_method_switches
WHERE expires_at IS NULL OR expires_at > now()
ORDER BY method_id, bank_code, changed_at DESC, id DESC;
```

Rows with `available = true` are switches that have **ended**; only
`available = false` is in force.

Two things this cannot do. It cannot turn a method **on** that the code does
not implement — a row for `credit_card` is ignored and logged, because a method
needs a charge-body shaper and an instruction extractor in Go before anything
can be charged with it. And if Aurora is unreachable the switch fails **open**,
offering everything: a kill switch that hides every payment method when its own
storage hiccups is a worse outage than the one it exists for, and in that state
§5.2 cannot charge anything anyway.

---

## Sale dashboard and alarms

One stack, `rusuh-<env>-observability` (`infra/observability/template.yaml`), holds the dashboards
`rusuh-<env>-sale` (the sale) and `rusuh-<env>-api` (every endpoint) and seven alarms that all notify the SNS topic `rusuh-<env>-alarms`. Metric names, formulas
and the dashboard layout: `docs/metrics.md`. During a sale, keep the dashboard open with the time range set to the
sale window and the event picked in the dropdown.

**Setup, once:** the deploy role needs the `Observability` statement of `infra/bootstrap/github-oidc-role.yaml`
(redeploy that stack), and the repository variable `ALARM_EMAIL` names who gets the alarms
(`gh variable set ALARM_EMAIL --body team@example.com`). The address must click the confirmation email once. CI deploys
the stack when `infra/observability/**` changes, or with `force_deploy=observability`.

| Alarm | Means | Do this |
|---|---|---|
| `rusuh-<env>-queue-stalled` | No sse-server task has heard admission-ctl for > 30 s (NFR-14). Customers see "reconnecting", the queue is frozen. | Check the admission-ctl task: `aws logs tail /ecs/rusuh-<env>/admission-ctl --since 15m`. If it is running but silent, restart the service (`aws ecs update-service --cluster rusuh-<env> --service admission-ctl --force-new-deployment`). |
| `rusuh-<env>-admission-ctl-down` | No running admission-ctl task for 2 min. Nobody is let in; expired bookings are not swept. | `aws ecs describe-services --cluster rusuh-<env> --services admission-ctl` → the stopped task's reason (image, secret, crash). Fix and redeploy. |
| `rusuh-<env>-stock-negative` | The hold script refused a hold that would take stock below zero (NFR-04). | Compare Redis `stock:{ev}:*` with Aurora for the event. Reconciliation corrects Redis once the counter holds still; if the sale is busy, it may wait. |
| `rusuh-<env>-late-settlement` | Midtrans settled money for a booking that had already expired. Booking marked disputed, customer emailed. | Refund by hand. The dashboard's *Disputed bookings* table lists event, booking, `order_id` and amount (FR-36). |
| `rusuh-<env>-email-dlq` | E-tickets or notices failed every retry (NFR-10). | Read the message in `rusuh-<env>-notification-dlq`, fix the cause (SES, template), redrive the DLQ. |
| `rusuh-<env>-api-5xx` | > 1 % of API requests answered 5xx over 5 min. | The dashboard's *Lambda errors and throttles* widget names the service; then `aws logs tail /aws/lambda/rusuh-<env>-<svc>`. |
| `rusuh-<env>-tick-overrun` | admission-ctl ticks take > 500 ms, 20 times a minute, for 3 min. Admission slows for every event. | Look at *admission-ctl tick*, *Redis* and *Aurora* on the dashboard: usually Aurora scaling up from 0 ACU or a slow query. |
| `rusuh-<env>-webhook-forbidden-ip` | A webhook request came from an IP outside the allowlist. | **Check the logged IP against https://docs.midtrans.com/docs/ip-address FIRST.** If Midtrans changed their IPs, settlements are being dropped NOW — update the SSM parameter `/rusuh/<env>/midtrans/webhook-ip-allowlist` and redeploy payment-api. If the IP is not Midtrans's, someone is probing; the 200 response reveals nothing, but review access patterns. |

**A failing or slow endpoint** (no alarm of its own): open the dashboard `rusuh-<env>-api`. The *Per route* table
gives requests, success %, 4xx %, 5xx % and p50/p95/p99 latency per route for the time range; the two charts under
it show when it started; the bottom row shows which Lambda errors, slows down or is throttled. Then
`aws logs tail /aws/lambda/rusuh-<env>-<svc> --since 30m`.

## When something fails

| Symptom | Do this |
|---|---|
| `Not authorized to perform sts:AssumeRoleWithWebIdentity` | The token is valid, the trust policy rejected it. Compare the `sub` claim: branch, environment, owner casing, and the immutable `@id` prefix. |
| `The web identity token provided could not be validated` | The provider is missing. `aws iam list-open-id-connect-providers`. Never set `CreateOidcProvider=false` on a stack that created it — `DeletionPolicy: Retain` now prevents the deletion, but older stacks lost it this way. |
| Foundation left in `ROLLBACK_*`, retry fails on "already exists" | `bash infra/cleanup-failed-foundation.sh` (dry run first). It refuses to touch a healthy stack and will not delete the NIK pepper. |
| A service returns 500 on `/sys/ping` | Read its log group — `aws logs tail /aws/lambda/rusuh-prod-<svc> --since 30m`. Init failures are almost always a bad `DATABASE_URL` or an unreachable dependency. |
| `password authentication failed` | The cluster and the connection string are a generation apart. See *The one thing to internalise*. |
| One bank is failing but Midtrans is up | Switch that bank off — see *Turning a payment method off, mid-sale* above. The breaker cannot see inside Midtrans. |
| Lint fails with `Unexpected HTTP response: 504` | Not a lint finding — the action could not download its binary. Re-run. `max-parallel: 2` makes it rare. |

---

## AWAITING_PAYMENT sweep metrics

The backstop sweep runs in admission-ctl and invokes payment-api every
`SWEEP_AWAITING_INTERVAL_SECONDS` (default 60) to poll Midtrans for bookings
whose webhook never arrived.

| Metric | What it means | What to do |
|---|---|---|
| `AwaitingSweptExpired` | Bookings expired by the sweep. Normal during a sale — these are payments that timed out at Midtrans. | Nothing; this is the sweep working. |
| `AwaitingSweptSettled` | **Alarm.** Bookings confirmed by the sweep, not by the webhook. The webhook is broken. | Check Midtrans dashboard notification settings. Verify the webhook URL. Check payment-api logs for signature failures. |
| `AwaitingSweepSkipped` | Bookings skipped: gateway unreachable, no `payment_ref`, or a non-terminal status like `pending`. | If high and sustained, check Midtrans connectivity. The sweep fails closed — it won't expire a booking it can't poll. |
| `AwaitingSweepFailed` | The sweep invocation itself failed (admission-ctl → payment-api Lambda). | Check admission-ctl logs for IAM or Lambda errors. Verify `PAYMENT_API_FUNCTION` is set correctly. |

**Non-zero `AwaitingSweptSettled` is the single most important alarm.** It means
customers are paying for tickets but not receiving them, and only the sweep is
catching them. The root cause is always in the webhook path — the sweep is the
symptom, not the problem. Common causes:

1. Webhook URL not configured in Midtrans dashboard.
2. Signature secret key mismatch (`MIDTRANS_SIGNATURE_SECRET_KEY`).
3. API Gateway rejecting the callback (check unified gateway logs).
4. Lambda throttling or timeout (check payment-api metrics).

To investigate:
```bash
# Recent sweep results
aws logs filter-log-events --log-group-name /aws/lambda/rusuh-prod-payment-api \
  --filter-pattern "sweep_awaiting_payment" --since 1h

# Webhook activity (should see notifications)
aws logs filter-log-events --log-group-name /aws/lambda/rusuh-prod-payment-api \
  --filter-pattern "payment notification" --since 1h
```

---

## Promoting to `main`

When the pipeline is proven, redeploy the one stack whose trust policy names
the branch:

```bash
aws cloudformation deploy \
  --template-file infra/bootstrap/github-oidc-role.yaml \
  --stack-name rusuh-cd-bootstrap \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    GitHubRepo=OWNER/REPO \
    SubClaimPrefix='<same as before>' \
    AllowedBranch=main \
    AllowedEnvironment=prod \
    CreateOidcProvider=true

DEPLOY_BRANCH=main bash infra/bootstrap/configure-github.sh
```

**Change the role before merging, not after** — the role is the gate, so
merging first means the push fails at assume-role and has to be re-run.

Nothing else is redone: the SES identity, the secrets, the schema and the
foundation all persist. Stack names derive from the environment, never the
branch, so merging updates the same stacks — no second Aurora, no duplicate
bill.

`workflow_dispatch` appears once `main` is the deploy branch, which is the
only way to force `deploy_foundation=true` or turn off `cost_optimized`.

---

## What is not automated, and why

| | Why |
|---|---|
| The bootstrap role | Creates the role CI assumes. Chicken-and-egg. |
| SES verification | Requires a click in a mailbox. |
| `SubClaimPrefix` | Per-repository numeric IDs; must be read from GitHub. |
| The schema | A POC decision. Tables are created by hand with a SQL client. |
| Production SES access | Human review at AWS. |

Everything else is in the pipeline.

> **Never run clean.** Every one of these steps works, but this procedure was
> arrived at by repairing a half-built stack, not by deploying into an empty
> account start to finish. A first run elsewhere may still surface something.
> If it matters — a tender demo, a second environment — do a full teardown and
> redeploy once, deliberately, rather than discovering it under time pressure.
