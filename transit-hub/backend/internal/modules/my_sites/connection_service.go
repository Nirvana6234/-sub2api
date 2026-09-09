package my_sites

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"transithub/backend/internal/modules/upstream"
)

func randomLatencyCompensationPayoutID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate latency compensation payout id: %w", err)
	}
	return "lcp_" + hex.EncodeToString(bytes), nil
}

func randomLatencySubsidyTaskID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate latency subsidy task id: %w", err)
	}
	return "lst_" + hex.EncodeToString(bytes), nil
}

// connectionContext contains the two independently authenticated sides of a
// connection. Their platforms may differ and must never be inferred from one another.
type connectionContext struct {
	adminAccountID  string
	state           *State
	upstreamSite    *upstream.Site
	upstreamSession upstream.Session
	groupType       string
	groupName       string
	multiplierLabel string
}

func addToPricingMapping(value *bool) bool {
	return value == nil || *value
}

func normalizeOperationID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		return "", requestError(ErrorRequest)
	}
	return value, nil
}

func (s *Service) prepareConnectionContext(ctx context.Context, userID, siteID, groupID, groupName, requestedType string, requireAdminResourceType bool) (connectionContext, error) {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return connectionContext{}, err
	}
	state, err := s.authenticatedState(ctx, userID, adminAccountID)
	if err != nil {
		return connectionContext{}, err
	}
	upstreamSite, err := s.upstreamLookup.GetSite(ctx, strings.TrimSpace(siteID))
	if err != nil || upstreamSite == nil || upstreamSite.Session == nil || upstreamSite.UserID != userID || upstreamSite.AdminAccountID != adminAccountID {
		return connectionContext{}, requestError(ErrorRequest)
	}

	groupType, multiplierLabel := resolveGroupInfo(upstreamSite.Metrics.Groups, strings.TrimSpace(groupID))
	if groupType == "" {
		groupType = strings.ToLower(strings.TrimSpace(requestedType))
	}
	resolvedName := strings.TrimSpace(groupName)
	if resolvedName == "" {
		resolvedName = strings.TrimSpace(groupID)
	}
	if resolvedName == "" {
		return connectionContext{}, requestError(ErrorRequest)
	}
	// A Sub2API admin account requires a concrete provider type even when the
	// upstream side is NewAPI and its group itself has no type metadata.
	if requireAdminResourceType && state.Session.Platform == upstream.PlatformSub2API && groupType == "" {
		return connectionContext{}, requestError(ErrorRequest)
	}

	return connectionContext{
		adminAccountID:  adminAccountID,
		state:           state,
		upstreamSite:    upstreamSite,
		upstreamSession: *upstreamSite.Session,
		groupType:       groupType,
		groupName:       resolvedName,
		multiplierLabel: multiplierLabel,
	}, nil
}

func (s *Service) idempotentConnection(ctx context.Context, userID, adminAccountID, operationID string) (*RealConnection, error) {
	if operationID == "" || s.connRepository == nil {
		return nil, nil
	}
	repo, ok := s.connRepository.(IdempotentRealConnectionRepository)
	if !ok {
		return nil, nil
	}
	return repo.GetRealConnectionByOperationID(ctx, userID, adminAccountID, operationID)
}

func (s *Service) rejectDuplicateTarget(ctx context.Context, userID, adminAccountID, siteID, groupID, groupName string) error {
	if s.connRepository == nil {
		return nil
	}
	connections, err := s.connRepository.ListRealConnections(ctx, userID, adminAccountID)
	if err != nil {
		return err
	}
	for _, conn := range connections {
		if conn.UpstreamSiteID != siteID || conn.Status != "" && conn.Status != ConnectionStatusActive {
			continue
		}
		if conn.UpstreamGroupID == groupID || (conn.UpstreamGroupID == "" && groupID == "" && conn.UpstreamGroupName == groupName) {
			return requestError(ErrorConnectionExists)
		}
	}
	return nil
}

func (s *Service) resolveAdminGroups(ctx context.Context, state *State, requestedIDs []string) ([]string, []string, error) {
	if state == nil || len(requestedIDs) == 0 {
		return nil, nil, requestError(ErrorRequest)
	}
	groups, err := s.platformService.FetchAdminAllGroups(state.Session)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]upstream.AdminGroupInfo, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
	}
	ids := make([]string, 0, len(requestedIDs))
	names := make([]string, 0, len(requestedIDs))
	seen := make(map[string]struct{}, len(requestedIDs))
	for _, requestedID := range requestedIDs {
		requestedID = strings.TrimSpace(requestedID)
		group, ok := byID[requestedID]
		if !ok {
			return nil, nil, requestError(ErrorRequest)
		}
		if _, exists := seen[group.ID]; exists {
			continue
		}
		seen[group.ID] = struct{}{}
		ids = append(ids, group.ID)
		names = append(names, group.Name)
	}
	if len(ids) == 0 {
		return nil, nil, requestError(ErrorRequest)
	}
	return ids, names, nil
}

func (s *Service) realConnectManaged(ctx context.Context, userID string, req RealConnectRequest) (RealConnectResponse, error) {
	if strings.TrimSpace(req.UpstreamSiteID) == "" || strings.TrimSpace(req.UpstreamGroupID) == "" || len(req.OwnGroupIDs) == 0 {
		return RealConnectResponse{}, requestError(ErrorRequest)
	}
	operationID, err := normalizeOperationID(req.OperationID)
	if err != nil {
		return RealConnectResponse{}, err
	}
	connectionCtx, err := s.prepareConnectionContext(ctx, userID, req.UpstreamSiteID, req.UpstreamGroupID, req.UpstreamGroupName, req.GroupType, true)
	if err != nil {
		return RealConnectResponse{}, err
	}
	if existing, err := s.idempotentConnection(ctx, userID, connectionCtx.adminAccountID, operationID); err != nil {
		return RealConnectResponse{}, err
	} else if existing != nil {
		return RealConnectResponse{Connection: publicRealConnection(*existing)}, nil
	}
	if err := s.rejectDuplicateTarget(ctx, userID, connectionCtx.adminAccountID, req.UpstreamSiteID, req.UpstreamGroupID, connectionCtx.groupName); err != nil {
		return RealConnectResponse{}, err
	}
	ownGroupIDs, ownGroupNames, err := s.resolveAdminGroups(ctx, connectionCtx.state, req.OwnGroupIDs)
	if err != nil {
		return RealConnectResponse{}, err
	}

	connID, err := randomConnID()
	if err != nil {
		return RealConnectResponse{}, err
	}
	resourceName := fmt.Sprintf("%s-%s-%s", randomKeyPrefix(), connectionCtx.upstreamSite.Name, connectionCtx.groupName)
	keyID, key, err := s.createUpstreamCredential(connectionCtx.upstreamSession, resourceName, req.UpstreamGroupID)
	if err != nil {
		return RealConnectResponse{}, err
	}
	rollbackKey := func() {
		if rollbackErr := s.deleteUpstreamCredential(connectionCtx.upstreamSession, keyID); rollbackErr != nil {
			log.Printf("[real-connect] compensate upstream credential failed platform=%s id=%s err=%v", connectionCtx.upstreamSession.Platform, keyID, rollbackErr)
		}
	}

	adminResourceID, adminResourceName, err := s.createAdminResource(connectionCtx, req.ChannelType, ownGroupIDs, key)
	if err != nil {
		rollbackKey()
		return RealConnectResponse{}, err
	}
	rollbackAdmin := func() {
		if rollbackErr := s.deleteAdminResource(connectionCtx.state.Session, adminResourceID); rollbackErr != nil {
			log.Printf("[real-connect] compensate admin resource failed platform=%s id=%s err=%v", connectionCtx.state.Session.Platform, adminResourceID, rollbackErr)
		}
	}

	conn := RealConnection{
		ID:                      connID,
		UserID:                  userID,
		WorkspaceAdminAccountID: connectionCtx.adminAccountID,
		UpstreamSiteID:          req.UpstreamSiteID,
		UpstreamGroupID:         req.UpstreamGroupID,
		UpstreamGroupName:       connectionCtx.groupName,
		UpstreamKeyID:           keyID,
		UpstreamKey:             key,
		AdminAccountID:          adminResourceID,
		AdminAccountName:        adminResourceName,
		OwnGroupIDs:             ownGroupIDs,
		OwnGroupNames:           ownGroupNames,
		GroupType:               connectionCtx.groupType,
		ProvisioningMode:        ProvisioningModeManaged,
		Status:                  ConnectionStatusActive,
		UpstreamPlatform:        string(connectionCtx.upstreamSession.Platform),
		AdminPlatform:           string(connectionCtx.state.Session.Platform),
		PricingMappingEnabled:   addToPricingMapping(req.AddToPricingMapping),
		OperationID:             operationID,
		CanDeleteRemote:         true,
		CreatedAt:               time.Now().Format(time.RFC3339),
	}
	if err := s.persistConnection(ctx, conn); err != nil {
		rollbackAdmin()
		rollbackKey()
		return RealConnectResponse{}, err
	}
	return RealConnectResponse{Connection: publicRealConnection(conn)}, nil
}

func (s *Service) createUpstreamCredential(session upstream.Session, name, groupID string) (string, string, error) {
	switch session.Platform {
	case upstream.PlatformNewAPI:
		return s.platformService.CreateNewAPIToken(session, name, groupID)
	case upstream.PlatformSub2API:
		numericGroupID, err := strconv.Atoi(groupID)
		if err != nil {
			return "", "", requestError(ErrorRequest)
		}
		return s.platformService.CreateSub2APIKey(session, name, numericGroupID)
	default:
		return "", "", requestError(ErrorRequest)
	}
}

func (s *Service) deleteUpstreamCredential(session upstream.Session, keyID string) error {
	if strings.TrimSpace(keyID) == "" {
		return nil
	}
	if session.Platform == upstream.PlatformNewAPI {
		return s.platformService.DeleteNewAPIToken(session, keyID)
	}
	return s.platformService.DeleteSub2APIKey(session, keyID)
}

func (s *Service) createAdminResource(connectionCtx connectionContext, requestedChannelType int, ownGroupIDs []string, key string) (string, string, error) {
	if connectionCtx.state.Session.Platform == upstream.PlatformNewAPI {
		channelType := requestedChannelType
		if channelType <= 0 {
			channelType = groupTypeToNewAPIChannelType(connectionCtx.groupType)
		}
		name := fmt.Sprintf("%s-【%s】-%s", newAPIChannelTypeName(channelType), connectionCtx.upstreamSite.Name, connectionCtx.groupName)
		id, err := s.platformService.CreateNewAPIChannel(connectionCtx.state.Session, name, connectionCtx.upstreamSite.BaseURL, key, channelType, ownGroupIDs)
		return id, name, err
	}

	numericGroupIDs, err := stringsToInts(ownGroupIDs)
	if err != nil {
		return "", "", requestError(ErrorRequest)
	}
	rateLabel := connectionCtx.multiplierLabel
	if rateLabel == "" {
		rateLabel = connectionCtx.groupName
	}
	name := fmt.Sprintf("%s-【%s】-%s", groupTypePrefix(connectionCtx.groupType), connectionCtx.upstreamSite.Name, rateLabel)
	payload := buildAccountPayload(connectionCtx.groupType, connectionCtx.upstreamSite.BaseURL, key, numericGroupIDs, name)
	id, err := s.platformService.CreateSub2APIAdminAccount(connectionCtx.state.Session, payload)
	return id, name, err
}

func (s *Service) deleteAdminResource(session upstream.Session, resourceID string) error {
	if strings.TrimSpace(resourceID) == "" {
		return nil
	}
	if session.Platform == upstream.PlatformNewAPI {
		return s.platformService.DeleteNewAPIChannel(session, resourceID)
	}
	return s.platformService.DeleteSub2APIAdminAccount(session, resourceID)
}

func (s *Service) persistConnection(ctx context.Context, conn RealConnection) error {
	if s.connRepository == nil {
		return requestError(ErrorRequest)
	}
	if repository, ok := s.connRepository.(AtomicRealConnectionRepository); ok {
		return repository.SaveRealConnectionWithPricingMapping(ctx, conn)
	}
	if err := s.connRepository.SaveRealConnection(ctx, conn); err != nil {
		return err
	}
	// Older/in-memory repositories do not expose a transaction boundary. Keep
	// their existing behavior for tests and rolling deployments.
	if conn.PricingMappingEnabled {
		s.addUpstreamMapping(ctx, conn.UserID, conn.WorkspaceAdminAccountID, conn.OwnGroupIDs, conn.UpstreamSiteID, conn.UpstreamGroupName, conn.AdminPlatform, conn.AdminAccountID)
	}
	return nil
}

func (s *Service) listOwnedUpstreamKeys(ctx context.Context, userID, adminAccountID, siteID string) (*upstream.Site, []upstream.Sub2APIKeyItem, error) {
	upstreamSite, err := s.upstreamLookup.GetSite(ctx, strings.TrimSpace(siteID))
	if err != nil || upstreamSite == nil || upstreamSite.Session == nil || upstreamSite.UserID != userID || upstreamSite.AdminAccountID != adminAccountID {
		return nil, nil, requestError(ErrorRequest)
	}
	var keys []upstream.Sub2APIKeyItem
	if upstreamSite.Session.Platform == upstream.PlatformNewAPI {
		keys, err = s.platformService.ListNewAPITokens(*upstreamSite.Session)
	} else {
		keys, err = s.platformService.ListSub2APIKeys(*upstreamSite.Session)
	}
	return upstreamSite, keys, err
}

func credentialMatchesGroup(item upstream.Sub2APIKeyItem, groupID, groupName string) bool {
	groupID = strings.TrimSpace(groupID)
	groupName = strings.TrimSpace(groupName)
	return (item.GroupID != "" && (item.GroupID == groupID || item.GroupID == groupName)) ||
		(item.GroupName != "" && (item.GroupName == groupName || item.GroupName == groupID))
}

// ListUpstreamCredentials returns only credentials that belong to the selected
// upstream group and strips the full secret before crossing the HTTP boundary.
func (s *Service) ListUpstreamCredentials(ctx context.Context, userID, siteID, groupID, groupName string) ([]UpstreamCredentialOption, error) {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return nil, err
	}
	_, keys, err := s.listOwnedUpstreamKeys(ctx, userID, adminAccountID, siteID)
	if err != nil {
		return nil, err
	}
	result := make([]UpstreamCredentialOption, 0, len(keys))
	for _, key := range keys {
		if !credentialMatchesGroup(key, groupID, groupName) {
			continue
		}
		result = append(result, UpstreamCredentialOption{
			ID: key.ID, Name: key.Name, GroupID: key.GroupID, GroupName: key.GroupName,
			Status: key.Status, KeyPreview: safeCredentialPreview(key.Key),
		})
	}
	return result, nil
}

func safeCredentialPreview(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 12 {
		return value
	}
	return value[:6] + "..." + value[len(value)-4:]
}

// ListAdminResources lists existing accounts/channels in one current-admin
// group. Selection is revalidated by RealBind; this endpoint is presentation only.
func (s *Service) ListAdminResources(ctx context.Context, userID, adminGroupID string) ([]AdminResourceOption, error) {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return nil, err
	}
	state, err := s.authenticatedState(ctx, userID, adminAccountID)
	if err != nil {
		return nil, err
	}
	group, err := s.findAdminGroup(ctx, state, adminGroupID)
	if err != nil {
		return nil, err
	}
	resources, err := s.platformService.ListAdminGroupAccounts(state.Session, group)
	if err != nil {
		return nil, err
	}
	result := make([]AdminResourceOption, 0, len(resources))
	for _, resource := range resources {
		groupIDs := resource.GroupIDs
		if len(groupIDs) == 0 {
			groupIDs = []string{group.ID}
		}
		result = append(result, AdminResourceOption{
			ID: resource.ID, Name: resource.Name, Type: resource.Type,
			Status: resource.Status, Platform: resource.Platform, GroupIDs: groupIDs,
		})
	}
	return result, nil
}

// PreviewLatencyCompensation reports what a latency-compensation payout over
// [from, to) at thresholdMs would look like on the connected Sub2API site —
// per user, how much was actually charged for slow requests versus what
// they cost the platform — without crediting anyone. Safe to call
// repeatedly while an operator narrows the window in the UI.
func (s *Service) PreviewLatencyCompensation(
	ctx context.Context, userID string, from, to time.Time, thresholdMs int, profitRatio float64,
) (upstream.LatencyCompensationSummary, error) {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return upstream.LatencyCompensationSummary{}, err
	}
	state, err := s.authenticatedState(ctx, userID, adminAccountID)
	if err != nil {
		return upstream.LatencyCompensationSummary{}, err
	}
	return s.platformService.FetchSub2APILatencyCompensationPreview(state.Session, from, to, thresholdMs, profitRatio)
}

// ApplyLatencyCompensation pays out a latency-compensation batch on the
// connected Sub2API site. This is the irreversible write — the caller must
// have already shown the operator a PreviewLatencyCompensation result and
// gotten explicit confirmation before calling this. taskID/taskName record
// which 延迟补贴任务 triggered the payout (manual run or the scheduler both
// always go through a task now).
func (s *Service) ApplyLatencyCompensation(
	ctx context.Context, userID string, from, to time.Time, thresholdMs int, profitRatio float64, taskID, taskName string,
) (upstream.LatencyCompensationSummary, error) {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return upstream.LatencyCompensationSummary{}, err
	}
	state, err := s.authenticatedState(ctx, userID, adminAccountID)
	if err != nil {
		return upstream.LatencyCompensationSummary{}, err
	}
	summary, err := s.platformService.ApplySub2APILatencyCompensation(state.Session, from, to, thresholdMs, profitRatio)
	if err != nil {
		return upstream.LatencyCompensationSummary{}, err
	}
	// Record even a zero-amount payout: it still marked rows compensated on
	// the Sub2API side (so a re-run over the same window won't double-count
	// requests), and the report needs an accurate "no compensation today"
	// as much as it needs the actual amount. payoutRepository is optional —
	// see its doc comment for why a nil repo doesn't block the payout itself.
	if s.payoutRepository != nil {
		payoutID, idErr := randomLatencyCompensationPayoutID()
		if idErr != nil {
			log.Printf("latency compensation: paid %d users $%.8f but failed to generate a payout record id (report will under-count cost): user_id=%s admin_account_id=%s err=%v",
				len(summary.Users), summary.TotalCompensation, userID, adminAccountID, idErr)
			return summary, nil
		}
		record := LatencyCompensationPayout{
			ID:               payoutID,
			UserID:           userID,
			AdminAccountID:   adminAccountID,
			SiteBaseURL:      state.Session.BaseURL,
			FromTime:         from,
			ToTime:           to,
			ThresholdMs:      thresholdMs,
			ProfitRatio:      profitRatio,
			UsersCompensated: len(summary.Users),
			AmountUSD:        summary.TotalCompensation,
			TaskID:           taskID,
			TaskName:         taskName,
			Users:            toLatencyCompensationSummary(summary).Users,
		}
		if recordErr := s.payoutRepository.RecordLatencyCompensationPayout(ctx, record); recordErr != nil {
			// The money already moved on the Sub2API side; failing to record
			// it locally must not roll that back or hide the result from the
			// operator. Surfacing this loudly (not just a log line) matters
			// because a missed record means the report will under-count
			// today's cost — same failure class as the analogous mark-step
			// warning on the Sub2API side.
			log.Printf("latency compensation: paid %d users $%.8f but failed to record payout (report will under-count cost): user_id=%s admin_account_id=%s err=%v",
				len(summary.Users), summary.TotalCompensation, userID, adminAccountID, recordErr)
		}
	}
	return summary, nil
}

// SumLatencyCompensationPayoutsUSD totals latency-compensation payouts
// recorded in [from, to) for the daily/weekly report to subtract from
// profit. Returns 0 (not an error) when no payout repository is configured,
// so a deployment that hasn't wired one up still gets a report — just one
// that doesn't know about compensation, same as before this feature existed.
func (s *Service) SumLatencyCompensationPayoutsUSD(ctx context.Context, userID, adminAccountID string, from, to time.Time) (float64, error) {
	if s.payoutRepository == nil {
		return 0, nil
	}
	return s.payoutRepository.SumLatencyCompensationPayoutsUSD(ctx, userID, adminAccountID, from, to)
}

// ListLatencyCompensationPayouts returns past payout batches (newest first),
// each with its full per-user breakdown, for the operator to review what
// was actually paid and when. Returns an empty slice (not an error) when no
// payout repository is configured — same "feature degrades, doesn't break"
// stance as SumLatencyCompensationPayoutsUSD.
func (s *Service) ListLatencyCompensationPayouts(ctx context.Context, userID string, limit int) ([]LatencyCompensationPayout, error) {
	if s.payoutRepository == nil {
		return []LatencyCompensationPayout{}, nil
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.payoutRepository.ListLatencyCompensationPayouts(ctx, userID, adminAccountID, limit)
}

// RevokeLatencyCompensationPayout undoes an entire past payout batch — every
// user in it, not a subset — because the batch was a mistake (wrong
// threshold/ratio, wrong window). It claws the money back on Sub2API without
// creating a redeem_codes entry there (a visible "+补偿/-补偿" pair on the
// user's own balance history reads as a billing mistake and generates
// support tickets, when this is the operator correcting their own mistake),
// reopens the underlying requests so a corrected re-run can compensate them
// properly, and flags the payout revoked locally — the only place this undo
// is ever recorded. Already-revoked or not-found payouts return an error;
// callers should treat any error as "nothing changed".
func (s *Service) RevokeLatencyCompensationPayout(ctx context.Context, userID, payoutID string) (upstream.RevokeLatencyCompensationResult, error) {
	if s.payoutRepository == nil {
		return upstream.RevokeLatencyCompensationResult{}, requestError(ErrorRequest)
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return upstream.RevokeLatencyCompensationResult{}, err
	}
	payout, err := s.payoutRepository.GetLatencyCompensationPayout(ctx, payoutID, userID, adminAccountID)
	if err != nil {
		return upstream.RevokeLatencyCompensationResult{}, err
	}
	if payout.RevokedAt != nil {
		return upstream.RevokeLatencyCompensationResult{}, requestError(ErrorRequest)
	}

	state, err := s.authenticatedState(ctx, userID, adminAccountID)
	if err != nil {
		return upstream.RevokeLatencyCompensationResult{}, err
	}
	users := make([]upstream.LatencyCompensationUserSummary, 0, len(payout.Users))
	for _, u := range payout.Users {
		users = append(users, upstream.LatencyCompensationUserSummary{
			UserID:       u.UserID,
			Email:        u.Email,
			Requests:     u.Requests,
			ActualCost:   u.ActualCost,
			AccountCost:  u.AccountCost,
			Compensation: u.Compensation,
		})
	}
	result, err := s.platformService.RevokeSub2APILatencyCompensation(state.Session, payout.FromTime, payout.ToTime, payout.ThresholdMs, users)
	if err != nil {
		return upstream.RevokeLatencyCompensationResult{}, err
	}

	if markErr := s.payoutRepository.MarkLatencyCompensationPayoutRevoked(ctx, payoutID, userID, adminAccountID); markErr != nil {
		// 钱已经在 Sub2API 那边扣回了，这一步只影响本地历史列表还显示成"未撤回"，
		// 不影响撤回本身是否生效，但要响亮地记日志，免得下次误判成还没撤过。
		log.Printf("latency compensation revoke: reversed on Sub2API but failed to mark locally revoked (history will look stale): payout_id=%s user_id=%s admin_account_id=%s err=%v",
			payoutID, userID, adminAccountID, markErr)
	}
	return result, nil
}

// normalizeLatencySubsidyTaskInput trims/validates a task input. Name empty
// after trimming, threshold <= 0, ratio outside [0,1], or an invalid daily
// window are all rejected here rather than left for the scheduler to
// silently misbehave on later.
func normalizeLatencySubsidyTaskInput(input LatencySubsidyTaskInput) (LatencySubsidyTaskInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return input, requestError(ErrorRequest)
	}
	if input.ThresholdMs <= 0 {
		return input, requestError(ErrorRequest)
	}
	if input.ProfitRatio < 0 || input.ProfitRatio > 1 {
		return input, requestError(ErrorRequest)
	}
	input.WindowStart = strings.TrimSpace(input.WindowStart)
	if input.WindowStart == "" {
		input.WindowStart = "00:00"
	} else if _, err := time.Parse("15:04", input.WindowStart); err != nil {
		return input, requestError(ErrorRequest)
	}
	input.WindowEnd = strings.TrimSpace(input.WindowEnd)
	if input.WindowEnd == "" {
		input.WindowEnd = "23:59"
	} else if _, err := time.Parse("15:04", input.WindowEnd); err != nil {
		return input, requestError(ErrorRequest)
	}
	// 字符串比较对零填充的 HH:MM 24 小时制是安全的，跟数值比较等价。
	// v1 只支持当天窗口，跨零点（比如 22:00~02:00）暂不支持。
	if input.WindowEnd <= input.WindowStart {
		return input, requestError(ErrorRequest)
	}

	switch input.RecurrenceType {
	case "":
		// 老客户端没有这个字段，按原来的"每天"处理。
		input.RecurrenceType = LatencySubsidyRecurrenceDaily
	case LatencySubsidyRecurrenceDaily, LatencySubsidyRecurrenceWeekday:
		input.RecurrenceDaysOfWeek = nil
	case LatencySubsidyRecurrenceWeekly:
		if len(input.RecurrenceDaysOfWeek) == 0 {
			return input, requestError(ErrorRequest)
		}
		seen := make(map[int]bool, len(input.RecurrenceDaysOfWeek))
		days := make([]int, 0, len(input.RecurrenceDaysOfWeek))
		for _, d := range input.RecurrenceDaysOfWeek {
			if d < 0 || d > 6 || seen[d] {
				continue
			}
			seen[d] = true
			days = append(days, d)
		}
		if len(days) == 0 {
			return input, requestError(ErrorRequest)
		}
		sort.Ints(days)
		input.RecurrenceDaysOfWeek = days
	default:
		return input, requestError(ErrorRequest)
	}

	input.ValidFrom = strings.TrimSpace(input.ValidFrom)
	input.ValidUntil = strings.TrimSpace(input.ValidUntil)
	if input.ValidFrom != "" {
		if _, err := time.Parse("2006-01-02", input.ValidFrom); err != nil {
			return input, requestError(ErrorRequest)
		}
	}
	if input.ValidUntil != "" {
		if _, err := time.Parse("2006-01-02", input.ValidUntil); err != nil {
			return input, requestError(ErrorRequest)
		}
	}
	if input.ValidFrom != "" && input.ValidUntil != "" && input.ValidUntil < input.ValidFrom {
		return input, requestError(ErrorRequest)
	}
	return input, nil
}

// parseLatencySubsidyDate turns a "2026-01-02" (or "") input-form date into
// the *time.Time the repository stores — nil means no bound. Caller must
// have already validated the format via normalizeLatencySubsidyTaskInput.
func parseLatencySubsidyDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}

// ListLatencySubsidyTasks returns every 延迟补贴任务 for the caller's current
// workspace. Returns an empty slice (not an error) when no task repository
// is configured, same "feature degrades, doesn't break" stance used
// elsewhere in this file.
func (s *Service) ListLatencySubsidyTasks(ctx context.Context, userID string) ([]LatencySubsidyTask, error) {
	if s.taskRepository == nil {
		return []LatencySubsidyTask{}, nil
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.taskRepository.ListLatencySubsidyTasks(ctx, userID, adminAccountID)
}

// CreateLatencySubsidyTask saves a new task. It does not run anything —
// running (manual or scheduled) is a separate step.
func (s *Service) CreateLatencySubsidyTask(ctx context.Context, userID string, input LatencySubsidyTaskInput) (LatencySubsidyTask, error) {
	if s.taskRepository == nil {
		return LatencySubsidyTask{}, requestError(ErrorRequest)
	}
	input, err := normalizeLatencySubsidyTaskInput(input)
	if err != nil {
		return LatencySubsidyTask{}, err
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return LatencySubsidyTask{}, err
	}
	id, err := randomLatencySubsidyTaskID()
	if err != nil {
		return LatencySubsidyTask{}, err
	}
	now := time.Now().UTC()
	task := LatencySubsidyTask{
		ID:                   id,
		UserID:               userID,
		AdminAccountID:       adminAccountID,
		Name:                 input.Name,
		ThresholdMs:          input.ThresholdMs,
		ProfitRatio:          input.ProfitRatio,
		AutoEnabled:          input.AutoEnabled,
		WindowStart:          input.WindowStart,
		WindowEnd:            input.WindowEnd,
		RecurrenceType:       input.RecurrenceType,
		RecurrenceDaysOfWeek: input.RecurrenceDaysOfWeek,
		ValidFrom:            parseLatencySubsidyDate(input.ValidFrom),
		ValidUntil:           parseLatencySubsidyDate(input.ValidUntil),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := s.taskRepository.CreateLatencySubsidyTask(ctx, task); err != nil {
		return LatencySubsidyTask{}, err
	}
	return task, nil
}

// getOwnedLatencySubsidyTask loads a task and implicitly checks ownership —
// GetLatencySubsidyTask filters by user_id+admin_account_id, so a task
// belonging to someone else simply doesn't come back, same as "not found".
func (s *Service) getOwnedLatencySubsidyTask(ctx context.Context, userID, taskID string) (LatencySubsidyTask, error) {
	if s.taskRepository == nil {
		return LatencySubsidyTask{}, requestError(ErrorRequest)
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return LatencySubsidyTask{}, err
	}
	return s.taskRepository.GetLatencySubsidyTask(ctx, taskID, userID, adminAccountID)
}

// UpdateLatencySubsidyTask replaces a task's config. Running it again after
// this uses the new values — it is not versioned.
func (s *Service) UpdateLatencySubsidyTask(ctx context.Context, userID, taskID string, input LatencySubsidyTaskInput) (LatencySubsidyTask, error) {
	existing, err := s.getOwnedLatencySubsidyTask(ctx, userID, taskID)
	if err != nil {
		return LatencySubsidyTask{}, err
	}
	input, err = normalizeLatencySubsidyTaskInput(input)
	if err != nil {
		return LatencySubsidyTask{}, err
	}
	existing.Name = input.Name
	existing.ThresholdMs = input.ThresholdMs
	existing.ProfitRatio = input.ProfitRatio
	existing.AutoEnabled = input.AutoEnabled
	existing.WindowStart = input.WindowStart
	existing.WindowEnd = input.WindowEnd
	existing.RecurrenceType = input.RecurrenceType
	existing.RecurrenceDaysOfWeek = input.RecurrenceDaysOfWeek
	existing.ValidFrom = parseLatencySubsidyDate(input.ValidFrom)
	existing.ValidUntil = parseLatencySubsidyDate(input.ValidUntil)
	existing.UpdatedAt = time.Now().UTC()
	if err := s.taskRepository.UpdateLatencySubsidyTask(ctx, existing); err != nil {
		return LatencySubsidyTask{}, err
	}
	return existing, nil
}

// DeleteLatencySubsidyTask removes a task. It does not touch any payout
// already recorded under it — history keeps the task name it had at the
// time, even after the task itself is gone.
func (s *Service) DeleteLatencySubsidyTask(ctx context.Context, userID, taskID string) error {
	if _, err := s.getOwnedLatencySubsidyTask(ctx, userID, taskID); err != nil {
		return err
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return err
	}
	return s.taskRepository.DeleteLatencySubsidyTask(ctx, taskID, userID, adminAccountID)
}

// RunLatencySubsidyTaskPreview previews what running this task over [from,
// to) would pay out, using the task's own threshold/ratio. Returns the
// task's current name alongside the summary so the caller can label the
// preview without a second lookup.
func (s *Service) RunLatencySubsidyTaskPreview(ctx context.Context, userID, taskID string, from, to time.Time) (upstream.LatencyCompensationSummary, string, error) {
	task, err := s.getOwnedLatencySubsidyTask(ctx, userID, taskID)
	if err != nil {
		return upstream.LatencyCompensationSummary{}, "", err
	}
	summary, err := s.PreviewLatencyCompensation(ctx, userID, from, to, task.ThresholdMs, task.ProfitRatio)
	return summary, task.Name, err
}

// RunLatencySubsidyTaskApply actually pays out this task's compensation
// over [from, to). Irreversible — the caller must have already shown the
// operator a RunLatencySubsidyTaskPreview result and gotten confirmation.
func (s *Service) RunLatencySubsidyTaskApply(ctx context.Context, userID, taskID string, from, to time.Time) (upstream.LatencyCompensationSummary, error) {
	task, err := s.getOwnedLatencySubsidyTask(ctx, userID, taskID)
	if err != nil {
		return upstream.LatencyCompensationSummary{}, err
	}
	return s.ApplyLatencyCompensation(ctx, userID, from, to, task.ThresholdMs, task.ProfitRatio, task.ID, task.Name)
}

func (s *Service) findAdminGroup(ctx context.Context, state *State, groupID string) (upstream.AdminGroupInfo, error) {
	groups, err := s.platformService.FetchAdminAllGroups(state.Session)
	if err != nil {
		return upstream.AdminGroupInfo{}, err
	}
	groupID = strings.TrimSpace(groupID)
	for _, group := range groups {
		if group.ID == groupID || group.Name == groupID {
			return group, nil
		}
	}
	return upstream.AdminGroupInfo{}, requestError(ErrorRequest)
}

func (s *Service) resolveExistingAdminResource(ctx context.Context, state *State, groupID, resourceID string) (upstream.AdminGroupAccountInfo, []string, []string, error) {
	group, err := s.findAdminGroup(ctx, state, groupID)
	if err != nil {
		return upstream.AdminGroupAccountInfo{}, nil, nil, err
	}
	resources, err := s.platformService.ListAdminGroupAccounts(state.Session, group)
	if err != nil {
		return upstream.AdminGroupAccountInfo{}, nil, nil, err
	}
	var selected *upstream.AdminGroupAccountInfo
	for i := range resources {
		if resources[i].ID == strings.TrimSpace(resourceID) {
			selected = &resources[i]
			break
		}
	}
	if selected == nil {
		return upstream.AdminGroupAccountInfo{}, nil, nil, requestError(ErrorRequest)
	}
	groupIDs := selected.GroupIDs
	if len(groupIDs) == 0 {
		groupIDs = []string{group.ID}
	}
	returnResource := *selected
	ids, names, err := s.resolveAdminGroups(ctx, state, groupIDs)
	if err != nil {
		return upstream.AdminGroupAccountInfo{}, nil, nil, err
	}
	return returnResource, ids, names, nil
}

func (s *Service) resolveExistingCredential(site *upstream.Site, keys []upstream.Sub2APIKeyItem, keyID, groupID, groupName string, allowLegacy bool, legacyKey string) (string, error) {
	for _, item := range keys {
		if item.ID != strings.TrimSpace(keyID) {
			continue
		}
		if !allowLegacy && !credentialMatchesGroup(item, groupID, groupName) {
			return "", requestError(ErrorRequest)
		}
		if site.Session.Platform == upstream.PlatformNewAPI {
			return s.platformService.FetchNewAPITokenKey(*site.Session, item.ID)
		}
		if strings.TrimSpace(item.Key) == "" {
			return "", requestError(ErrorRequest)
		}
		return item.Key, nil
	}
	// Old clients historically sent the Sub2API key value themselves. Preserve
	// that fallback only for legacy requests without an admin resource selection.
	if allowLegacy && site.Session.Platform == upstream.PlatformSub2API && strings.TrimSpace(legacyKey) != "" {
		return strings.TrimSpace(legacyKey), nil
	}
	return "", requestError(ErrorRequest)
}

func (s *Service) realBindExisting(ctx context.Context, userID string, req RealBindRequest) (RealConnectResponse, error) {
	if strings.TrimSpace(req.UpstreamSiteID) == "" || strings.TrimSpace(req.UpstreamGroupID) == "" || strings.TrimSpace(req.UpstreamKeyID) == "" {
		return RealConnectResponse{}, requestError(ErrorRequest)
	}
	operationID, err := normalizeOperationID(req.OperationID)
	if err != nil {
		return RealConnectResponse{}, err
	}
	connectionCtx, err := s.prepareConnectionContext(ctx, userID, req.UpstreamSiteID, req.UpstreamGroupID, req.UpstreamGroupName, req.GroupType, false)
	if err != nil {
		return RealConnectResponse{}, err
	}
	if existing, err := s.idempotentConnection(ctx, userID, connectionCtx.adminAccountID, operationID); err != nil {
		return RealConnectResponse{}, err
	} else if existing != nil {
		return RealConnectResponse{Connection: publicRealConnection(*existing)}, nil
	}
	if err := s.rejectDuplicateTarget(ctx, userID, connectionCtx.adminAccountID, req.UpstreamSiteID, req.UpstreamGroupID, connectionCtx.groupName); err != nil {
		return RealConnectResponse{}, err
	}

	legacyRequest := strings.TrimSpace(req.AdminGroupID) == "" && strings.TrimSpace(req.AdminResourceID) == ""
	if !legacyRequest && (strings.TrimSpace(req.AdminGroupID) == "" || strings.TrimSpace(req.AdminResourceID) == "") {
		return RealConnectResponse{}, requestError(ErrorRequest)
	}
	_, keys, err := s.listOwnedUpstreamKeys(ctx, userID, connectionCtx.adminAccountID, req.UpstreamSiteID)
	if err != nil {
		return RealConnectResponse{}, err
	}
	key, err := s.resolveExistingCredential(connectionCtx.upstreamSite, keys, req.UpstreamKeyID, req.UpstreamGroupID, connectionCtx.groupName, legacyRequest, req.UpstreamKey)
	if err != nil {
		return RealConnectResponse{}, err
	}

	mode := ProvisioningModeExisting
	adminResourceID := strings.TrimSpace(req.AdminResourceID)
	adminResourceName := ""
	var ownGroupIDs, ownGroupNames []string
	if legacyRequest {
		if len(req.OwnGroupIDs) == 0 {
			return RealConnectResponse{}, requestError(ErrorRequest)
		}
		mode = ProvisioningModeLegacy
		ownGroupIDs, ownGroupNames, err = s.resolveAdminGroups(ctx, connectionCtx.state, req.OwnGroupIDs)
	} else {
		resource, resolvedIDs, resolvedNames, resolveErr := s.resolveExistingAdminResource(ctx, connectionCtx.state, req.AdminGroupID, req.AdminResourceID)
		if resolveErr != nil {
			return RealConnectResponse{}, resolveErr
		}
		adminResourceName = resource.Name
		ownGroupIDs, ownGroupNames = resolvedIDs, resolvedNames
	}
	if err != nil {
		return RealConnectResponse{}, err
	}

	connID, err := randomConnID()
	if err != nil {
		return RealConnectResponse{}, err
	}
	conn := RealConnection{
		ID: connID, UserID: userID, WorkspaceAdminAccountID: connectionCtx.adminAccountID,
		UpstreamSiteID: req.UpstreamSiteID, UpstreamGroupID: req.UpstreamGroupID,
		UpstreamGroupName: connectionCtx.groupName, UpstreamKeyID: strings.TrimSpace(req.UpstreamKeyID),
		UpstreamKey: key, AdminAccountID: adminResourceID, AdminAccountName: adminResourceName,
		OwnGroupIDs: ownGroupIDs, OwnGroupNames: ownGroupNames, GroupType: connectionCtx.groupType,
		ProvisioningMode: mode, Status: ConnectionStatusActive,
		UpstreamPlatform: string(connectionCtx.upstreamSession.Platform), AdminPlatform: string(connectionCtx.state.Session.Platform),
		PricingMappingEnabled: addToPricingMapping(req.AddToPricingMapping), OperationID: operationID,
		CanDeleteRemote: false, CreatedAt: time.Now().Format(time.RFC3339),
	}
	if err := s.persistConnection(ctx, conn); err != nil {
		return RealConnectResponse{}, err
	}
	return RealConnectResponse{Connection: publicRealConnection(conn)}, nil
}

func (s *Service) realDisconnectConnection(ctx context.Context, userID string, req RealDisconnectRequest) error {
	mode := strings.TrimSpace(req.Mode)
	// Older frontends used "full" for the destructive action. Keep accepting
	// it, but never delete the Admin forwarding account from a disconnect.
	if mode == "full" {
		mode = "delete-key"
	}
	if strings.TrimSpace(req.ConnectionID) == "" || (mode != "unlink" && mode != "delete-key") || s.connRepository == nil {
		return requestError(ErrorRequest)
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return err
	}
	conn, err := s.connRepository.GetRealConnection(ctx, req.ConnectionID, userID, adminAccountID)
	if err != nil {
		return err
	}
	if conn == nil {
		return requestError(ErrorRequest)
	}

	if mode == "delete-key" {
		if strings.TrimSpace(conn.UpstreamKeyID) == "" {
			return requestError(ErrorRequest)
		}
		upstreamSite, err := s.upstreamLookup.GetSite(ctx, conn.UpstreamSiteID)
		if err != nil || upstreamSite == nil || upstreamSite.Session == nil || upstreamSite.UserID != userID || upstreamSite.AdminAccountID != adminAccountID {
			return requestError(ErrorRequest)
		}
		upstreamSession := *upstreamSite.Session
		if conn.UpstreamPlatform != "" && conn.UpstreamPlatform != string(upstreamSession.Platform) {
			return requestError(ErrorRequest)
		}
		if err := s.deleteUpstreamCredential(upstreamSession, conn.UpstreamKeyID); err != nil && !upstream.IsNotFound(err) {
			return err
		}
	}

	removePricing := conn.PricingMappingEnabled
	if req.RemovePricingMapping != nil {
		removePricing = *req.RemovePricingMapping
	}
	if repository, ok := s.connRepository.(ScopedRealDisconnectRepository); ok {
		return repository.DeleteRealConnectionWithPricingMapping(ctx, *conn, removePricing)
	}
	if removePricing {
		return s.removeUpstreamMappingAndDeleteConnection(ctx, userID, adminAccountID, req.ConnectionID, conn.UpstreamSiteID, conn.UpstreamGroupName)
	}
	return s.connRepository.DeleteRealConnection(ctx, req.ConnectionID, userID, adminAccountID)
}

func publicRealConnection(conn RealConnection) RealConnection {
	conn.UpstreamKey = ""
	conn.OperationID = ""
	conn.CanDeleteRemote = conn.ProvisioningMode == ProvisioningModeManaged ||
		(conn.ProvisioningMode == ProvisioningModeLegacy && conn.AdminAccountID != "")
	return conn
}
