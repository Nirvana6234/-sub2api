package service

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

const (
	autoGroupInvalidationUserPrefix  = "auto-group:user:"
	autoGroupInvalidationGroupPrefix = "auto-group:group:"
)

// InvalidateAuthCacheByKey 清除指定 API Key 的认证缓存
func (s *APIKeyService) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	if key == "" {
		return
	}
	cacheKey := s.authCacheKey(key)
	s.deleteAuthCache(ctx, cacheKey)
}

// InvalidateAuthCacheByUserID 清除用户相关的 API Key 认证缓存
func (s *APIKeyService) InvalidateAuthCacheByUserID(ctx context.Context, userID int64) {
	if userID <= 0 {
		return
	}
	keys, err := s.apiKeyRepo.ListKeysByUserID(ctx, userID)
	if err != nil {
		return
	}
	s.deleteAuthCacheByKeys(ctx, keys)
}

// InvalidateAuthCacheByGroupID 清除分组相关的 API Key 认证缓存
func (s *APIKeyService) InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64) {
	if groupID <= 0 {
		return
	}
	keys, err := s.apiKeyRepo.ListKeysByGroupID(ctx, groupID)
	if err != nil {
		return
	}
	// Automatic keys keep candidate IDs in JSON and therefore are not returned
	// by the normal group_id query. Include them when the repository supports
	// the narrow cache-invalidation lookup so deleted/changed groups cannot
	// survive in an auth snapshot.
	if autoLister, ok := s.apiKeyRepo.(interface {
		ListKeysByAutoGroupID(context.Context, int64) ([]string, error)
	}); ok {
		autoKeys, autoErr := autoLister.ListKeysByAutoGroupID(ctx, groupID)
		if autoErr == nil {
			keys = append(keys, autoKeys...)
		}
	}
	s.deleteAuthCacheByKeys(ctx, keys)
}

// InvalidateAutoGroupSelectionsByUserID clears settled Auto choices after a
// user's effective group rates or candidate access changes.
func (s *APIKeyService) InvalidateAutoGroupSelectionsByUserID(ctx context.Context, userID int64) {
	if userID <= 0 {
		return
	}
	s.deleteAutoGroupSelectionsByUserID(userID)
	s.publishAutoGroupInvalidation(ctx, autoGroupUserInvalidationMessage(userID))
}

// InvalidateAutoGroupSelectionsByGroupID clears settled Auto choices after a
// candidate group's rate or routing configuration changes.
func (s *APIKeyService) InvalidateAutoGroupSelectionsByGroupID(ctx context.Context, groupID int64) {
	if groupID <= 0 {
		return
	}
	s.deleteAutoGroupSelectionsByGroupID(groupID)
	s.publishAutoGroupInvalidation(ctx, autoGroupGroupInvalidationMessage(groupID))
}

func autoGroupUserInvalidationMessage(userID int64) string {
	return autoGroupInvalidationUserPrefix + strconv.FormatInt(userID, 10)
}

func autoGroupGroupInvalidationMessage(groupID int64) string {
	return autoGroupInvalidationGroupPrefix + strconv.FormatInt(groupID, 10)
}

func (s *APIKeyService) publishAutoGroupInvalidation(ctx context.Context, message string) {
	if s == nil || s.cache == nil || message == "" {
		return
	}
	if err := s.cache.PublishAuthCacheInvalidation(ctx, message); err == nil {
		return
	} else if s.authInvalidationOutbox == nil {
		slog.Warn("auto group invalidation publish failed without outbox fallback", "message", message, "error", err)
		return
	}
	retryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.authInvalidationOutbox.EnqueueControl(retryCtx, message); err != nil {
		slog.Warn("auto group invalidation outbox fallback failed", "message", message, "error", err)
	}
}

func (s *APIKeyService) SetAuthCacheInvalidationOutbox(repo AuthCacheInvalidationOutboxRepository) {
	if s != nil {
		s.authInvalidationOutbox = repo
	}
}

func (s *APIKeyService) handleAuthCacheInvalidationMessage(message string) {
	if id, ok := parseAutoGroupInvalidationID(message, autoGroupInvalidationUserPrefix); ok {
		s.deleteAutoGroupSelectionsByUserID(id)
		return
	}
	if id, ok := parseAutoGroupInvalidationID(message, autoGroupInvalidationGroupPrefix); ok {
		s.deleteAutoGroupSelectionsByGroupID(id)
		return
	}
	s.invalidateLocalAuthCache(message)
}

func parseAutoGroupInvalidationID(message, prefix string) (int64, bool) {
	value, ok := strings.CutPrefix(message, prefix)
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0
}

func isAutoGroupInvalidationMessage(message string) bool {
	if _, ok := parseAutoGroupInvalidationID(message, autoGroupInvalidationUserPrefix); ok {
		return true
	}
	_, ok := parseAutoGroupInvalidationID(message, autoGroupInvalidationGroupPrefix)
	return ok
}

func (s *APIKeyService) deleteAutoGroupSelectionsByUserID(userID int64) {
	if s == nil || userID <= 0 {
		return
	}
	s.autoGroupSelectionMu.Lock()
	defer s.autoGroupSelectionMu.Unlock()
	if s.autoGroupUserGenerations == nil {
		s.autoGroupUserGenerations = make(map[int64]uint64)
	}
	s.autoGroupUserGenerations[userID]++
	s.autoGroupSelections.Range(func(key, value any) bool {
		selection, ok := value.(autoGroupSelection)
		if ok && selection.userID == userID {
			s.autoGroupSelections.Delete(key)
		}
		return true
	})
}

func (s *APIKeyService) deleteAutoGroupSelectionsByGroupID(groupID int64) {
	if s == nil || groupID <= 0 {
		return
	}
	s.autoGroupSelectionMu.Lock()
	defer s.autoGroupSelectionMu.Unlock()
	if s.autoGroupGroupGenerations == nil {
		s.autoGroupGroupGenerations = make(map[int64]uint64)
	}
	s.autoGroupGroupGenerations[groupID]++
	s.autoGroupAllGroupsEpoch++
	s.autoGroupSelections.Range(func(key, value any) bool {
		selection, ok := value.(autoGroupSelection)
		if ok && autoGroupSelectionUsesGroup(selection, groupID) {
			s.autoGroupSelections.Delete(key)
		}
		return true
	})
}

func autoGroupSelectionUsesGroup(selection autoGroupSelection, groupID int64) bool {
	if selection.allCandidateGroups || selection.groupID == groupID ||
		(selection.selectedGroup != nil && selection.selectedGroup.ID == groupID) {
		return true
	}
	for _, candidateGroupID := range selection.candidateGroupIDs {
		if candidateGroupID == groupID {
			return true
		}
	}
	return false
}

func (s *APIKeyService) deleteAuthCacheByKeys(ctx context.Context, keys []string) {
	if len(keys) == 0 {
		return
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		s.deleteAuthCache(ctx, s.authCacheKey(key))
	}
}
