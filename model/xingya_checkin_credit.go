package model

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const CheckinCreditLifetime = 30 * 24 * time.Hour
const checkinExpiryMigration = "xingya_migration.checkin_credit_expiry_started_at"

// Lots retain both the original expiry and the destinations of every quota.
// Opening lots represent the pre-upgrade wallet, not a new daily award.
type XingyaCheckinCreditLot struct {
	ID        int    `json:"-" gorm:"primaryKey"`
	UserID    int    `json:"-" gorm:"not null;index:idx_checkin_lot_user_expiry,priority:1"`
	GrantID   int    `json:"-" gorm:"index"`
	SourceKey string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	Source    string `json:"source" gorm:"type:varchar(16);not null"`
	Granted   int    `json:"granted" gorm:"type:bigint;not null"`
	Available int    `json:"available" gorm:"type:bigint;not null"`
	Reserved  int    `json:"reserved" gorm:"type:bigint;not null"`
	Consumed  int    `json:"consumed" gorm:"type:bigint;not null"`
	Expired   int    `json:"expired" gorm:"type:bigint;not null"`
	ExpiresAt int64  `json:"expires_at" gorm:"type:bigint;not null;index:idx_checkin_lot_user_expiry,priority:2"`
	CreatedAt int64  `json:"-" gorm:"type:bigint;not null"`
}

func (XingyaCheckinCreditLot) TableName() string { return "xingya_checkin_credit_lots" }

type XingyaCheckinAllocation struct {
	RequestID string `gorm:"primaryKey;type:varchar(191)"`
	LotID     int    `gorm:"primaryKey;autoIncrement:false"`
	Amount    int    `gorm:"type:bigint;not null"`
	Reserved  int    `gorm:"type:bigint;not null"`
	Consumed  int    `gorm:"type:bigint;not null"`
	Returned  int    `gorm:"type:bigint;not null"`
	Expired   int    `gorm:"type:bigint;not null"`
}

func (XingyaCheckinAllocation) TableName() string { return "xingya_checkin_allocations" }

type XingyaCheckinExpiry struct {
	ExpiringSoon      int                     `json:"expiring_soon"`
	NextExpiresAt     int64                   `json:"next_expires_at"`
	NextExpiringQuota int                     `json:"next_expiring_quota"`
	Opening           *XingyaCheckinCreditLot `json:"opening,omitempty"`
}

type XingyaCheckinBalance struct {
	Wallet XingyaCheckinWallet `json:"wallet"`
	Expiry XingyaCheckinExpiry `json:"expiry"`
}

// The same balance reader serves browser and key APIs; no cache can revive an
// expired amount. An explicit timestamp is used only by deterministic model tests.
func GetXingyaCheckinBalance(userID int) (XingyaCheckinBalance, error) {
	return getXingyaCheckinBalanceAt(userID, 0)
}

func getXingyaCheckinBalanceAt(userID int, at int64) (XingyaCheckinBalance, error) {
	var result XingyaCheckinBalance
	if userID <= 0 {
		return result, errors.New("签到用户无效")
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		wallet, err := lockCheckinWallet(tx, userID)
		if err != nil {
			return err
		}
		if at == 0 {
			at = common.GetTimestamp()
		}
		if err = expireCheckinCredit(tx, wallet, at); err != nil {
			return err
		}
		var lots []XingyaCheckinCreditLot
		if err = tx.Where("user_id = ? AND available > 0", userID).Order("expires_at, id").Find(&lots).Error; err != nil {
			return err
		}
		available := 0
		for _, lot := range lots {
			if !validCheckinLot(lot) || lot.Available > wallet.Available-available {
				return errors.New("签到批次对账不一致")
			}
			available += lot.Available
			if lot.ExpiresAt <= at+7*24*60*60 {
				result.Expiry.ExpiringSoon += lot.Available
			}
			if result.Expiry.NextExpiresAt == 0 {
				result.Expiry.NextExpiresAt = lot.ExpiresAt
			}
			if lot.ExpiresAt == result.Expiry.NextExpiresAt {
				result.Expiry.NextExpiringQuota += lot.Available
			}
		}
		if available != wallet.Available {
			return errors.New("签到批次对账不一致")
		}
		var opening XingyaCheckinCreditLot
		if err = tx.Where("user_id = ? AND source = ?", userID, "opening").Take(&opening).Error; err == nil {
			result.Expiry.Opening = &opening
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		result.Wallet = *wallet
		return nil
	})
	return result, err
}

func validCheckinLot(lot XingyaCheckinCreditLot) bool {
	return lot.Granted >= 0 && lot.Granted <= common.MaxWalletQuota && lot.Available >= 0 && lot.Reserved >= 0 && lot.Consumed >= 0 && lot.Expired >= 0 && lot.ExpiresAt > 0 &&
		lot.Available <= lot.Granted && lot.Reserved <= lot.Granted-lot.Available && lot.Consumed <= lot.Granted-lot.Available-lot.Reserved && lot.Expired == lot.Granted-lot.Available-lot.Reserved-lot.Consumed
}

// The caller holds the wallet lock. Only available funds expire; allocations
// already reserved remain attached to their original lot until finalization.
func expireCheckinCredit(tx *gorm.DB, wallet *XingyaCheckinWallet, now int64) error {
	var lots []XingyaCheckinCreditLot
	if err := lockForUpdate(tx).Where("user_id = ? AND expires_at <= ? AND available > 0", wallet.UserID, now).Order("expires_at, id").Find(&lots).Error; err != nil {
		return err
	}
	for _, lot := range lots {
		if !validCheckinLot(lot) || lot.Available > wallet.Available {
			return errors.New("签到过期金额对账不一致")
		}
		wallet.Available -= lot.Available
		wallet.Expired += lot.Available
		lot.Expired += lot.Available
		lot.Available = 0
		if err := tx.Save(&lot).Error; err != nil {
			return err
		}
	}
	if len(lots) > 0 {
		return tx.Save(wallet).Error
	}
	return nil
}

// Batch migration is committed once under the migration row lock. Restarting
// cannot refresh the grace period. Any old in-flight spend aborts the opening.
func migrateCheckinCreditExpiry(db *gorm.DB, at int64) error {
	if !db.Migrator().HasTable(&Option{}) {
		// Core startup creates options before this initializer. A table-only
		// initialization cannot enable claims or spending without those settings.
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		marker := Option{Key: checkinExpiryMigration, Value: "0"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("key = ?", marker.Key).Take(&marker).Error; err != nil {
			return err
		}
		if marker.Value != "0" {
			started, err := strconv.ParseInt(marker.Value, 10, 64)
			if err != nil || started <= 0 {
				return errors.New("签到有效期迁移标记无效")
			}
			return nil
		}
		var pending int64
		if err := tx.Model(&XingyaCheckinSpend{}).Where("state = ?", "reserved").Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return errors.New("签到有效期升级前须完成或核对旧预留请求；暂停签到并使用原版本处理后重试")
		}
		if at == 0 {
			at = common.GetTimestamp()
		}
		lastUser := 0
		for {
			var wallets []XingyaCheckinWallet
			if err := lockForUpdate(tx).Where("user_id > ?", lastUser).Order("user_id").Limit(200).Find(&wallets).Error; err != nil {
				return err
			}
			for _, wallet := range wallets {
				if wallet.Reserved != 0 || !validCheckinWallet(wallet) {
					return errors.New("签到有效期升级前须核对旧钱包余额与预留")
				}
				if wallet.Granted > 0 {
					lot := XingyaCheckinCreditLot{UserID: wallet.UserID, Source: "opening", SourceKey: fmt.Sprintf("opening:%d", wallet.UserID), Granted: wallet.Granted, Available: wallet.Available, Consumed: wallet.Consumed, Expired: wallet.Expired, CreatedAt: at, ExpiresAt: at + int64(CheckinCreditLifetime/time.Second)}
					if err := tx.Create(&lot).Error; err != nil {
						return err
					}
				}
				lastUser = wallet.UserID
			}
			if len(wallets) < 200 {
				break
			}
		}
		marker.Value = strconv.FormatInt(at, 10)
		return tx.Save(&marker).Error
	})
}

// Reservation decreases release the newest allocations first, preserving the
// earliest expiry for consumption. Final charges use the reverse order.
func validCheckinAllocation(a XingyaCheckinAllocation) bool {
	return a.Amount >= 0 && a.Amount <= common.MaxWalletQuota && a.Reserved >= 0 && a.Consumed >= 0 && a.Returned >= 0 && a.Expired >= 0 &&
		a.Reserved <= a.Amount && a.Consumed <= a.Amount-a.Reserved && a.Returned <= a.Amount-a.Reserved-a.Consumed && a.Expired == a.Amount-a.Reserved-a.Consumed-a.Returned
}

func checkinAllocatedLots(tx *gorm.DB, wallet *XingyaCheckinWallet, spend *XingyaCheckinSpend) ([]XingyaCheckinAllocation, map[int]*XingyaCheckinCreditLot, error) {
	if spend.Reserved < 0 || spend.Reserved > common.MaxQuota {
		return nil, nil, errors.New("签到预留金额无效")
	}
	var allocations []XingyaCheckinAllocation
	if err := tx.Where("request_id = ? AND reserved > 0", spend.RequestID).Find(&allocations).Error; err != nil {
		return nil, nil, err
	}
	lots := make(map[int]*XingyaCheckinCreditLot, len(allocations))
	reserved := 0
	for _, allocation := range allocations {
		if !validCheckinAllocation(allocation) || allocation.Reserved > spend.Reserved-reserved {
			return nil, nil, errors.New("签到请求分配对账不一致")
		}
		var lot XingyaCheckinCreditLot
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", allocation.LotID, wallet.UserID).Take(&lot).Error; err != nil {
			return nil, nil, err
		}
		if !validCheckinLot(lot) || lot.Reserved < allocation.Reserved {
			return nil, nil, errors.New("签到批次预留对账不一致")
		}
		lots[lot.ID] = &lot
		reserved += allocation.Reserved
	}
	if reserved != spend.Reserved || reserved > wallet.Reserved {
		return nil, nil, errors.New("签到预留分配缺失，请联系管理员")
	}
	slices.SortFunc(allocations, func(a, b XingyaCheckinAllocation) int {
		left, right := lots[a.LotID], lots[b.LotID]
		if left.ExpiresAt < right.ExpiresAt {
			return -1
		}
		if left.ExpiresAt > right.ExpiresAt {
			return 1
		}
		return cmp.Compare(a.LotID, b.LotID)
	})
	return allocations, lots, nil
}

func releaseCheckinAllocation(wallet *XingyaCheckinWallet, lot *XingyaCheckinCreditLot, allocation *XingyaCheckinAllocation, amount int, now int64) {
	wallet.Reserved -= amount
	lot.Reserved -= amount
	allocation.Reserved -= amount
	if lot.ExpiresAt <= now {
		wallet.Expired += amount
		lot.Expired += amount
		allocation.Expired += amount
	} else {
		wallet.Available += amount
		lot.Available += amount
		allocation.Returned += amount
	}
}

func adjustCheckinAllocations(tx *gorm.DB, wallet *XingyaCheckinWallet, spend *XingyaCheckinSpend, target int, now int64) error {
	// A reduction never needs new funds, and must remain possible after expiry
	// or a review flag so failed token reservations can still roll back safely.
	allocations, lots, err := checkinAllocatedLots(tx, wallet, spend)
	if err != nil {
		return err
	}
	if target < spend.Reserved {
		release := spend.Reserved - target
		for i := len(allocations) - 1; i >= 0 && release > 0; i-- {
			allocation := &allocations[i]
			amount := min(release, allocation.Reserved)
			lot := lots[allocation.LotID]
			releaseCheckinAllocation(wallet, lot, allocation, amount, now)
			if err = tx.Save(lot).Error; err != nil {
				return err
			}
			if err = tx.Save(allocation).Error; err != nil {
				return err
			}
			release -= amount
		}
		return nil
	}
	remaining := target - spend.Reserved
	var availableLots []XingyaCheckinCreditLot
	if remaining > 0 {
		if err = lockForUpdate(tx).Where("user_id = ? AND available > 0 AND expires_at > ?", wallet.UserID, now).Order("expires_at, id").Find(&availableLots).Error; err != nil {
			return err
		}
	}
	for _, lot := range availableLots {
		if !validCheckinLot(lot) {
			return errors.New("签到批次对账不一致")
		}
		amount := min(remaining, lot.Available)
		var allocation XingyaCheckinAllocation
		err = tx.Where("request_id = ? AND lot_id = ?", spend.RequestID, lot.ID).Take(&allocation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			allocation.RequestID, allocation.LotID = spend.RequestID, lot.ID
		} else if err != nil {
			return err
		}
		if !validCheckinAllocation(allocation) {
			return errors.New("签到请求分配对账不一致")
		}
		if allocation.Amount > common.MaxWalletQuota-amount {
			return errors.New("签到请求分配超过上限")
		}
		allocation.Amount += amount
		allocation.Reserved += amount
		wallet.Available -= amount
		wallet.Reserved += amount
		lot.Available -= amount
		lot.Reserved += amount
		if err = tx.Save(&lot).Error; err != nil {
			return err
		}
		if err = tx.Save(&allocation).Error; err != nil {
			return err
		}
		remaining -= amount
		if remaining == 0 {
			break
		}
	}
	if remaining > 0 {
		return errors.New("签到可用余额缺少批次记录")
	}
	return nil
}

func settleCheckinAllocations(tx *gorm.DB, wallet *XingyaCheckinWallet, spend *XingyaCheckinSpend, paid int, now int64) error {
	allocations, lots, err := checkinAllocatedLots(tx, wallet, spend)
	if err != nil {
		return err
	}
	for i := range allocations {
		allocation := &allocations[i]
		lot := lots[allocation.LotID]
		charge := min(paid, allocation.Reserved)
		paid -= charge
		wallet.Reserved -= charge
		wallet.Consumed += charge
		lot.Reserved -= charge
		lot.Consumed += charge
		allocation.Reserved -= charge
		allocation.Consumed += charge
		releaseCheckinAllocation(wallet, lot, allocation, allocation.Reserved, now)
		if err = tx.Save(lot).Error; err != nil {
			return err
		}
		if err = tx.Save(allocation).Error; err != nil {
			return err
		}
	}
	return nil
}
