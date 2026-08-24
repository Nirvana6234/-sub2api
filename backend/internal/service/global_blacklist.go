package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	GlobalBlacklistKindAccount = "account"
	GlobalBlacklistKindIP      = "ip"
)

// GlobalBlacklistEntry is an administrator-managed deny rule. Account values
// are authenticated user IDs; IP values may be a single address or CIDR.
type GlobalBlacklistEntry struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Value     string     `json:"value"`
	Reason    string     `json:"reason,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Enabled   bool       `json:"enabled"`
	CreatedAt time.Time  `json:"created_at"`
}

type cachedGlobalBlacklist struct {
	Entries  []GlobalBlacklistEntry
	ExpiresAt time.Time
}

var (
	errGlobalBlacklistInvalidKind  = fmt.Errorf("blacklist kind must be account or ip")
	errGlobalBlacklistInvalidValue = fmt.Errorf("blacklist value is invalid")
)

// GlobalBlacklistSnapshot returns the current rules. A short cache keeps the
// request path independent of the database while updates take effect locally
// immediately.
func (s *SettingService) GlobalBlacklistSnapshot(ctx context.Context) ([]GlobalBlacklistEntry, error) {
	if s == nil || s.settingRepo == nil {
		return nil, nil
	}
	if value := s.globalBlacklistCache.Load(); value != nil {
		cached := value.(*cachedGlobalBlacklist)
		if time.Now().Before(cached.ExpiresAt) {
			return cloneGlobalBlacklistEntries(cached.Entries), nil
		}
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyGlobalBlacklist)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := decodeGlobalBlacklist(raw)
	if err != nil {
		return nil, err
	}
	s.globalBlacklistCache.Store(&cachedGlobalBlacklist{Entries: entries, ExpiresAt: time.Now().Add(5 * time.Second)})
	return cloneGlobalBlacklistEntries(entries), nil
}

func (s *SettingService) ReplaceGlobalBlacklist(ctx context.Context, entries []GlobalBlacklistEntry) ([]GlobalBlacklistEntry, error) {
	normalized, err := normalizeGlobalBlacklist(entries, false)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyGlobalBlacklist, string(data)); err != nil {
		return nil, err
	}
	s.globalBlacklistCache.Store(&cachedGlobalBlacklist{Entries: normalized, ExpiresAt: time.Now().Add(5 * time.Second)})
	return cloneGlobalBlacklistEntries(normalized), nil
}

func (s *SettingService) AddGlobalBlacklistEntry(ctx context.Context, entry GlobalBlacklistEntry) (GlobalBlacklistEntry, error) {
	entries, err := s.GlobalBlacklistSnapshot(ctx)
	if err != nil {
		return GlobalBlacklistEntry{}, err
	}
	entry, err = normalizeGlobalBlacklistEntry(entry, true)
	if err != nil {
		return GlobalBlacklistEntry{}, err
	}
	for _, existing := range entries {
		if existing.Kind == entry.Kind && existing.Value == entry.Value && existing.Enabled {
			return existing, nil
		}
	}
	entries = append(entries, entry)
	if _, err := s.ReplaceGlobalBlacklist(ctx, entries); err != nil {
		return GlobalBlacklistEntry{}, err
	}
	return entry, nil
}

func (s *SettingService) DeleteGlobalBlacklistEntry(ctx context.Context, id string) error {
	entries, err := s.GlobalBlacklistSnapshot(ctx)
	if err != nil {
		return err
	}
	filtered := entries[:0]
	found := false
	for _, entry := range entries {
		if entry.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, entry)
	}
	if !found {
		return ErrSettingNotFound
	}
	_, err = s.ReplaceGlobalBlacklist(ctx, filtered)
	return err
}

// IsGloballyBlacklisted checks an authenticated user ID and a client IP.
func (s *SettingService) IsGloballyBlacklisted(ctx context.Context, userID int64, clientIP string) (bool, GlobalBlacklistEntry, error) {
	entries, err := s.GlobalBlacklistSnapshot(ctx)
	if err != nil {
		return false, GlobalBlacklistEntry{}, err
	}
	now := time.Now()
	for _, entry := range entries {
		if !entry.Enabled || (entry.ExpiresAt != nil && !entry.ExpiresAt.After(now)) {
			continue
		}
		if entry.Kind == GlobalBlacklistKindAccount && userID > 0 && entry.Value == strconv.FormatInt(userID, 10) {
			return true, entry, nil
		}
		if entry.Kind == GlobalBlacklistKindIP && matchBlacklistIP(clientIP, entry.Value) {
			return true, entry, nil
		}
	}
	return false, GlobalBlacklistEntry{}, nil
}

func decodeGlobalBlacklist(raw string) ([]GlobalBlacklistEntry, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var entries []GlobalBlacklistEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("decode global blacklist: %w", err)
	}
	return normalizeGlobalBlacklist(entries, false)
}

func normalizeGlobalBlacklist(entries []GlobalBlacklistEntry, assignIDs bool) ([]GlobalBlacklistEntry, error) {
	seen := make(map[string]struct{}, len(entries))
	result := make([]GlobalBlacklistEntry, 0, len(entries))
	for _, entry := range entries {
		normalized, err := normalizeGlobalBlacklistEntry(entry, assignIDs)
		if err != nil {
			return nil, err
		}
		key := normalized.Kind + "\x00" + normalized.Value
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func normalizeGlobalBlacklistEntry(entry GlobalBlacklistEntry, assignID bool) (GlobalBlacklistEntry, error) {
	entry.Kind = strings.ToLower(strings.TrimSpace(entry.Kind))
	entry.Value = strings.TrimSpace(entry.Value)
	if entry.Kind != GlobalBlacklistKindAccount && entry.Kind != GlobalBlacklistKindIP {
		return GlobalBlacklistEntry{}, errGlobalBlacklistInvalidKind
	}
	if entry.Kind == GlobalBlacklistKindAccount {
		id, err := strconv.ParseInt(entry.Value, 10, 64)
		if err != nil || id <= 0 {
			return GlobalBlacklistEntry{}, errGlobalBlacklistInvalidValue
		}
		entry.Value = strconv.FormatInt(id, 10)
	} else {
		entry.Value = normalizeBlacklistIP(entry.Value)
		if entry.Value == "" {
			return GlobalBlacklistEntry{}, errGlobalBlacklistInvalidValue
		}
	}
	if entry.ID == "" || assignID {
		entry.ID = uuid.NewString()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	return entry, nil
}

func normalizeBlacklistIP(value string) string {
	value = strings.TrimSpace(value)
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	if _, network, err := net.ParseCIDR(value); err == nil {
		return network.String()
	}
	return ""
}

func matchBlacklistIP(clientIP, rule string) bool {
	ip := net.ParseIP(strings.TrimSpace(clientIP))
	if ip == nil {
		return false
	}
	if ruleIP := net.ParseIP(rule); ruleIP != nil {
		return ruleIP.Equal(ip)
	}
	_, network, err := net.ParseCIDR(rule)
	return err == nil && network.Contains(ip)
}

func cloneGlobalBlacklistEntries(entries []GlobalBlacklistEntry) []GlobalBlacklistEntry {
	return append([]GlobalBlacklistEntry(nil), entries...)
}
