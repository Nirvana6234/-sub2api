using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Tests;

/// <summary>A relay whose answers the test dictates.</summary>
internal sealed class FakeRelayClient : IRelayServerClient
{
    public Func<LoginOutcome>? OnLogin { get; set; }

    public Func<AuthTokens>? OnRefresh { get; set; }

    public Func<AuthTokens>? OnTwoFactor { get; set; }

    public Func<AuthTokens>? OnRegister { get; set; }

    public RegistrationRequest? LastRegistration { get; private set; }

    public string? LastVerifyEmail { get; private set; }

    public int VerifyCodeCallCount { get; private set; }

    public Func<RegistrationRequest, AuthTokens>? OnRegisterRequest { get; set; }

    public Func<string, VerifyCodeDispatch>? OnVerifyCodeRequest { get; set; }

    public Action? OnLogout { get; set; }

    public int RefreshCallCount { get; private set; }

    public int LogoutCallCount { get; private set; }

    public Task<PublicSettings> GetPublicSettingsAsync(CancellationToken cancellationToken = default) =>
        Task.FromResult(PublicSettings.Conservative);

    public Task<LoginOutcome> LoginAsync(string email, string password, CancellationToken cancellationToken = default) =>
        Task.FromResult(OnLogin?.Invoke() ?? LoginOutcome.Authenticated(Tokens("at")));

    public Task<AuthTokens> CompleteTwoFactorAsync(string tempToken, string totpCode, CancellationToken cancellationToken = default) =>
        Task.FromResult(OnTwoFactor?.Invoke() ?? Tokens("at-2fa"));

    public Task<AuthTokens> RegisterAsync(RegistrationRequest request, CancellationToken cancellationToken = default)
    {
        LastRegistration = request;
        return Task.FromResult(OnRegisterRequest?.Invoke(request) ?? OnRegister?.Invoke() ?? Tokens("at-new"));
    }

    public Task<VerifyCodeDispatch> SendVerifyCodeAsync(string email, string? turnstileToken, CancellationToken cancellationToken = default)
    {
        LastVerifyEmail = email;
        VerifyCodeCallCount++;
        return Task.FromResult(OnVerifyCodeRequest?.Invoke(email) ?? new VerifyCodeDispatch { CountdownSeconds = 60 });
    }

    public Task<AuthTokens> RefreshAsync(string refreshToken, CancellationToken cancellationToken = default)
    {
        RefreshCallCount++;
        return Task.FromResult(OnRefresh?.Invoke() ?? Tokens("at-renewed"));
    }

    public Task LogoutAsync(string refreshToken, CancellationToken cancellationToken = default)
    {
        LogoutCallCount++;
        OnLogout?.Invoke();
        return Task.CompletedTask;
    }

    public Func<RelayUser>? OnCurrentUser { get; set; }

    public Func<CancellationToken, Task<RelayUser>>? OnCurrentUserAsync { get; set; }

    public int CurrentUserCallCount { get; private set; }

    public async Task<RelayUser> GetCurrentUserAsync(string accessToken, CancellationToken cancellationToken = default)
    {
        CurrentUserCallCount++;
        if (OnCurrentUserAsync is not null)
        {
            return await OnCurrentUserAsync(cancellationToken).ConfigureAwait(false);
        }

        return OnCurrentUser?.Invoke() ?? new RelayUser { Email = "a@b.com", Username = "ann" };
    }

    public Func<DashboardStats>? OnDashboardStats { get; set; }

    public int DashboardStatsCallCount { get; private set; }

    public Func<IReadOnlyList<RelayGroup>>? OnAvailableGroups { get; set; }

    public Func<IReadOnlyDictionary<long, double>>? OnGroupRates { get; set; }

    public Func<IReadOnlyList<RelayApiKey>>? OnListKeys { get; set; }

    public int ListKeysCallCount { get; private set; }

    public Func<long, RelayApiKey>? OnUpdateKeyGroup { get; set; }

    public Task<DashboardStats> GetDashboardStatsAsync(string accessToken, CancellationToken cancellationToken = default)
    {
        DashboardStatsCallCount++;
        return Task.FromResult(OnDashboardStats?.Invoke() ?? new DashboardStats());
    }

    public Func<PaymentCheckoutInfo>? OnCheckoutInfo { get; set; }

    public Task<PaymentCheckoutInfo> GetCheckoutInfoAsync(string accessToken, CancellationToken cancellationToken = default) =>
        Task.FromResult(OnCheckoutInfo?.Invoke() ?? new PaymentCheckoutInfo());

    public Func<decimal, string, PaymentOrderCreateResult>? OnCreateBalanceOrder { get; set; }

    public decimal? LastCreatedAmount { get; private set; }

    public string? LastCreatedPaymentType { get; private set; }

    public Task<PaymentOrderCreateResult> CreateBalanceOrderAsync(
        string accessToken,
        decimal amount,
        string paymentType,
        CancellationToken cancellationToken = default)
    {
        LastCreatedAmount = amount;
        LastCreatedPaymentType = paymentType;
        return Task.FromResult(OnCreateBalanceOrder?.Invoke(amount, paymentType) ?? new PaymentOrderCreateResult());
    }

    public Func<long, PaymentOrder>? OnGetPaymentOrder { get; set; }

    public Task<PaymentOrder> GetPaymentOrderAsync(string accessToken, long orderId, CancellationToken cancellationToken = default) =>
        Task.FromResult(OnGetPaymentOrder?.Invoke(orderId) ?? new PaymentOrder { Id = orderId, Status = PaymentOrderStatus.Pending });

    public Func<string, PaymentOrder>? OnVerifyPaymentOrder { get; set; }

    public Task<PaymentOrder> VerifyPaymentOrderAsync(string accessToken, string outTradeNo, CancellationToken cancellationToken = default) =>
        Task.FromResult(OnVerifyPaymentOrder?.Invoke(outTradeNo) ?? new PaymentOrder { OutTradeNo = outTradeNo, Status = PaymentOrderStatus.Pending });

    public Action<long>? OnCancelPaymentOrder { get; set; }

    public long? LastCancelledOrderId { get; private set; }

    public Task CancelPaymentOrderAsync(string accessToken, long orderId, CancellationToken cancellationToken = default)
    {
        LastCancelledOrderId = orderId;
        OnCancelPaymentOrder?.Invoke(orderId);
        return Task.CompletedTask;
    }

    public int SubscriptionSummaryCallCount { get; private set; }

    public Task<IReadOnlyList<SubscriptionSummaryItem>> GetSubscriptionSummaryAsync(string accessToken, CancellationToken cancellationToken = default)
    {
        SubscriptionSummaryCallCount++;
        return Task.FromResult(OnSubscriptionSummary?.Invoke() ?? Array.Empty<SubscriptionSummaryItem>());
    }

    public Func<IReadOnlyList<SubscriptionSummaryItem>>? OnSubscriptionSummary { get; set; }

    public int AvailableGroupsCallCount { get; private set; }

    public Task<IReadOnlyList<RelayGroup>> GetAvailableGroupsAsync(string accessToken, CancellationToken cancellationToken = default)
    {
        AvailableGroupsCallCount++;
        return Task.FromResult(OnAvailableGroups?.Invoke() ?? Array.Empty<RelayGroup>());
    }

    public int GroupRatesCallCount { get; private set; }

    public Task<IReadOnlyDictionary<long, double>> GetUserGroupRatesAsync(string accessToken, CancellationToken cancellationToken = default)
    {
        GroupRatesCallCount++;
        return Task.FromResult(OnGroupRates?.Invoke() ?? new Dictionary<long, double>());
    }

    public Task<IReadOnlyList<RelayApiKey>> ListApiKeysAsync(string accessToken, CancellationToken cancellationToken = default)
    {
        ListKeysCallCount++;
        return Task.FromResult(OnListKeys?.Invoke() ?? Array.Empty<RelayApiKey>());
    }

    public Func<IReadOnlyList<UsageTrendPoint>>? OnUsageTrend { get; set; }

    public Func<IReadOnlyList<ModelUsage>>? OnModelUsage { get; set; }

    public int UsageTrendCallCount { get; private set; }

    public Task<IReadOnlyList<UsageTrendPoint>> GetUsageTrendAsync(string accessToken, int days, CancellationToken cancellationToken = default)
    {
        UsageTrendCallCount++;
        return Task.FromResult(OnUsageTrend?.Invoke() ?? Array.Empty<UsageTrendPoint>());
    }

    public int ModelUsageCallCount { get; private set; }

    public Task<IReadOnlyList<ModelUsage>> GetModelUsageAsync(string accessToken, int days, CancellationToken cancellationToken = default)
    {
        ModelUsageCallCount++;
        return Task.FromResult(OnModelUsage?.Invoke() ?? Array.Empty<ModelUsage>());
    }

    public Func<string, long?, RelayApiKey>? OnCreateKey { get; set; }

    public Task<RelayApiKey> CreateApiKeyAsync(string accessToken, string name, long? groupId, int expiresInDays, CancellationToken cancellationToken = default) =>
        Task.FromResult(OnCreateKey?.Invoke(name, groupId)
            ?? new RelayApiKey
            {
                Id = 99,
                Name = name,
                Key = "sk-issued",
                GroupId = groupId,
                ExpiresAt = DateTimeOffset.UtcNow.AddDays(expiresInDays),
            });

    public Func<long, DateTimeOffset, RelayApiKey>? OnRenewKey { get; set; }

    public Action<long>? OnDeleteKey { get; set; }

    public int DeleteKeyCallCount { get; private set; }

    public long? LastDeletedKeyId { get; private set; }

    public List<long> DeletedKeyIds { get; } = [];

    public Task<RelayApiKey> RenewApiKeyAsync(string accessToken, long keyId, DateTimeOffset expiresAt, CancellationToken cancellationToken = default) =>
        Task.FromResult(OnRenewKey?.Invoke(keyId, expiresAt)
            ?? new RelayApiKey { Id = keyId, ExpiresAt = expiresAt });

    public Task DeleteApiKeyAsync(string accessToken, long keyId, CancellationToken cancellationToken = default)
    {
        DeleteKeyCallCount++;
        LastDeletedKeyId = keyId;
        DeletedKeyIds.Add(keyId);
        OnDeleteKey?.Invoke(keyId);
        return Task.CompletedTask;
    }

    public Task<RelayApiKey> UpdateApiKeyGroupAsync(string accessToken, long keyId, long groupId, CancellationToken cancellationToken = default) =>
        Task.FromResult(OnUpdateKeyGroup?.Invoke(groupId) ?? new RelayApiKey { Id = keyId, GroupId = groupId });

    public ClaudePreferenceDto ClaudePreference { get; set; } = new();

    public int ClaudePreferenceGetCallCount { get; private set; }

    public int ClaudePreferenceSetCallCount { get; private set; }

    public Task<ClaudePreferenceDto> GetClaudePreferenceAsync(string accessToken, CancellationToken cancellationToken = default)
    {
        ClaudePreferenceGetCallCount++;
        return Task.FromResult(ClaudePreference);
    }

    public Task SetClaudePreferenceAsync(
        string accessToken,
        string model,
        string thinkingLevel,
        CancellationToken cancellationToken = default)
    {
        ClaudePreferenceSetCallCount++;
        ClaudePreference = new ClaudePreferenceDto { Model = model, ThinkingLevel = thinkingLevel };
        return Task.CompletedTask;
    }

    /// <summary>What the next announcement poll returns.</summary>
    public IReadOnlyList<RelayAnnouncement> Announcements { get; set; } = [];

    /// <summary>Set to make one poll fail, exercising the "leave the list alone" path.</summary>
    public Func<Exception>? OnListAnnouncements { get; set; }

    public int ListAnnouncementsCallCount { get; private set; }

    public List<long> MarkedReadIds { get; } = [];

    public Task<IReadOnlyList<RelayAnnouncement>> ListAnnouncementsAsync(
        string accessToken,
        CancellationToken cancellationToken = default)
    {
        ListAnnouncementsCallCount++;
        if (OnListAnnouncements is not null)
        {
            throw OnListAnnouncements();
        }

        return Task.FromResult(Announcements);
    }

    public int AnnouncementHeadCallCount { get; private set; }

    /// <summary>Set to simulate a relay that predates the summary endpoint.</summary>
    public Func<Exception>? OnAnnouncementHead { get; set; }

    /// <summary>
    /// Derived from <see cref="Announcements"/> so the fake cannot claim a summary
    /// that disagrees with the list it would hand back.
    /// </summary>
    public Task<AnnouncementHead> GetAnnouncementHeadAsync(
        string accessToken,
        CancellationToken cancellationToken = default)
    {
        AnnouncementHeadCallCount++;
        if (OnAnnouncementHead is not null)
        {
            throw OnAnnouncementHead();
        }

        return Task.FromResult(new AnnouncementHead
        {
            MaxId = Announcements.Count == 0 ? 0 : Announcements.Max(item => item.Id),
            UnreadCount = Announcements.Count(item => item.IsUnread),
            Total = Announcements.Count,
        });
    }

    public Task MarkAnnouncementReadAsync(
        string accessToken,
        long announcementId,
        CancellationToken cancellationToken = default)
    {
        MarkedReadIds.Add(announcementId);
        return Task.CompletedTask;
    }

    public static AuthTokens Tokens(
        string accessToken,
        string refreshToken = "rt",
        int expiresIn = 3600,
        string? email = "a@b.com",
        string? username = "ann") =>
        new()
        {
            AccessToken = accessToken,
            RefreshToken = refreshToken,
            ExpiresInSeconds = expiresIn,
            TokenType = "Bearer",
            User = email is null ? null : new RelayUser { Email = email, Username = username ?? string.Empty },
        };
}

/// <summary>An in-memory session store, so tests never touch DPAPI or the disk.</summary>
internal sealed class FakeSessionStore : ISessionStore
{
    public StoredSession? Current { get; set; }

    public int ClearCallCount { get; private set; }

    public StoredSession? Load() => Current;

    public void Save(StoredSession session) => Current = session;

    public void Clear()
    {
        ClearCallCount++;
        Current = null;
    }
}

/// <summary>A clock the test moves by hand.</summary>
internal sealed class TestClock
{
    public DateTimeOffset Now { get; set; } = new(2026, 7, 31, 12, 0, 0, TimeSpan.Zero);

    public Func<DateTimeOffset> Read => () => Now;

    public void Advance(TimeSpan amount) => Now += amount;
}

/// <summary>A Codex startup whose outcome the test dictates.</summary>
internal sealed class FakeCodexStartup : ICodexStartup
{
    public Func<long?, bool, CodexStartupResult>? OnRun { get; set; }

    public int RunCount { get; private set; }

    public bool LastAllowRestart { get; private set; }

    public string? LastPreferredModel { get; private set; }

    public Task<CodexStartupResult> RunAsync(
        long? groupId,
        string apiBaseUrl,
        bool allowRestart = false,
        CancellationToken cancellationToken = default,
        string? preferredModel = null)
    {
        RunCount++;
        LastAllowRestart = allowRestart;
        LastPreferredModel = preferredModel;

        return Task.FromResult(OnRun?.Invoke(groupId, allowRestart)
            ?? new CodexStartupResult(CodexStartupStatus.Ready, "ChatGPT 已就绪，可以开始对话了。"));
    }

    public Func<CodexHealth>? OnCheck { get; set; }

    public bool IsInstalled { get; set; } = true;

    public Func<bool>? OnInstalled { get; set; }

    public int CheckCallCount { get; private set; }

    public Func<DateTimeOffset?>? OnRenew { get; set; }

    public int RenewCallCount { get; private set; }

    public int ReleaseCallCount { get; private set; }

    public Func<Task>? OnRelease { get; set; }

    public Task<CodexHealth> CheckAsync(CancellationToken cancellationToken = default)
    {
        CheckCallCount++;
        return Task.FromResult(OnCheck?.Invoke() ?? new CodexHealth(true, false, null));
    }

    public Task<bool> CheckInstalledAsync(CancellationToken cancellationToken = default) =>
        Task.FromResult(OnInstalled?.Invoke() ?? IsInstalled);

    public Task<DateTimeOffset?> RenewLeaseIfDueAsync(CancellationToken cancellationToken = default)
    {
        RenewCallCount++;
        return Task.FromResult(OnRenew?.Invoke());
    }

    public async Task ReleaseAsync(CancellationToken cancellationToken = default)
    {
        ReleaseCallCount++;
        if (OnRelease is not null)
        {
            await OnRelease().ConfigureAwait(false);
        }
    }
}

internal sealed class FakeCodexAccountStore : ICodexAccountStore
{
    public string? Email { get; set; }

    public int SaveCallCount { get; private set; }

    public string? Load() => Email;

    public void Save(string email)
    {
        SaveCallCount++;
        Email = email;
    }
}

internal sealed class FakeCodexInstaller : ICodexInstaller
{
    public CodexInstallerInspection Inspection { get; set; } = new(true, "C:\\codex-installer", "C:\\codex-installer\\Codex.msix", "found");

    public CodexInstallerResult LaunchResult { get; set; } = new(true, "started");

    public CodexInstallerResult EnsureAndLaunchResult { get; set; } = new(true, "started");

    public Func<IProgress<CodexDownloadProgress>?, CancellationToken, Task<CodexInstallerResult>>? OnEnsureAndLaunch { get; set; }

    public int LaunchCallCount { get; private set; }

    public int EnsureAndLaunchCallCount { get; private set; }

    public CodexInstallerInspection Inspect() => Inspection;

    public CodexInstallerResult Launch()
    {
        LaunchCallCount++;
        return LaunchResult;
    }

    public Task<CodexInstallerResult> EnsureAndLaunchAsync(
        IProgress<CodexDownloadProgress>? progress = null,
        CancellationToken cancellationToken = default)
    {
        EnsureAndLaunchCallCount++;
        if (OnEnsureAndLaunch is not null)
        {
            return OnEnsureAndLaunch(progress, cancellationToken);
        }

        progress?.Report(new CodexDownloadProgress(1, 1));
        return Task.FromResult(EnsureAndLaunchResult);
    }
}
