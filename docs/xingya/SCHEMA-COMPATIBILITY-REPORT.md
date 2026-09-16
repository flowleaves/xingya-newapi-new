# Xingya Rewrite — Schema Compatibility Report (Deliverable 1)

> Scope: rc37 baseline (`codex/xingya-newapi-new` @ `385d2dfd10d821b25c8a6766bd16eea248cb1652`)
> vs. production PostgreSQL dump `C:\Users\fine\Desktop\xingya\sql\newapi.sql`.
>
> Method: **metadata only**. Table / column / index / constraint definitions were extracted
> from the dump (`_diag/xingya-rewrite/`). No row contents were read, copied or exposed.
> The dump is **never** to be edited in place or copied into the repository.

## 1. Verdict

**Compatible, additive-only.** The new runtime can run against the existing production
database without a destructive migration. Nothing needs to be dropped, truncated or
rebuilt. `log_refunds`, `checkins` and the four extended `redemptions` columns already
exist in production with exactly the shape the rc37 port requires.

## 2. Production inventory

- Dumped from / by: PostgreSQL **18.4**
- Tables: **35**
- Sequences: **24**, indexes: **171**, `ALTER TABLE` constraint blocks: **71**
- Owners in the dump: `user_DGrxGm` × 423 occurrences, `newapi_user` × 16 occurrences

### Production tables (35)

```
abilities                        log_refunds             subscription_orders
auth_flows                       logs                    subscription_plans
authz_roles                      midjourneys             subscription_pre_consume_records
casbin_rule                      models                  system_instances
channels                         options                 system_task_locks
checkins                         passkey_credentials     system_tasks
custom_oauth_providers           perf_metrics            tasks
external_identity_claims         prefill_groups          tokens
                                 quota_data              top_ups
                                 redemptions             two_fa_backup_codes
                                 setups                  two_fas
                                 user_oauth_bindings     user_sessions
                                 user_subscriptions      users
                                 vendors
```

## 3. Extension tables required by the port

### 3.1 `log_refunds` — PRESENT, exact match ✅

```sql
CREATE TABLE public.log_refunds (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    log_id bigint NOT NULL,
    funding_source character varying(16) NOT NULL,
    subscription_id bigint,
    base_quota bigint NOT NULL,
    quota bigint NOT NULL,
    refund_date character varying(10),
    reason character varying(32),
    request_id character varying(64),
    created_at bigint NOT NULL
);
```

Matches the port's `model.LogRefund` field-for-field. Every index the port relies on
already exists:

| Index | Columns | Purpose |
|---|---|---|
| `idx_log_refund_log_id` | `log_id` **UNIQUE** | idempotency boundary — blocks duplicate refunds |
| `idx_log_refunds_user_id` | `user_id` | ownership scoping |
| `idx_log_refunds_refund_date` | `refund_date` | daily-limit accounting |
| `idx_log_refunds_request_id` | `request_id` | user-facing lookup key |
| `idx_log_refunds_subscription_id` | `subscription_id` | subscription refund path |
| `idx_log_refunds_created_at` | `created_at` | retention / audit scans |

The unique index is the load-bearing part: it is what makes the refund insert itself the
idempotency check, so the port must **not** add a second unique constraint or rename it.

### 3.2 `checkins` — PRESENT, exact match ✅

```sql
CREATE TABLE public.checkins (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    checkin_date character varying(10) NOT NULL,
    quota_awarded bigint NOT NULL,
    created_at bigint
);
CREATE UNIQUE INDEX idx_user_checkin_date ON public.checkins USING btree (user_id, checkin_date);
```

`quota_awarded` is `bigint`, so the tiered reward (a converted internal-quota integer)
fits without narrowing. `idx_user_checkin_date` is the DB-level guard against concurrent
duplicate same-day claims — the ported `UserCheckin` depends on this index existing, and
it does.

### 3.3 `redemptions` — extended columns PRESENT ✅

The four Xingya columns required by the plan are all present, with safe defaults:

```sql
plan_id       bigint DEFAULT 0
plan_snapshot text DEFAULT ''::text
batch_id      character(32) DEFAULT ''::bpchar
batch_name    character varying(64) DEFAULT ''::character varying
```

All four are nullable/defaulted, so existing rows remain valid and readable. Per §2 and §6
of the plan these columns are **retained read-only**; the rewrite must not reinterpret old
rows or convert subscription quota into wallet quota.

### 3.4 Legacy subscription tables — PRESENT, retained read-only ✅

`subscription_plans`, `subscription_orders`, `subscription_pre_consume_records`,
`user_subscriptions` all exist, each with its own sequence and indexes
(`idx_subscription_orders_*`, `idx_subscription_pre_consume_records_*`,
`idx_subscription_orders_trade_no` UNIQUE). `user_subscriptions` is required by the
retained subscription refund path; the other three are audit-only.

## 4. Migration registration in rc37

`model/main.go` → `migrateDB()` already calls `AutoMigrate` for `&Redemption{}` (line 347)
and `&Checkin{}` (line 361), plus `&SubscriptionOrder{}`, `&UserSubscription{}`,
`&SubscriptionPreConsumeRecord{}` and `&SubscriptionPlan{}`.

**`LogRefund` is not registered.** The port must add `&LogRefund{}` to that `AutoMigrate`
list. Against production this is a **no-op** — the table and all six indexes already
exist, and GORM's `AutoMigrate` only adds what is missing. Against a clean rc37 fixture it
creates the table, which is why the port must keep the struct tags aligned with the
production DDL above (notably `uniqueIndex:idx_log_refund_log_id`).

## 5. Owner / permission discrepancy — OPERATOR ACTION REQUIRED ⚠️

- Most objects are owned by `user_DGrxGm`.
- `log_refunds` **and its sequence** `log_refunds_id_seq` are owned by `newapi_user`.

This is a real deployment risk: if the runtime role cannot `SELECT/INSERT/UPDATE` on
`log_refunds`, refunds fail at insert time with a permission error rather than a logic
error, and the failure surfaces as a generic "该请求已补回" to the user.

**Required before deployment:** verify the runtime role holds `SELECT, INSERT, UPDATE` on
`log_refunds` and `USAGE` on `log_refunds_id_seq`. Resolve by granting to the existing
owner or by aligning the deployment role — **never** by search-and-replace on the dump
(that would break ownership of the other 34 tables).

Verification SQL (read-only, safe to run):

```sql
SELECT c.relname, pg_get_userbyid(c.relowner) AS owner
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public'
  AND c.relname IN ('log_refunds','log_refunds_id_seq','checkins','users','redemptions')
ORDER BY c.relname;

SELECT grantee, privilege_type
FROM information_schema.role_table_grants
WHERE table_name = 'log_refunds';
```

## 6. Additive objects the port introduces

None at the table level. The port adds **no new table and no new column**. Everything the
retained feature set needs is already in production. The only registration change is
adding `&LogRefund{}` to `AutoMigrate` (§4), which is idempotent against production.

This is the strongest possible compatibility result: the entire Phase E concern reduces to
a permissions check rather than a schema migration.

## 7. Conclusion against the plan's audit gate

| Gate item (§10) | Status |
|---|---|
| official rc37 commit pinned and reproducible | ✅ `385d2dfd…`, clean working tree |
| production database role ownership/grants known | ⚠️ known but **must be verified** (§5) |
| no production dump in the build context | ✅ dump lives in `../sql/`, outside the repo |
| schema diff is additive-only | ✅ verified — no delta at all (§6) |

## 8. Explicitly not verified here

- **Row-level content** of any table (deliberately out of scope; metadata only).
- **Live database permissions** — §5 statements were derived from the dump, not executed
  against the running production instance. Running them is the operator's step.
- **Migration execution** on a sanitized PostgreSQL copy (plan Phase E step 2) — not
  performed; requires a database instance, which this baseline run did not provision.
