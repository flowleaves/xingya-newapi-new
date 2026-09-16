# Xingya NewAPI New Rewrite Plan

## 1. Branch Baseline

- Repository: `xingya-newapi-new`
- Base tag: official `v1.0.0-rc.37`
- Base commit: `385d2dfd10d821b25c8a6766bd16eea248cb1652`
- Working branch: `codex/xingya-newapi-new`
- Source of truth for production data: `C:\Users\fine\Desktop\xingya\sql\newapi.sql`
- The production dump is intentionally excluded from this repository.

This branch is a clean rc37 copy. The older `xingya-newapi` branch is not used as
the code base for the rewrite. Its security fixes and feature implementations are
reference material only and must be re-reviewed before selective porting.

## 2. Product Scope

### Keep

1. Core NewAPI gateway and provider relay compatibility.
2. User authentication, sessions, API tokens, channels, models, usage logs and
   administrator operations required by the core gateway.
3. Wallet recharge and existing payment callbacks.
4. Tiered check-in rewards, including yesterday-usage and historical-usage rules.
5. Self refund for wallet-funded requests.
6. Self refund for subscription-funded requests: this is retained as an
   **automatic refund funding path**. It restores the consumed subscription
   allowance and must not mint wallet quota.
7. Frontend wallet/recharge, check-in, usage-log and self-refund entry points.

### Drop from the new product surface

1. Context-length limit feature and all related settings, enforcement, audit UI
   and documentation.
2. Tiered-expression/per-request pricing and all related expression editors,
   fallback logic and pricing UI.
3. Subscription-code issuance, batch management, redemption and subscription
   code UI.

> **CORRECTION — operator, 2026-09-16.** An earlier revision of this document also
> listed the **subscription mechanism itself** (plans, checkout/payment products,
> subscription administration UI, subscription preference) as droppable. **That was
> wrong and is no longer in scope.** The operator keeps subscriptions; only the
> subscription **code** mechanism was ever in scope.
>
> **Reality check against this branch (`xingya-newapi-new`, clean rc37):** items
> 1, 2 and 3 above **do not exist here** — the old fork's `context_limit_setting`,
> Xingya's per-request `v2:` pricing, and the `redemptions.plan_id/plan_snapshot/
> batch_id/batch_name` columns were never ported. Phase C therefore has **no
> removals to perform** on this branch; it reduces to branding plus the
> recharge-1 page. (Removing the subscription product surface was attempted once
> and has been **fully reverted**; those files are byte-identical to rc37 again.)
>
> Upstream `pkg/billingexpr` (v1, USD per million tokens) is **core billing and
> must never be removed** — it is not the "tiered-expression pricing" this plan
> referred to.

### Keep as compatibility-only internals

Existing subscription consumption records and subscription tables must not be
deleted during the first rewrite migration. They are needed to preserve historic
usage and to support subscription-funded automatic refunds. New subscription
purchase/code flows are disabled, while the refund path can read and reverse
eligible historical subscription consumption.

## 3. Database Strategy

### Tables required by the new runtime

- `users`, `tokens`, `user_sessions`, authentication/authorization tables
- `channels`, `models`, `vendors`, `abilities`, `prefill_groups`
- `logs`, `tasks`, `midjourneys`, `quota_data`, `perf_metrics`
- `top_ups`
- `checkins`
- `log_refunds`
- `options`, `setups`, 2FA/passkey/OAuth tables used by enabled routes

### Tables retained but made legacy/read-only at first

- `subscription_plans`
- `subscription_orders`
- `subscription_pre_consume_records`
- `user_subscriptions`
- `redemptions` and its `plan_id`, `plan_snapshot`, `batch_id`, `batch_name`
  columns

No table or column is dropped in the first production migration. Historical
subscription data remains available for audit and automatic subscription refund.
A later archive project may move legacy data after a separately approved backup
and retention period.

### `log_refunds` contract

The table is the idempotency and audit boundary for both funding sources:

- `funding_source=wallet`: credit wallet quota after the transaction commits.
- `funding_source=subscription`: decrement the original subscription usage;
  never credit wallet quota.
- `log_id` remains unique to prevent duplicate refunds.
- `request_id` is the preferred user-facing lookup key.

The migration must verify owner, grants, indexes and sequence ownership. The
production dump currently shows most tables owned by `user_DGrxGm`, while
`log_refunds` is owned by `newapi_user`; this mismatch must be resolved in the
deployment role configuration, not by editing the dump blindly.

## 4. Implementation Phases

### Phase A: Freeze the rc37 baseline

- Record the rc37 commit and module versions.
- Run rc37 backend/frontend build and relevant tests before modifications.
- Generate a model/router inventory from rc37.
- Do not copy production SQL into the repository or test workspace.

### Phase B: Port only the approved Xingya behavior

- Port tiered check-in calculation and validation from the old branch.
- Port self-refund eligibility, request-id ownership checks and idempotency.
- Port wallet refund cache invalidation and quota ceiling checks.
- Port subscription refund as a separate `SubscriptionRefundFunding` path.
- Add regression tests for wallet and subscription refund settlement.
- Preserve rc37's locking and database compatibility helpers.

### Phase C: Remove product surfaces

- Remove context-limit routes, settings, controller/service calls and frontend
  pages.
- Remove tiered-expression pricing routes, settings, parser/editor and UI.
- Remove subscription-code routes, code generation, redemption UI and admin
  menus.
- Remove subscription purchase/payment routes and subscription management UI.
- Keep model types and read paths needed by automatic subscription refund until
  the legacy-data retirement decision is separately approved.

### Phase D: Frontend reduction

- Keep Wallet/Recharge, Check-in, Usage Logs and Self Refund routes.
- Keep only payment entry points required for wallet recharge.
- Remove subscription, context-limit and tiered-pricing navigation and lazy
  chunks.
- Run i18n sync, typecheck, lint on touched files, and production build.

### Phase E: Data compatibility validation

- Compare rc37 schema with the production dump using table/column/index
  metadata only.
- Validate additive migrations on a sanitized PostgreSQL copy.
- Verify row counts and aggregate checksums without exposing row contents.
- Test existing wallet users, existing subscription users, old logs, old
  check-ins, old top-ups and existing `log_refunds` rows.

### Phase F: Release gate

- Full backup and tested restore.
- Dry-run schema migration with the production database role.
- Read-only smoke test.
- Low-traffic canary.
- Verify recharge, API call, check-in, wallet refund and subscription refund.
- Confirm no subscription purchase/code endpoint remains reachable.
- Keep the previous image and database rollback procedure available.

## 5. Required Regression Matrix

### Check-in

- No prior usage, paid usage and subscription usage.
- Threshold boundary values and inverted/NaN reward configuration.
- Duplicate same-day claim.
- Wallet quota ceiling and missing/soft-deleted user.
- SQLite, MySQL and PostgreSQL transaction behavior.

### Automatic refund

- Empty wallet-funded response.
- Truncated wallet-funded stream.
- Empty subscription-funded response.
- Truncated subscription-funded stream.
- Subscription refund restores `amount_used` only.
- Subscription refund never changes wallet quota.
- Expired/deleted/reset subscription is rejected.
- Duplicate request is idempotently rejected.
- Mismatched `request_id` and `log_id` is rejected.
- Cache reflects wallet refund after commit.

### Removed features

- Context-limit configuration and endpoints return not found/disabled.
- Tiered-expression pricing cannot be created or evaluated.
- Subscription-code creation/redemption is unavailable.
- Subscription purchase/payment endpoints are unavailable.
- Existing legacy rows remain readable by audit/refund paths.

## 6. Explicit Non-Goals

- No production database rewrite in this branch.
- No deletion of legacy subscription tables in the first release.
- No balance or API-key normalization.
- No provider/channel batch edits.
- No Cloudflare, Nginx, Docker or production credential changes.
- No automatic conversion of historical subscription data into wallet quota.

## 7. Delivery Artifacts

Before implementation is considered complete, this branch must contain:

1. A schema compatibility report against the production dump.
2. A reviewed additive migration script or verified rc37 `AutoMigrate` path.
3. A feature removal inventory with route and frontend references.
4. Check-in and both funding-source refund regression tests.
5. A deployment/rollback runbook that never requires deleting production data.

## 8. Current Production Schema Findings (2026-09-16)

The production PostgreSQL dump at `C:\Users\fine\Desktop\xingya\sql\newapi.sql`
is a PostgreSQL 18.4 dump and already contains the Xingya extensions. The dump
must remain outside the repository and must never be edited in place.

### Confirmed production tables

The dump contains the normal NewAPI tables plus `checkins`, `log_refunds`,
`subscription_plans`, `subscription_orders`,
`subscription_pre_consume_records`, `user_subscriptions`, and the extended
`redemptions` table.

### Confirmed production additions

`log_refunds` exists with the following contract: wallet/subscription funding
source, original log id, subscription id, base quota, refund quota, request id,
date and created timestamp. `log_id` has a unique index.

`redemptions` contains `plan_id`, `plan_snapshot`, `batch_id` and `batch_name`.
Existing rows must be preserved. The rewrite must not reinterpret old rows or
convert subscription quota into wallet quota.

### Owner/permission discrepancy

Most tables in the dump are owned by `user_DGrxGm`, while `log_refunds` and its
sequence are owned by `newapi_user`. Before deployment, the operator must verify
the runtime role has SELECT/INSERT/UPDATE and migration permissions. Do not
replace owners by search-and-replace in the SQL dump.

### Compatibility conclusion

The target runtime can be additive-compatible with the production database:
keep existing tables and columns, add nothing destructive, and disable unwanted
features at the route/service layer. A clean rc37 database must not be used as a
replacement database; it is only a schema/reference fixture.

## 9. Exact Implementation Plan for This Branch

### 9.1 Baseline verification

Before touching application code:

1. Run `go build ./...` and the rc37 model/service tests.
2. Run `cd relaykit; GOWORK=off go build ./...`.
3. Run frontend `bun run typecheck` and `bun run build`.
4. Capture the rc37 route inventory and `model/migrateDB` inventory.
5. Keep this branch's initial rc37 commit available for rollback.

### 9.2 Backend files to port or adapt

Port from the old `xingya-newapi` only after comparing against rc37:

- `model/checkin.go`: tiered reward calculation, SQL usage aggregation,
  normalization, quota ceiling and cache update.
- `setting/operation_setting/checkin_setting.go`: reward tier configuration and
  defensive sanitization.
- `controller/checkin.go`, `router/api-router.go`: check-in behavior and rate
  limiting, preserving rc37 middleware order.
- `model/log_refund.go`: refund table access, idempotency and bounded wallet
  credit.
- `service/self_refund.go`: eligibility, request-id ownership and dual-selector
  verification.
- `controller/self_refund.go`: request parsing and fail-closed validation.
- `model/log.go`: refund log type and request-id lookup, only where rc37 does
  not already provide an equivalent.
- `model/user_cache.go`: only the smallest cache invalidation API needed after
  a wallet refund.

Do not copy entire directories. Every port must be reconciled with rc37 model
fields, billing session APIs, row-lock helpers and error types.

### 9.3 Subscription automatic refund design

This is the one subscription capability intentionally retained:

1. Original usage log identifies `billing_source=subscription` and
   `subscription_id`.
2. Self-refund revalidates user ownership, request path, time window, refund
   ratio and completion/truncation conditions.
3. A transaction inserts one `log_refunds` row using the real log primary key.
4. The subscription row is locked with the rc37 cross-database locking helper.
5. `amount_used` is reduced with a lower bound of zero.
6. Wallet `users.quota` is not changed.
7. The refund is recorded as an audit event with wallet amount zero.

If the subscription is expired, deleted, cancelled or already reset to a new
cycle, the refund must fail closed rather than reducing current-cycle usage.

### 9.4 Features to remove from product routes only

Remove or disable the following route families after the kept flows work:

- subscription-code generation and redemption routes
- context-limit settings/API/routes
- tiered-expression pricing settings/API/routes

> **CORRECTION — operator, 2026-09-16.** This list previously also named
> `/api/subscription/*` purchase/preference/payment routes, the subscription admin
> plan/binding/reset routes, and the subscription payment callbacks. **Those stay.**
> The operator keeps the subscription mechanism; only the subscription **code**
> mechanism was in scope — and it does not exist on this branch, so this section is
> **a no-op here**.

The subscription model files and tables remain in the first release for
historical reads and automatic refund. Route removal is not permission to drop
tables.

### 9.5 Frontend scope

The old Xingya UI is the visual reference for only these areas:

- `web/src/features/home/**` and the public home route
- authenticated check-in route and check-in calendar/rules components
- self-refund route, API types and detail-dialog refund card
- wallet/recharge route and sidebar/mobile entry points

All other rc37 pages remain unchanged unless they contain a removed feature's
navigation entry. Remove only subscription, context-limit and tiered-pricing
menus/pages; do not redesign unrelated admin, model, channel or usage pages.

The old UI must be adapted to rc37 APIs and generated route types. Do not copy
the old generated `routeTree.gen.ts`; regenerate it after route changes.

### 9.6 Database-safe release sequence

1. Back up primary and log databases and test restore.
2. Run metadata-only schema comparison against the production dump.
3. Verify `log_refunds` and the four `redemptions` columns exist; add only
   missing additive objects through reviewed SQL or rc37 migration code.
4. Start the new image with all destructive/legacy routes disabled.
5. Execute read-only smoke tests.
6. Test one controlled recharge, check-in, wallet refund and subscription
   refund using preselected test accounts.
7. Compare aggregate balances, check-in totals and refund counts before/after.
8. Canary release, monitor errors, then broaden traffic.

No step may truncate, rebuild, drop or bulk-update production tables.

## 10. Audit Gate Before Operator Execution

The operator should not execute migration until all of the following are true:

- official rc37 commit is pinned and reproducible;
- production database role ownership/grants are known;
- no production dump is in the build context;
- schema diff is additive-only;
- subscription refund tests prove wallet quota remains unchanged;
- duplicate refund and mismatched selector tests pass;
- check-in boundary/ceiling tests pass on PostgreSQL;
- removed endpoints return not found/disabled without touching data;
- old image and database restore procedure are tested.

This document is a plan and audit baseline, not an authorization to run a
production migration.
