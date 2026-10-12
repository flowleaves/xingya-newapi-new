package model

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// This is the released rc42 schema, before the expired balance column exists.
type preExpiryCheckinWallet struct {
	UserID    int `gorm:"primaryKey;autoIncrement:false"`
	Available int `gorm:"type:bigint;not null;default:0"`
	Reserved  int `gorm:"type:bigint;not null;default:0"`
	Granted   int `gorm:"type:bigint;not null;default:0"`
	Consumed  int `gorm:"type:bigint;not null;default:0"`
}

func (preExpiryCheckinWallet) TableName() string { return "xingya_checkin_wallets" }

func checkinMigrationPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_XINGYA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN is required")
	}
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	initCol()
	t.Cleanup(func() { common.SetDatabaseTypes(oldMainType, oldLogType); initCol() })
	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	admin, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	adminPool, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminPool.Close()) })
	schema := fmt.Sprintf("checkin_expiry_test_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	// Only the unique schema created above is removed; no existing table is touched.
	t.Cleanup(func() { require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error) })
	config.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*config)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool, PreferSimpleProtocol: true}), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	return db
}

func TestXingyaCheckinExpiryMigrationPostgres(t *testing.T) {
	db := checkinMigrationPostgres(t)
	oldReady := xingyaTablesReady.Load()
	t.Cleanup(func() { xingyaTablesReady.Store(oldReady) })
	xingyaTablesReady.Store(false)
	require.NoError(t, db.AutoMigrate(&Option{}, &preExpiryCheckinWallet{}, &XingyaCheckinGrant{}, &XingyaCheckinSpend{}))
	require.False(t, db.Migrator().HasColumn(&XingyaCheckinWallet{}, "Expired"))
	require.NoError(t, db.Create(&preExpiryCheckinWallet{UserID: 1, Available: 12000, Granted: 15000, Consumed: 3000}).Error)
	require.NoError(t, db.Create(&preExpiryCheckinWallet{UserID: 2, Available: 2000, Reserved: 1000, Granted: 5000, Consumed: 2000}).Error)
	grant := XingyaCheckinGrant{UserID: 1, CheckinDate: "2026-10-11", QuotaAwarded: 15000, CreatedAt: 1791705600}
	require.NoError(t, db.Create(&grant).Error)
	spend := XingyaCheckinSpend{RequestID: "upgrade-pending-fixture", UserID: 2, Reserved: 1000, State: "reserved", CreatedAt: 1791705600, UpdatedAt: 1791705600}
	require.NoError(t, db.Create(&spend).Error)

	// Additive schema operations may finish, but opening and its marker roll back.
	require.ErrorContains(t, InitXingyaTables(db), "旧预留请求")
	assert.False(t, xingyaTablesReady.Load())
	assert.True(t, db.Migrator().HasColumn(&XingyaCheckinWallet{}, "Expired"))
	var count int64
	require.NoError(t, db.Model(&XingyaCheckinCreditLot{}).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, db.Model(&Option{}).Where("key = ?", checkinExpiryMigration).Count(&count).Error)
	assert.Zero(t, count)

	// The old-version maintenance path finishes its request before upgrade retries.
	require.NoError(t, db.Model(&spend).Updates(map[string]any{"state": "settled", "consumed": 1000, "actual_quota": 1000}).Error)
	require.NoError(t, db.Model(&preExpiryCheckinWallet{}).Where("user_id = ?", 2).Updates(map[string]any{"reserved": 0, "consumed": 3000}).Error)
	at := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, migrateCheckinCreditExpiry(db, at))
	for range 2 {
		require.NoError(t, InitXingyaTables(db))
	}
	require.NoError(t, migrateCheckinCreditExpiry(db, at+86400))
	var marker Option
	require.NoError(t, db.First(&marker, "key = ?", checkinExpiryMigration).Error)
	assert.Equal(t, strconv.FormatInt(at, 10), marker.Value)
	var lots []XingyaCheckinCreditLot
	require.NoError(t, db.Order("user_id").Find(&lots).Error)
	require.Len(t, lots, 2)
	assert.Equal(t, 12000, lots[0].Available)
	assert.Equal(t, 15000, lots[0].Granted)
	assert.Equal(t, 3000, lots[0].Consumed)
	assert.Equal(t, 2000, lots[1].Available)
	for _, lot := range lots {
		assert.Equal(t, "opening", lot.Source)
		assert.Zero(t, lot.GrantID)
		assert.Equal(t, at+int64(CheckinCreditLifetime/time.Second), lot.ExpiresAt)
		assert.True(t, validCheckinLot(lot))
	}
	var wallets []XingyaCheckinWallet
	require.NoError(t, db.Order("user_id").Find(&wallets).Error)
	assert.Equal(t, []XingyaCheckinWallet{{UserID: 1, Available: 12000, Granted: 15000, Consumed: 3000}, {UserID: 2, Available: 2000, Granted: 5000, Consumed: 3000}}, wallets)
	var savedGrant XingyaCheckinGrant
	require.NoError(t, db.First(&savedGrant, grant.ID).Error)
	assert.Equal(t, grant, savedGrant, "historical grants are not rewritten or re-awarded")
	duplicate := lots[0]
	duplicate.ID = 0
	require.Error(t, db.Create(&duplicate).Error, "opening source must remain unique")
	require.Error(t, db.Create(&XingyaCheckinGrant{UserID: 1, CheckinDate: grant.CheckinDate, QuotaAwarded: 1}).Error, "daily uniqueness must survive upgrade")
	allocation := XingyaCheckinAllocation{RequestID: "composite-fixture", LotID: lots[0].ID, Amount: 5, Reserved: 5}
	require.NoError(t, db.Create(&allocation).Error)
	require.Error(t, db.Create(&allocation).Error)
	allocation.LotID = lots[1].ID
	require.NoError(t, db.Create(&allocation).Error, "one request can allocate multiple lots")
	assert.True(t, db.Migrator().HasIndex(&XingyaCheckinCreditLot{}, "idx_checkin_lot_user_expiry"))
}

func TestXingyaCheckinExpiryFreshPostgres(t *testing.T) {
	db := checkinMigrationPostgres(t)
	oldReady := xingyaTablesReady.Load()
	t.Cleanup(func() { xingyaTablesReady.Store(oldReady) })
	require.NoError(t, db.AutoMigrate(&Option{}))
	for range 2 {
		require.NoError(t, InitXingyaTables(db))
	}
	var lots int64
	require.NoError(t, db.Model(&XingyaCheckinCreditLot{}).Count(&lots).Error)
	assert.Zero(t, lots, "a fresh database does not invent an opening reward")
	var marker Option
	require.NoError(t, db.First(&marker, "key = ?", checkinExpiryMigration).Error)
	started, err := strconv.ParseInt(marker.Value, 10, 64)
	require.NoError(t, err)
	assert.Positive(t, started)
}
