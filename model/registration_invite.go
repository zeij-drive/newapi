package model

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	RegistrationInviteStatusEnabled  = 1
	RegistrationInviteStatusClaimed  = 2
	RegistrationInviteStatusUsed     = 3
	RegistrationInviteStatusDisabled = 4
)

var (
	ErrRegistrationInviteRequired      = errors.New("registration invite code is required")
	ErrRegistrationInviteInvalid       = errors.New("registration invite code is invalid")
	ErrRegistrationInviteExpired       = errors.New("registration invite code has expired")
	ErrRegistrationInviteUsed          = errors.New("registration invite code has already been used")
	ErrRegistrationInviteDisabled      = errors.New("registration invite code is disabled")
	ErrRegistrationInviteNotConfigured = errors.New("registration invite encryption is not configured")
)

// RegistrationInvite stores a one-time registration credential. The plaintext
// code is only returned when it is created or claimed by the QQ bot; lookup and
// validation use CodeHash so the code never appears in normal database output.
type RegistrationInvite struct {
	Id                  int64   `json:"id" gorm:"primaryKey"`
	CodeHash            string  `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	EncryptedCode       string  `json:"-" gorm:"type:text;not null"`
	CodePrefix          string  `json:"code_prefix" gorm:"type:varchar(16);not null"`
	Status              int     `json:"status" gorm:"type:int;not null;index"`
	CreatedBy           int     `json:"created_by" gorm:"type:int;not null;index"`
	ClaimedQQOpenIDHash *string `json:"-" gorm:"type:char(64);uniqueIndex"`
	ClaimedAt           int64   `json:"claimed_at,omitempty"`
	UsedUserID          int     `json:"used_user_id,omitempty" gorm:"type:int;index"`
	UsedAt              int64   `json:"used_at,omitempty"`
	ExpiresAt           int64   `json:"expires_at,omitempty" gorm:"type:bigint;index"`
	CreatedAt           int64   `json:"created_at" gorm:"autoCreateTime;column:created_at"`
}

func normalizeRegistrationInviteCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

func registrationInviteSecret() string {
	return common.ConfigEncryptionSecret
}

func registrationInviteHash(code string) string {
	return common.GenerateHMACWithKey([]byte("registration-invite-v1:"+registrationInviteSecret()), normalizeRegistrationInviteCode(code))
}

func HashRegistrationInviteCode(code string) string {
	return registrationInviteHash(code)
}

func registrationQQOpenIDHash(openID string) string {
	return common.GenerateHMACWithKey([]byte("registration-qq-open-id-v1:"+registrationInviteSecret()), strings.TrimSpace(openID))
}

func registrationInviteUsable(invite *RegistrationInvite, now int64) error {
	switch invite.Status {
	case RegistrationInviteStatusEnabled, RegistrationInviteStatusClaimed:
		// continue
	case RegistrationInviteStatusUsed:
		return ErrRegistrationInviteUsed
	default:
		return ErrRegistrationInviteDisabled
	}
	if invite.ExpiresAt != 0 && invite.ExpiresAt <= now {
		return ErrRegistrationInviteExpired
	}
	return nil
}

func registrationInviteExpiration(expiry int64, now time.Time) error {
	if expiry < 0 {
		return errors.New("invite expiration must not be negative")
	}
	if expiry != 0 && expiry <= now.Unix() {
		return ErrRegistrationInviteExpired
	}
	return nil
}

func (invite *RegistrationInvite) plaintextCode() (string, error) {
	if invite.EncryptedCode == "" {
		return "", ErrRegistrationInviteInvalid
	}
	return common.DecryptWithCryptoSecret(invite.EncryptedCode)
}

// CreateRegistrationInvites creates count independent, cryptographically
// random invite codes. The returned plaintext values are intentionally not
// persisted in any log or model JSON representation.
func CreateRegistrationInvites(count, createdBy int, expiresAt int64) ([]string, error) {
	if count < 1 || count > 1000 {
		return nil, errors.New("invite count must be between 1 and 1000")
	}
	if expiresAt < 0 {
		return nil, errors.New("invite expiration must not be negative")
	}
	if err := registrationInviteExpiration(expiresAt, time.Now()); err != nil {
		return nil, err
	}
	if registrationInviteSecret() == "" || common.ConfigEncryptionSecret == "" {
		return nil, ErrRegistrationInviteNotConfigured
	}
	keys := make([]string, 0, count)
	err := DB.Transaction(func(tx *gorm.DB) error {
		for range count {
			raw, err := common.GenerateRandomCharsKey(24)
			if err != nil {
				return err
			}
			code := "SAPI-" + strings.ToUpper(raw)
			encrypted, err := common.EncryptWithCryptoSecret(code)
			if err != nil {
				return err
			}
			invite := &RegistrationInvite{
				CodeHash:      registrationInviteHash(code),
				EncryptedCode: encrypted,
				CodePrefix:    code[:min(len(code), 12)],
				Status:        RegistrationInviteStatusEnabled,
				CreatedBy:     createdBy,
				ExpiresAt:     expiresAt,
			}
			if err := tx.Create(invite).Error; err != nil {
				return err
			}
			keys = append(keys, code)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// ConsumeRegistrationInviteWithTx atomically changes an invite to used. It is
// called in the same transaction that creates the user, so a failed user
// insert cannot consume a code.
func ConsumeRegistrationInviteWithTx(tx *gorm.DB, code string, userID int) error {
	code = normalizeRegistrationInviteCode(code)
	if code == "" {
		return ErrRegistrationInviteRequired
	}
	return ConsumeRegistrationInviteHashWithTx(tx, registrationInviteHash(code), userID)
}

// ConsumeRegistrationInviteHashWithTx consumes a code using its keyed digest;
// OAuth state stores only the digest so a raw registration code never enters
// the short-lived authentication-flow payload.
func ConsumeRegistrationInviteHashWithTx(tx *gorm.DB, codeHash string, userID int) error {
	if codeHash == "" {
		return ErrRegistrationInviteRequired
	}
	if userID <= 0 {
		return ErrRegistrationInviteInvalid
	}
	var invite RegistrationInvite
	if err := lockForUpdate(tx).Where("code_hash = ?", codeHash).First(&invite).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRegistrationInviteInvalid
		}
		return err
	}
	if err := registrationInviteUsable(&invite, time.Now().Unix()); err != nil {
		return err
	}
	result := tx.Model(&RegistrationInvite{}).
		Where("id = ? AND status IN ?", invite.Id, []int{RegistrationInviteStatusEnabled, RegistrationInviteStatusClaimed}).
		Updates(map[string]any{
			"status":       RegistrationInviteStatusUsed,
			"used_user_id": userID,
			"used_at":      time.Now().Unix(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRegistrationInviteUsed
	}
	return nil
}

// ClaimRegistrationInviteForQQ assigns one available invite to one QQ open id.
// The unique nullable column prevents duplicate claims for the same QQ user,
// while the compare-and-swap update also protects SQLite's lock-free reads.
func ClaimRegistrationInviteForQQ(openID string) (string, error) {
	openID = strings.TrimSpace(openID)
	if openID == "" || len(openID) > 128 || registrationInviteSecret() == "" {
		return "", ErrRegistrationInviteInvalid
	}
	openIDHash := registrationQQOpenIDHash(openID)
	var code string
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing RegistrationInvite
		if err := lockForUpdate(tx).Where("claimed_qq_open_id_hash = ?", openIDHash).First(&existing).Error; err == nil {
			if err := registrationInviteUsable(&existing, time.Now().Unix()); err != nil {
				return err
			}
			var decryptErr error
			code, decryptErr = existing.plaintextCode()
			return decryptErr
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var invite RegistrationInvite
		query := lockForUpdate(tx).
			Where("status = ? AND claimed_qq_open_id_hash IS NULL", RegistrationInviteStatusEnabled).
			Where("(expires_at = 0 OR expires_at >= ?)", time.Now().Unix()).
			Order("id ASC")
		if err := query.First(&invite).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRegistrationInviteInvalid
			}
			return err
		}
		result := tx.Model(&RegistrationInvite{}).
			Where("id = ? AND status = ? AND claimed_qq_open_id_hash IS NULL", invite.Id, RegistrationInviteStatusEnabled).
			Updates(map[string]any{
				"status":                  RegistrationInviteStatusClaimed,
				"claimed_qq_open_id_hash": openIDHash,
				"claimed_at":              time.Now().Unix(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRegistrationInviteInvalid
		}
		plaintext, decryptErr := invite.plaintextCode()
		code = plaintext
		return decryptErr
	})
	if err != nil {
		// If a duplicate callback raced the first claim, return the same invite
		// rather than allocating another one to the same QQ user.
		var existing RegistrationInvite
		if lookupErr := DB.Where("claimed_qq_open_id_hash = ?", openIDHash).First(&existing).Error; lookupErr == nil {
			if usableErr := registrationInviteUsable(&existing, time.Now().Unix()); usableErr == nil {
				return existing.plaintextCode()
			}
		}
	}
	return code, err
}

func ListRegistrationInvites(page, pageSize int) ([]*RegistrationInvite, int64, error) {
	page = max(page, 1)
	pageSize = min(max(pageSize, 1), 100)
	var invites []*RegistrationInvite
	query := DB.Model(&RegistrationInvite{})
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&invites).Error; err != nil {
		return nil, 0, err
	}
	return invites, total, nil
}

func SetRegistrationInviteStatus(id int64, status int) error {
	if status != RegistrationInviteStatusEnabled && status != RegistrationInviteStatusDisabled {
		return errors.New("invalid invite status")
	}
	result := DB.Model(&RegistrationInvite{}).
		Where("id = ? AND status NOT IN ?", id, []int{RegistrationInviteStatusUsed}).
		Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// VerifyRegistrationInvite checks a code without consuming it. It is useful
// for API callers that want to provide an immediate validation response.
func VerifyRegistrationInvite(code string) error {
	code = normalizeRegistrationInviteCode(code)
	if code == "" {
		return ErrRegistrationInviteRequired
	}
	var invite RegistrationInvite
	if err := DB.Where("code_hash = ?", registrationInviteHash(code)).First(&invite).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRegistrationInviteInvalid
		}
		return err
	}
	return registrationInviteUsable(&invite, time.Now().Unix())
}
