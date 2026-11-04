package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	botmessage "github.com/tencent-connect/botgo/dto/message"
)

const ratioNotificationPathSuffix = "/ratio-notification"

type ratioChange struct {
	Name     string  `json:"name"`
	OldRatio float64 `json:"old_ratio"`
	NewRatio float64 `json:"new_ratio"`
}

type ratioNotificationPayload struct {
	Groups []ratioChangePayload `json:"groups"`
}

type ratioChangePayload struct {
	Name     *string  `json:"name"`
	OldRatio *float64 `json:"old_ratio"`
	NewRatio *float64 `json:"new_ratio"`
}

type ratioNotificationStore struct {
	mu        sync.Mutex
	path      string
	noncePath string
	groups    map[string]struct{}
	nonces    map[string]time.Time
}

func newRatioNotificationStore(path string) (*ratioNotificationStore, error) {
	store := &ratioNotificationStore{
		path:   strings.TrimSpace(path),
		groups: make(map[string]struct{}),
		nonces: make(map[string]time.Time),
	}
	if store.path == "" {
		return store, nil
	}
	store.noncePath = store.path + ".nonces"
	data, err := os.ReadFile(store.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		var groupIDs []string
		if err := json.Unmarshal(data, &groupIDs); err != nil {
			return nil, fmt.Errorf("invalid ratio notification state: %w", err)
		}
		for _, groupID := range groupIDs {
			if groupID = strings.TrimSpace(groupID); groupID != "" && len(groupID) <= 128 {
				store.groups[groupID] = struct{}{}
			}
		}
	}
	nonceData, err := os.ReadFile(store.noncePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		var persistedNonces map[string]int64
		if err := json.Unmarshal(nonceData, &persistedNonces); err != nil {
			return nil, fmt.Errorf("invalid ratio notification replay state: %w", err)
		}
		for nonce, expiresAt := range persistedNonces {
			if len(nonce) == 32 && expiresAt > time.Now().Unix() {
				store.nonces[nonce] = time.Unix(expiresAt, 0)
			}
		}
	}
	return store, nil
}

func (store *ratioNotificationStore) setEnabled(groupID string, enabled bool) error {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" || len(groupID) > 128 {
		return errors.New("invalid QQ group identifier")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	_, wasEnabled := store.groups[groupID]
	if enabled {
		store.groups[groupID] = struct{}{}
	} else {
		delete(store.groups, groupID)
	}
	if err := store.persistLocked(); err != nil {
		if wasEnabled {
			store.groups[groupID] = struct{}{}
		} else {
			delete(store.groups, groupID)
		}
		return err
	}
	return nil
}

func (store *ratioNotificationStore) enabledGroups() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	groups := make([]string, 0, len(store.groups))
	for groupID := range store.groups {
		groups = append(groups, groupID)
	}
	sort.Strings(groups)
	return groups
}

func (store *ratioNotificationStore) reserveNonce(nonce string, now time.Time) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	for value, expiresAt := range store.nonces {
		if !expiresAt.After(now) {
			delete(store.nonces, value)
		}
	}
	if _, exists := store.nonces[nonce]; exists {
		return false
	}
	if len(store.nonces) >= 8192 {
		return false
	}
	store.nonces[nonce] = now.Add(10 * time.Minute)
	if err := store.persistNoncesLocked(); err != nil {
		delete(store.nonces, nonce)
		return false
	}
	return true
}

func (store *ratioNotificationStore) persistLocked() error {
	if store.path == "" {
		return nil
	}
	groups := make([]string, 0, len(store.groups))
	for groupID := range store.groups {
		groups = append(groups, groupID)
	}
	sort.Strings(groups)
	data, err := json.Marshal(groups)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0700); err != nil {
		return err
	}
	temporary := store.path + ".tmp"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(temporary, store.path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (store *ratioNotificationStore) persistNoncesLocked() error {
	if store.noncePath == "" {
		return nil
	}
	nonces := make(map[string]int64, len(store.nonces))
	for nonce, expiresAt := range store.nonces {
		nonces[nonce] = expiresAt.Unix()
	}
	data, err := json.Marshal(nonces)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(store.noncePath), 0700); err != nil {
		return err
	}
	temporary := store.noncePath + ".tmp"
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(temporary, store.noncePath); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func ratioNotificationSignature(secret, method, path, timestamp, nonce string, body []byte) string {
	digest := sha256.Sum256(body)
	canonical := strings.Join([]string{method, path, timestamp, nonce, hex.EncodeToString(digest[:])}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func decodeRatioNotificationPayload(data []byte) ([]ratioChange, error) {
	var payload ratioNotificationPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, errors.New("invalid group-ratio notification payload")
	}
	if len(payload.Groups) == 0 || len(payload.Groups) > 100 {
		return nil, errors.New("invalid group-ratio notification payload")
	}
	changes := make([]ratioChange, 0, len(payload.Groups))
	for _, change := range payload.Groups {
		if change.Name == nil || change.OldRatio == nil || change.NewRatio == nil {
			return nil, errors.New("invalid group-ratio notification payload")
		}
		changes = append(changes, ratioChange{Name: *change.Name, OldRatio: *change.OldRatio, NewRatio: *change.NewRatio})
	}
	if err := validateRatioChanges(changes); err != nil {
		return nil, err
	}
	return changes, nil
}

func validateRatioChanges(changes []ratioChange) error {
	if len(changes) == 0 || len(changes) > 100 {
		return errors.New("invalid group-ratio notification payload")
	}
	for _, change := range changes {
		if strings.TrimSpace(change.Name) == "" || len(change.Name) > 128 || strings.ContainsAny(change.Name, "\r\n\x00") || math.IsNaN(change.OldRatio) || math.IsInf(change.OldRatio, 0) || math.IsNaN(change.NewRatio) || math.IsInf(change.NewRatio, 0) || change.OldRatio < 0 || change.NewRatio < 0 || change.OldRatio == change.NewRatio {
			return errors.New("invalid group-ratio notification payload")
		}
	}
	return nil
}

func formatRatioValue(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64) + "x"
}

func ratioNotificationText(changes []ratioChange) string {
	lines := make([]string, 0, len(changes)+1)
	lines = append(lines, botmessage.MentionAllUser()+" 分组倍率变更通知")
	for _, change := range changes {
		name := change.Name
		if !strings.HasSuffix(name, "分组") {
			name += "分组"
		}
		direction := "降价"
		if change.NewRatio > change.OldRatio {
			direction = "涨价"
		}
		lines = append(lines, fmt.Sprintf("%s%s，%s->%s", name, direction, formatRatioValue(change.OldRatio), formatRatioValue(change.NewRatio)))
	}
	return strings.Join(lines, "\n")
}

func splitNonEmpty(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\r' || r == '\n' })
	values := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			values = append(values, field)
		}
	}
	return values
}
