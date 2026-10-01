package model

import (
	"errors"

	"gorm.io/gorm"
)

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
	return nil
}
