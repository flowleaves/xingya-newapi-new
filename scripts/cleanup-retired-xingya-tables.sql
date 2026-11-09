-- Post-deployment cleanup for the retired deferred-invite ledger and registration
-- audit table. Run only after:
--   1. a verified database backup exists;
--   2. cmd/xingya-invite-cleanup has been applied (eligible paid, pending cancelled);
--   3. the rc49 server has run without errors for at least one full retention window.
--
-- This script only renames the tables. Renaming is reversible and keeps the data
-- readable; drop the *_archive_* tables in a later maintenance window after the
-- observation period. Do NOT drop users.success_calls_after_invite_window: it is a
-- nullable/defaulted legacy column and still part of the known schema.
--
-- PostgreSQL is the production engine. Run inside a transaction.

BEGIN;

ALTER TABLE IF EXISTS xingya_invite_reward_pending
  RENAME TO xingya_invite_reward_pending_archive_20261109;

ALTER TABLE IF EXISTS xingya_registration_devices
  RENAME TO xingya_registration_devices_archive_20261109;

COMMIT;

-- Later, after verification:
-- DROP TABLE IF EXISTS xingya_invite_reward_pending_archive_20261109;
-- DROP TABLE IF EXISTS xingya_registration_devices_archive_20261109;
