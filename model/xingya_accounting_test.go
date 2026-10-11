package model

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// External DSNs must point to disposable databases. Reuse the behavior regressions
// against real engines, with a separate log database and a single-connection pool.
func TestXingyaAccountingDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		name           common.DatabaseType
		dsnEnv, logEnv string
	}{
		{common.DatabaseTypeSQLite, "", ""},
		{common.DatabaseTypeMySQL, "TEST_XINGYA_MYSQL_DSN", "TEST_XINGYA_MYSQL_LOG_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_XINGYA_POSTGRES_DSN", "TEST_XINGYA_POSTGRES_LOG_DSN"},
	} {
		t.Run(string(dialect.name), func(t *testing.T) {
			var mainDriver, logDriver gorm.Dialector = sqlite.Open(":memory:"), sqlite.Open(":memory:")
			if dialect.dsnEnv != "" {
				mainDSN, logDSN := os.Getenv(dialect.dsnEnv), os.Getenv(dialect.logEnv)
				if mainDSN == "" || logDSN == "" {
					t.Skip("isolated main and log DSNs are not configured")
				}
				if dialect.name == common.DatabaseTypeMySQL {
					mainDriver, logDriver = mysql.Open(mainDSN), mysql.Open(logDSN)
				} else {
					mainDriver = postgres.New(postgres.Config{DSN: mainDSN, PreferSimpleProtocol: true})
					logDriver = postgres.New(postgres.Config{DSN: logDSN, PreferSimpleProtocol: true})
				}
			}
			mainDB, err := gorm.Open(mainDriver, &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
			require.NoError(t, err)
			logDB, err := gorm.Open(logDriver, &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
			require.NoError(t, err)
			for _, db := range []*gorm.DB{mainDB, logDB} {
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
				t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			}
			oldDB, oldLogDB := DB, LOG_DB
			oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			oldReady := xingyaTablesReady.Load()
			DB, LOG_DB = mainDB, logDB
			common.SetDatabaseTypes(dialect.name, dialect.name)
			initCol()
			t.Cleanup(func() {
				DB, LOG_DB = oldDB, oldLogDB
				common.SetDatabaseTypes(oldMainType, oldLogType)
				xingyaTablesReady.Store(oldReady)
				initCol()
			})
			require.NoError(t, mainDB.AutoMigrate(&User{}, &Token{}, &Channel{}, &Log{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
			require.NoError(t, logDB.AutoMigrate(&Log{}))
			require.NoError(t, InitXingyaTables(mainDB))
			require.NoError(t, InitXingyaTables(mainDB))
			versionQuery := "SELECT version()"
			if dialect.name == common.DatabaseTypeSQLite {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, mainDB.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database: %s; separate log database", version)
			for _, regression := range []struct {
				name string
				run  func(*testing.T)
			}{
				{"raised gate", TestRaisedInviteSpendGateCanStillBeReached},
				{"legacy eligible recovery", TestLegacyEligibleInviteWithZeroSpendCanRecover},
				{"scheduler batch progress", TestAutoGrantSkipsLegacyRowsBelowCurrentSpendGate},
				{"manual grant idempotency", TestManualClaimCreditsInviterWalletAndIsIdempotent},
				{"legacy refund revocation", TestRefundExcludesLegacyCallFromUnpaidInviteProgressOnce},
				{"excluded calls and paid history", TestRefundDoesNotRemoveExcludedCallsOrPaidRewards},
				{"failed refund rollback", TestFailedRefundRollsBackInviteProgress},
				{"wallet refund idempotency", TestDoSelfRefundWalletCreditsOnce},
				{"subscription cycle", TestDoSelfRefundSubscriptionChecksOriginalConsumptionCycle},
				{"refund daily count", TestDoSelfRefundEnforcesDailyCountLimitInsideTransaction},
				{"refund daily quota", TestDoSelfRefundEnforcesDailyQuotaLimit},
				{"browser families", TestDistinctBrowserFamiliesKeepSeparateTrialFingerprints},
				{"subnet concurrent reservations", TestConcurrentSubnetTrialsStayWithinBudget},
				{"zero gift budget", TestZeroQuotaRegistrationsDoNotSpendSubnetBudget},
				{"client cancellation", TestCancelledSignupContextCannotBypassPendingSubnetBudget},
				{"populated legacy registration migration", TestMigrationAddsTheNormalizedDigestToAPopulatedLegacyTable},
			} {
				t.Run(regression.name, regression.run)
			}
			if dialect.name != common.DatabaseTypeSQLite {
				sqlDB, err := mainDB.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(8)
				t.Run("concurrent daily refund cap", TestDoSelfRefundDailyLimitSurvivesConcurrentRequests)
				t.Run("concurrent claim and refund", TestConcurrentInviteClaimAndRefundKeepAccountingConsistent)
				sqlDB.SetMaxOpenConns(1)
			}
			t.Run("startup preserves existing ledger", func(t *testing.T) {
				setupInviteRewardTestState(t, 8000)
				inviter := createReserveTestUser(t, 100000)
				invitee := createReserveTestUser(t, 100000)
				reward := registerInviteRewardFixture(t, inviter.Id, invitee.Id)
				RecordSuccessfulBillableCall(invitee.Id, 50000)
				before := getInviteRewardFromDB(t, reward.Id)
				require.NoError(t, InitXingyaTables(mainDB))
				require.NoError(t, InitXingyaTables(mainDB))
				assert.Equal(t, before, getInviteRewardFromDB(t, reward.Id))
				duplicate := before
				duplicate.Id = 0
				require.Error(t, mainDB.Create(&duplicate).Error, "the unique invitee boundary must survive restarts")
			})
		})
	}
}
