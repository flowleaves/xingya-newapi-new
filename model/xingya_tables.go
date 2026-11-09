package model

import (
	"errors"
	"sync/atomic"

	"gorm.io/gorm"
)

var xingyaTablesReady atomic.Bool

// InitXingyaTables creates the Xingya-owned tables on a database that does not
// have them yet, and does nothing at all when they already exist.
//
// These tables are deliberately kept out of the AutoMigrate list in migrateDB.
// AutoMigrate is free to reshape whatever the Go struct declares, and rc41 added
// a migrator that drops single-column unique constraints under an ACCESS
// EXCLUSIVE lock. Nothing upstream ever needs to inspect a Xingya-owned table, so
// letting it into that path would only expose fork-only state to a migration
// written for upstream tables. Creating a missing table is safe and idempotent;
// reshaping one is not.
//
// The log_refunds precedent (ensureLogRefundTable) established this pattern for a
// financially sensitive table; this is the same rule applied to the rest.
func InitXingyaTables(db *gorm.DB) error {
	if db == nil {
		return errors.New("星芽数据表初始化失败：数据库未就绪")
	}
	for _, table := range []any{
		&LogRefund{},
		&XingyaInviteRebate{},
		&XingyaCheckinWallet{},
		&XingyaCheckinGrant{},
		&XingyaCheckinSpend{},
		&XingyaCheckinCreditLot{},
		&XingyaCheckinAllocation{},
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
	if err := migrateCheckinCreditExpiry(db, 0); err != nil {
		return err
	}
	if err := migrateCheckinLegacyReview(db); err != nil {
		return err
	}
	xingyaTablesReady.Store(true)
	return nil
}

// migrateXingyaTables performs additive upgrades for existing Xingya-owned tables.
// GORM's migrator keeps the same column/index operations valid on PostgreSQL, MySQL,
// and SQLite; the global upstream migration list deliberately does not own these tables.
func migrateXingyaTables(db *gorm.DB) error {
	wallet := &XingyaCheckinWallet{}
	if !db.Migrator().HasColumn(wallet, "Expired") {
		if err := db.Migrator().AddColumn(wallet, "Expired"); err != nil {
			return err
		}
	}
	if !db.Migrator().HasColumn(wallet, "Borrowed") {
		if err := db.Migrator().AddColumn(wallet, "Borrowed"); err != nil {
			return err
		}
	}
	spend := &XingyaCheckinSpend{}
	for _, column := range []string{"BorrowedReserved", "BorrowedConsumed", "BalanceAfter"} {
		if !db.Migrator().HasColumn(spend, column) {
			if err := db.Migrator().AddColumn(spend, column); err != nil {
				return err
			}
		}
	}
	// Build the replacement uniqueness before removing the former daily index.
	// Existing rewards enter round zero and retain all amounts and expiry dates.
	return db.Transaction(func(tx *gorm.DB) error {
		grant := &XingyaCheckinGrant{}
		if !tx.Migrator().HasColumn(grant, "ClaimRound") {
			if err := tx.Migrator().AddColumn(grant, "ClaimRound"); err != nil {
				return err
			}
		}
		if !tx.Migrator().HasIndex(grant, "idx_xingya_checkin_grant_round") {
			if err := tx.Migrator().CreateIndex(grant, "idx_xingya_checkin_grant_round"); err != nil {
				return err
			}
		}
		if tx.Migrator().HasIndex(grant, "idx_xingya_checkin_grant_day") {
			return tx.Migrator().DropIndex(grant, "idx_xingya_checkin_grant_day")
		}
		return nil
	})
}
