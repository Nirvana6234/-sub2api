namespace LanAi.RelayClient.Server;

/// <summary>
/// The relay's HTTP surface, as this client uses it.
/// </summary>
/// <remarks>
/// Every method throws <see cref="RelayApiException"/> on failure; none return
/// null to signal an error. Bearer tokens are passed explicitly rather than held
/// as client state, so token lifetime stays the session layer's concern.
/// </remarks>
public interface IRelayServerClient
{
    /// <summary>Reads the flags that decide which sign-in and sign-up controls exist (F1.7).</summary>
    Task<PublicSettings> GetPublicSettingsAsync(CancellationToken cancellationToken = default);

    /// <summary>Signs in with email and password. May return a two-factor demand instead of tokens.</summary>
    Task<LoginOutcome> LoginAsync(string email, string password, CancellationToken cancellationToken = default);

    /// <summary>Completes a sign-in that required a second factor.</summary>
    Task<AuthTokens> CompleteTwoFactorAsync(string tempToken, string totpCode, CancellationToken cancellationToken = default);

    /// <summary>Creates an account. On success the caller should sign in without asking again (F1'.3).</summary>
    Task<AuthTokens> RegisterAsync(RegistrationRequest request, CancellationToken cancellationToken = default);

    /// <summary>Asks the server to email a verification code.</summary>
    Task<VerifyCodeDispatch> SendVerifyCodeAsync(string email, string? turnstileToken, CancellationToken cancellationToken = default);

    /// <summary>Exchanges a refresh token for a fresh pair.</summary>
    Task<AuthTokens> RefreshAsync(string refreshToken, CancellationToken cancellationToken = default);

    /// <summary>Revokes a refresh token. Best-effort: sign-out must not be blocked by a failure here.</summary>
    Task LogoutAsync(string refreshToken, CancellationToken cancellationToken = default);

    /// <summary>Reads the signed-in user.</summary>
    Task<RelayUser> GetCurrentUserAsync(string accessToken, CancellationToken cancellationToken = default);

    /// <summary>Reads today's and cumulative usage totals for the account card (F4).</summary>
    Task<DashboardStats> GetDashboardStatsAsync(string accessToken, CancellationToken cancellationToken = default);

    Task<PaymentCheckoutInfo> GetCheckoutInfoAsync(string accessToken, CancellationToken cancellationToken = default);

    Task<PaymentOrderCreateResult> CreateBalanceOrderAsync(
        string accessToken,
        decimal amount,
        string paymentType,
        CancellationToken cancellationToken = default);

    Task<PaymentOrder> GetPaymentOrderAsync(
        string accessToken,
        long orderId,
        CancellationToken cancellationToken = default);

    Task<PaymentOrder> VerifyPaymentOrderAsync(
        string accessToken,
        string outTradeNo,
        CancellationToken cancellationToken = default);

    Task CancelPaymentOrderAsync(
        string accessToken,
        long orderId,
        CancellationToken cancellationToken = default);

    /// <summary>Reads active subscriptions and their limits; empty when the user has none (F4).</summary>
    Task<IReadOnlyList<SubscriptionSummaryItem>> GetSubscriptionSummaryAsync(
        string accessToken,
        CancellationToken cancellationToken = default);

    /// <summary>
    /// Lists the groups this user may bind a key to (F5.3).
    /// </summary>
    /// <remarks>
    /// Already filtered server-side by subscription and permission. The client
    /// must not add groups back in; it may only narrow the list further, e.g. by
    /// platform.
    /// </remarks>
    Task<IReadOnlyList<RelayGroup>> GetAvailableGroupsAsync(
        string accessToken,
        CancellationToken cancellationToken = default);

    /// <summary>
    /// Reads this user's per-group multiplier overrides, keyed by group id (F5.2).
    /// </summary>
    /// <remarks>
    /// A group missing from the map has no override. A group present with
    /// <c>0</c> is charged nothing — the two are different and must stay so.
    /// </remarks>
    Task<IReadOnlyDictionary<long, double>> GetUserGroupRatesAsync(
        string accessToken,
        CancellationToken cancellationToken = default);

    /// <summary>Reads the recent daily usage trend for the chart (F4).</summary>
    /// <param name="days">How many days back to cover, ending today.</param>
    Task<IReadOnlyList<UsageTrendPoint>> GetUsageTrendAsync(
        string accessToken,
        int days,
        CancellationToken cancellationToken = default);

    /// <summary>Reads usage split by model over the same window (F4).</summary>
    Task<IReadOnlyList<ModelUsage>> GetModelUsageAsync(
        string accessToken,
        int days,
        CancellationToken cancellationToken = default);

    /// <summary>Lists the user's API keys, used to recognise the one this client manages (F3.2.1).</summary>
    Task<IReadOnlyList<RelayApiKey>> ListApiKeysAsync(
        string accessToken,
        CancellationToken cancellationToken = default);

    /// <summary>
    /// Issues the managed key that authorises Codex, as a one-day lease (F3.2.2).
    /// </summary>
    /// <param name="name">The F3.2.1 name, which is how the client recognises the key later.</param>
    /// <param name="groupId">The group to bill against; null leaves the server's default.</param>
    Task<RelayApiKey> CreateApiKeyAsync(
        string accessToken,
        string name,
        long? groupId,
        int expiresInDays,
        CancellationToken cancellationToken = default);

    /// <summary>
    /// Rolls the lease forward to <paramref name="expiresAt"/> (F3.2.3).
    /// </summary>
    /// <remarks>
    /// Sends <c>expires_at</c> and nothing else, so a renewal cannot disturb the
    /// key's group or any other property.
    /// </remarks>
    Task<RelayApiKey> RenewApiKeyAsync(
        string accessToken,
        long keyId,
        DateTimeOffset expiresAt,
        CancellationToken cancellationToken = default);

    /// <summary>Revokes one managed key during sign-out or a real client exit.</summary>
    Task DeleteApiKeyAsync(
        string accessToken,
        long keyId,
        CancellationToken cancellationToken = default);

    /// <summary>
    /// Rebinds one API key to a different group (F5.4).
    /// </summary>
    /// <remarks>
    /// Changes the group and nothing else — see the implementation for why that
    /// is a safety property rather than a matter of taste.
    /// </remarks>
    Task<RelayApiKey> UpdateApiKeyGroupAsync(
        string accessToken,
        long keyId,
        long groupId,
        CancellationToken cancellationToken = default);

    /// <summary>Summarises the announcement list without pulling any bodies.</summary>
    /// <remarks>
    /// Added after the client shipped, so a server that predates it answers 404.
    /// Callers must treat that as "probing unavailable" and fall back to
    /// <see cref="ListAnnouncementsAsync"/> rather than reporting an error —
    /// otherwise a client newer than its relay loses announcements entirely.
    /// </remarks>
    Task<AnnouncementHead> GetAnnouncementHeadAsync(
        string accessToken,
        CancellationToken cancellationToken = default);

    /// <summary>Lists the announcements visible to this user, newest-unread first.</summary>
    /// <remarks>
    /// Returns read and unread alike: the client needs the read ones to populate
    /// the list view, and needs the read state to keep its unread badge honest.
    /// </remarks>
    Task<IReadOnlyList<RelayAnnouncement>> ListAnnouncementsAsync(
        string accessToken,
        CancellationToken cancellationToken = default);

    /// <summary>Marks one announcement read for this user.</summary>
    /// <remarks>
    /// The server re-checks visibility and rejects an announcement this user was
    /// never eligible for, so a stale id fails rather than silently succeeding.
    /// </remarks>
    Task MarkAnnouncementReadAsync(
        string accessToken,
        long announcementId,
        CancellationToken cancellationToken = default);

    /// <summary>Gets the current user Claude preference (model + thinking level).</summary>
    Task<ClaudePreferenceDto> GetClaudePreferenceAsync(
        string accessToken,
        CancellationToken cancellationToken = default);

    /// <summary>Saves the user Claude preference.</summary>
    Task SetClaudePreferenceAsync(
        string accessToken,
        string model,
        string thinkingLevel,
        CancellationToken cancellationToken = default);
}