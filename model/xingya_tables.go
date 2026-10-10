package model

import (
	"errors"
	"sync/atomic"

	"gorm.io/gorm"
)

var xingyaTablesReady atomic.Bool

// ensureXingyaTables creates the Xingya-owned tables on a database that does not
// have them yet, and does nothing at all when they already exist.
//
// These tables are deliberately kept out of the AutoMigrate list in migrateDB.
// AutoMigrate is free to reshape whatever the Go struct declares, and rc41 added
// a migrator that drops single-column unique constraints under an ACCESS
// EXCLUSIVE lock. Nothing upstream ever needs to inspect a Xingya-owned table, so
// letting it into that path would only expose fork-only state — including the
// invite-reward idempotency uniqueness — to a migration written for upstream
// tables. Creating a missing table is safe and idempotent; reshaping one is not.
//
// The log_refunds precedent (ensureLogRefundTable) established this pattern for a
// financially sensitive table; this is the same rule applied to the rest.
func ensureXingyaTables(db *gorm.DB) error {
	if db == nil {
		return errors.New("星芽数据表初始化失败：数据库未就绪")
	}
	for _, table := range []any{
		&LogRefund{},
		&XingyaInviteRewardPending{},
		&XingyaRegistrationDevice{},
	} {
		if db.Migrator().HasTable(table) {
			continue
		}
		if err := db.AutoMigrate(table); err != nil {
			return err
		}
	}
	if err := migrateXingyaTables(db); err != nil {
		return err
	}
	xingyaTablesReady.Store(true)
	return nil
}

// migrateXingyaTables performs additive upgrades for existing Xingya-owned tables.
// GORM's migrator keeps the same column/index operations valid on PostgreSQL, MySQL,
// and SQLite; the global upstream migration list deliberately does not own these tables.
func migrateXingyaTables(db *gorm.DB) error {
	reward := &XingyaInviteRewardPending{}
	for _, column := range []string{"QualifyingCalls", "QualifyingQuota", "EligibleAt", "AutoGrantAt", "GrantMethod"} {
		if db.Migrator().HasColumn(reward, column) {
			continue
		}
		if err := db.Migrator().AddColumn(reward, column); err != nil {
			return err
		}
	}

	device := &XingyaRegistrationDevice{}
	for _, index := range []string{
		"idx_xingya_invite_reward_state_auto_grant",
		"idx_xingya_invite_reward_inviter",
		"idx_xingya_regdev_ip_ua_time",
	} {
		var value any
		switch index {
		case "idx_xingya_invite_reward_state_auto_grant", "idx_xingya_invite_reward_inviter":
			value = reward
		default:
			value = device
		}
		if !db.Migrator().HasIndex(value, index) {
			if err := db.Migrator().CreateIndex(value, index); err != nil {
				return err
			}
		}
	}

	for _, index := range []string{
		"idx_xingya_invite_reward_state_auto",
		"idx_xingya_invite_reward_state_time",
		"idx_xingya_invite_reward_pending_inviter_id",
		"idx_xingya_regdev_ip_time",
		"idx_xingya_regdev_prefix_time",
	} {
		if db.Migrator().HasIndex(reward, index) || db.Migrator().HasIndex(device, index) {
			if err := dropXingyaIndex(db, reward, device, index); err != nil {
				return err
			}
		}
	}
	return nil
}

func dropXingyaIndex(db *gorm.DB, reward, device any, index string) error {
	if db.Migrator().HasIndex(reward, index) {
		return db.Migrator().DropIndex(reward, index)
	}
	return db.Migrator().DropIndex(device, index)
}
