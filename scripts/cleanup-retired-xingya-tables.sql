-- Optional archival of the retired deferred-invite ledger and registration audit table.
--
-- Current policy: keep both tables and every historical row exactly as-is. No
-- settlement, no cancellation, no background processing. Run this script only if a
-- future decision explicitly asks to archive the records.
--
-- If that decision is made, first verify:
--   1. a database backup exists;
--   2. the rc49 server has run without reading these tables for at least one full
--      retention window;
--   3. the operator accepts that the rows become read-only archive data.
--
-- This script only renames the tables. Renaming is reversible and keeps the data
-- readable; drop the *_archive_* tables in a later maintenance window if desired.
-- Do NOT drop users.success_calls_after_invite_window: it is a nullable/defaulted
-- legacy column and still part of the known schema.
--
-- PostgreSQL is the production engine. Run inside a transaction.

BEGIN;

ALTER TABLE IF EXISTS xingya_invite_reward_pending
  RENAME TO xingya_invite_reward_pending_archive_20261109;

ALTER TABLE IF EXISTS xingya_registration_devices
  RENAME TO xingya_registration_devices_archive_20261109;

COMMIT;

-- Later, only after the archive observation period:
-- DROP TABLE IF EXISTS xingya_invite_reward_pending_archive_20261109;
-- DROP TABLE IF EXISTS xingya_registration_devices_archive_20261109;
