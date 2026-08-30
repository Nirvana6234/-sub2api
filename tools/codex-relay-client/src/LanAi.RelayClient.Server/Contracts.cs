using System.Text.Json.Serialization;

namespace LanAi.RelayClient.Server;

/// <summary>
/// The server-driven shape of the sign-in and sign-up surface.
/// </summary>
/// <remarks>
/// The requirements make this a hard rule (F1.7): the client must not hard-code
/// which fields exist. Every flag below decides whether some control is shown at
/// all, so that an operator changing a server setting does not require a client
/// release. Mirrors <c>dto.PublicSettings</c>; only the fields this client
/// actually consumes are mapped.
/// </remarks>
public sealed record PublicSettings
{
    [JsonConstructor]
    public PublicSettings(
        bool registrationEnabled = default,
        bool emailVerifyEnabled = default,
        IReadOnlyList<string>? registrationEmailSuffixWhitelist = null,
        bool invitationCodeEnabled = default,
        bool promoCodeEnabled = default,
        bool passwordResetEnabled = default,
        bool totpEnabled = default,
        bool turnstileEnabled = default,
        string? turnstileSiteKey = default,
        bool loginAgreementEnabled = default,
        string? loginAgreementMode = default,
        IReadOnlyList<LoginAgreementDocument>? loginAgreementDocuments = null,
        bool paymentEnabled = default,
        bool backendModeEnabled = default,
        string? siteName = default,
        string? siteLogo = default,
        string? apiBaseUrl = default,
        string? contactInfo = default,
        bool balanceLowNotifyEnabled = default,
        double balanceLowNotifyThreshold = default,
        string? balanceLowNotifyRechargeUrl = default,
        string? serverUtcOffset = default,
        bool clientDownloadEnabled = default,
        string? clientLatestVersion = default,
        string? clientLatestVersionMac = default)
    {
        RegistrationEnabled = registrationEnabled;
        EmailVerifyEnabled = emailVerifyEnabled;
        RegistrationEmailSuffixWhitelist = registrationEmailSuffixWhitelist ?? Array.Empty<string>();
        InvitationCodeEnabled = invitationCodeEnabled;
        PromoCodeEnabled = promoCodeEnabled;
        PasswordResetEnabled = passwordResetEnabled;
        TotpEnabled = totpEnabled;
        TurnstileEnabled = turnstileEnabled;
        TurnstileSiteKey = turnstileSiteKey;
        LoginAgreementEnabled = loginAgreementEnabled;
        LoginAgreementMode = loginAgreementMode;
        LoginAgreementDocuments = loginAgreementDocuments ?? Array.Empty<LoginAgreementDocument>();
        PaymentEnabled = paymentEnabled;
        BackendModeEnabled = backendModeEnabled;
        SiteName = siteName;
        SiteLogo = siteLogo;
        ApiBaseUrl = apiBaseUrl;
        ContactInfo = contactInfo;
        BalanceLowNotifyEnabled = balanceLowNotifyEnabled;
        BalanceLowNotifyThreshold = balanceLowNotifyThreshold;
        BalanceLowNotifyRechargeUrl = balanceLowNotifyRechargeUrl;
        ServerUtcOffset = serverUtcOffset;
        ClientDownloadEnabled = clientDownloadEnabled;
        ClientLatestVersion = clientLatestVersion;
        ClientLatestVersionMac = clientLatestVersionMac;
    }

    [JsonPropertyName("registration_enabled")]
    public bool RegistrationEnabled { get; init; }

    [JsonPropertyName("email_verify_enabled")]
    public bool EmailVerifyEnabled { get; init; }

    /// <summary>
    /// Email suffixes the server will accept at registration; empty means unrestricted.
    /// </summary>
    /// <remarks>
    /// A JSON array, not a delimited string — see <c>dto.PublicSettings</c>
    /// (<c>[]string</c>). Binding it as a string makes the whole settings fetch
    /// throw, which would take the sign-in surface down with it.
    /// </remarks>
    [JsonPropertyName("registration_email_suffix_whitelist")]
    public IReadOnlyList<string> RegistrationEmailSuffixWhitelist { get; init; } = Array.Empty<string>();

    [JsonPropertyName("invitation_code_enabled")]
    public bool InvitationCodeEnabled { get; init; }

    [JsonPropertyName("promo_code_enabled")]
    public bool PromoCodeEnabled { get; init; }

    [JsonPropertyName("password_reset_enabled")]
    public bool PasswordResetEnabled { get; init; }

    [JsonPropertyName("totp_enabled")]
    public bool TotpEnabled { get; init; }

    [JsonPropertyName("turnstile_enabled")]
    public bool TurnstileEnabled { get; init; }

    [JsonPropertyName("turnstile_site_key")]
    public string? TurnstileSiteKey { get; init; }

    [JsonPropertyName("login_agreement_enabled")]
    public bool LoginAgreementEnabled { get; init; }

    /// <summary>How the agreement must be presented, e.g. <c>modal</c>.</summary>
    [JsonPropertyName("login_agreement_mode")]
    public string? LoginAgreementMode { get; init; }

    /// <summary>The documents the user has to be able to read before consenting (F1.9).</summary>
    [JsonPropertyName("login_agreement_documents")]
    public IReadOnlyList<LoginAgreementDocument> LoginAgreementDocuments { get; init; } =
        Array.Empty<LoginAgreementDocument>();

    [JsonPropertyName("payment_enabled")]
    public bool PaymentEnabled { get; init; }

    /// <summary>
    /// Whether the server runs in "backend mode".
    /// </summary>
    /// <remarks>
    /// When on, the panel guards reject ordinary user traffic. Mapping it lets the
    /// client say what happened instead of surfacing an unexplained rejection on
    /// every call after a successful sign-in.
    /// </remarks>
    [JsonPropertyName("backend_mode_enabled")]
    public bool BackendModeEnabled { get; init; }

    [JsonPropertyName("site_name")]
    public string? SiteName { get; init; }

    [JsonPropertyName("site_logo")]
    public string? SiteLogo { get; init; }

    /// <summary>
    /// The public base URL of the relay's OpenAI-compatible endpoint.
    /// </summary>
    /// <remarks>
    /// This is what gets written into <c>config.toml</c> as the custom provider's
    /// base URL, so it must come from the server rather than being derived from
    /// whatever address the user typed in settings.
    /// </remarks>
    [JsonPropertyName("api_base_url")]
    public string? ApiBaseUrl { get; init; }

    /// <summary>Operator contact details; the only support route the client offers for refunds.</summary>
    [JsonPropertyName("contact_info")]
    public string? ContactInfo { get; init; }

    [JsonPropertyName("balance_low_notify_enabled")]
    public bool BalanceLowNotifyEnabled { get; init; }

    /// <summary>Balance below which the client warns and offers to top up. Not nullable server-side.</summary>
    [JsonPropertyName("balance_low_notify_threshold")]
    public double BalanceLowNotifyThreshold { get; init; }

    [JsonPropertyName("balance_low_notify_recharge_url")]
    public string? BalanceLowNotifyRechargeUrl { get; init; }

    /// <summary>
    /// The server's own UTC offset, e.g. <c>+08:00</c>.
    /// </summary>
    /// <remarks>
    /// Peak-rate windows are evaluated by the server in <em>its</em> timezone
    /// (<c>Group.PeakMultiplierAt</c> calls <c>now.In(timezone.Location())</c>),
    /// not the user's and not the machine's. Showing a window without saying which
    /// clock it refers to invites a user in another timezone to read the billing
    /// window off by hours, so this label travels with every window the UI renders.
    /// </remarks>
    [JsonPropertyName("server_utc_offset")]
    public string? ServerUtcOffset { get; init; }

    /// <summary>Whether the site's own /download page is reachable.</summary>
    /// <remarks>
    /// Gates the update banner. The route is disabled server-side when this is
    /// false (<c>router/index.ts</c>), so offering "点击更新" without checking it
    /// sends the user to a page that refuses to render — an update prompt that
    /// cannot be acted on is worse than none.
    /// </remarks>
    [JsonPropertyName("client_download_enabled")]
    public bool ClientDownloadEnabled { get; init; }

    /// <summary>Newest client version published for Windows; empty means "do not advertise".</summary>
    /// <remarks>
    /// Server-driven for the same reason every other field here is (F1.7), but with
    /// a sharper edge: this used to live in <c>client-version.json</c>, a static file
    /// embedded into the backend binary, while the package it advertises is a plain
    /// settings row. Changing one took a full rebuild and redeploy and the other took
    /// a form field, so the two drifted — and the failure was silent, because every
    /// error path in the checker returns "no update".
    /// </remarks>
    [JsonPropertyName("client_latest_version")]
    public string? ClientLatestVersion { get; init; }

    /// <summary>Newest client version published for macOS; empty means "do not advertise".</summary>
    /// <remarks>
    /// Separate from <see cref="ClientLatestVersion"/> because the two platforms do
    /// not ship together. One shared number would tell every Mac user to upgrade the
    /// moment Windows shipped, and send them to a download page with nothing on it
    /// for them.
    /// </remarks>
    [JsonPropertyName("client_latest_version_mac")]
    public string? ClientLatestVersionMac { get; init; }

    /// <summary>
    /// The safest possible surface, used when <c>/settings/public</c> cannot be read.
    /// </summary>
    /// <remarks>
    /// F1.7 requires falling back to the most conservative form rather than
    /// guessing: sign-in only, no registration entry, no optional fields.
    /// </remarks>
    public static PublicSettings Conservative { get; } = new();

    /// <summary>
    /// Whether <paramref name="email"/> passes the server's suffix whitelist.
    /// </summary>
    /// <remarks>
    /// Used to warn while the user is still typing (F1'.1 acceptance criteria)
    /// rather than after a rejected submit. Advisory only — the server remains
    /// the authority, and an empty whitelist means "no restriction".
    /// </remarks>
    public bool IsEmailSuffixAllowed(string? email)
    {
        if (RegistrationEmailSuffixWhitelist.Count == 0)
        {
            return true;
        }

        if (string.IsNullOrWhiteSpace(email))
        {
            return false;
        }

        return RegistrationEmailSuffixWhitelist.Any(suffix =>
            !string.IsNullOrWhiteSpace(suffix) &&
            email.EndsWith(suffix.Trim(), StringComparison.OrdinalIgnoreCase));
    }
}

/// <summary>One agreement document the user must be able to read before consenting.</summary>
/// <remarks>
/// <c>content_md</c> is markdown and can legitimately be empty — the live server
/// returns four documents with empty bodies. An empty body means "nothing to
/// show for this entry", not an error, so the UI must skip it rather than render
/// a blank modal.
/// </remarks>
public sealed record LoginAgreementDocument
{
    [JsonConstructor]
    public LoginAgreementDocument(
        string? id = null,
        string? title = null,
        string? contentMarkdown = null)
    {
        Id = id ?? string.Empty;
        Title = title ?? string.Empty;
        ContentMarkdown = contentMarkdown ?? string.Empty;
    }

    [JsonPropertyName("id")]
    public string Id { get; init; } = string.Empty;

    [JsonPropertyName("title")]
    public string Title { get; init; } = string.Empty;

    [JsonPropertyName("content_md")]
    public string ContentMarkdown { get; init; } = string.Empty;

    /// <summary>Whether this entry actually has something to display.</summary>
    public bool HasContent => !string.IsNullOrWhiteSpace(ContentMarkdown);
}

/// <summary>The signed-in user as the relay describes them.</summary>
public sealed record RelayUser
{
    [JsonConstructor]
    public RelayUser(
        long id = default,
        string? email = null,
        string? username = null,
        string? role = null,
        double balance = default,
        double frozenBalance = default,
        string? status = null)
    {
        Id = id;
        Email = email ?? string.Empty;
        Username = username ?? string.Empty;
        Role = role ?? string.Empty;
        Balance = balance;
        FrozenBalance = frozenBalance;
        Status = status ?? string.Empty;
    }

    [JsonPropertyName("id")]
    public long Id { get; init; }

    [JsonPropertyName("email")]
    public string Email { get; init; } = string.Empty;

    [JsonPropertyName("username")]
    public string Username { get; init; } = string.Empty;

    [JsonPropertyName("role")]
    public string Role { get; init; } = string.Empty;

    [JsonPropertyName("balance")]
    public double Balance { get; init; }

    [JsonPropertyName("frozen_balance")]
    public double FrozenBalance { get; init; }

    [JsonPropertyName("status")]
    public string Status { get; init; } = string.Empty;

    /// <summary>What to show in the identity area; falls back to the email's local part.</summary>
    public string DisplayName =>
        !string.IsNullOrWhiteSpace(Username) ? Username
        : !string.IsNullOrWhiteSpace(Email) ? Email.Split('@')[0]
        : "未命名用户";
}

/// <summary>One announcement as the relay presents it to this user.</summary>
/// <remarks>
/// Mirrors <c>dto.UserAnnouncement</c>. The server has already filtered by time
/// window and by targeting (balance and active subscription groups), so every
/// item that arrives here is one this user is meant to see — the client must not
/// re-apply eligibility rules it does not have the inputs for.
/// </remarks>
public sealed record RelayAnnouncement
{
    [JsonConstructor]
    public RelayAnnouncement(
        long id = default,
        string? title = null,
        string? content = null,
        string? notifyMode = null,
        DateTimeOffset? readAt = default,
        DateTimeOffset createdAt = default)
    {
        Id = id;
        Title = title ?? string.Empty;
        Content = content ?? string.Empty;
        NotifyMode = notifyMode ?? string.Empty;
        ReadAt = readAt;
        CreatedAt = createdAt;
    }

    [JsonPropertyName("id")]
    public long Id { get; init; }

    [JsonPropertyName("title")]
    public string Title { get; init; } = string.Empty;

    /// <summary>The body, as markdown. Rendered client-side; may contain images.</summary>
    [JsonPropertyName("content")]
    public string Content { get; init; } = string.Empty;

    /// <summary>
    /// How the operator asked for this to be surfaced: <c>silent</c> or <c>popup</c>.
    /// </summary>
    /// <remarks>
    /// The same field drives the web popup, so the client honours it rather than
    /// inventing its own rule — one setting must not come to mean two things.
    /// </remarks>
    [JsonPropertyName("notify_mode")]
    public string NotifyMode { get; init; } = string.Empty;

    /// <summary>When this user read it; null means unread.</summary>
    [JsonPropertyName("read_at")]
    public DateTimeOffset? ReadAt { get; init; }

    [JsonPropertyName("created_at")]
    public DateTimeOffset CreatedAt { get; init; }

    public bool IsUnread => ReadAt is null;

    /// <summary>Whether the operator asked for this one to interrupt the user.</summary>
    public bool WantsPopup => string.Equals(NotifyMode, "popup", StringComparison.OrdinalIgnoreCase);
}

/// <summary>The announcement list summarised, with no bodies.</summary>
/// <remarks>
/// What a poll asks for. Announcement bodies are markdown that may carry
/// embedded base64 images, so pulling the whole list on a schedule costs far
/// more than checking these two numbers and pulling only when they move.
/// </remarks>
public sealed record AnnouncementHead
{
    /// <summary>Highest visible announcement id; 0 when the user has none.</summary>
    [JsonPropertyName("max_id")]
    public long MaxId { get; init; }

    [JsonPropertyName("unread_count")]
    public int UnreadCount { get; init; }

    /// <summary>
    /// How many are visible in total.
    /// </summary>
    /// <remarks>
    /// Carried alongside the watermark because an announcement being withdrawn
    /// moves neither <see cref="MaxId"/> nor <see cref="UnreadCount"/> when the
    /// withdrawn one was read — the total is what catches that.
    /// </remarks>
    [JsonPropertyName("total")]
    public int Total { get; init; }
}

/// <summary>An issued token pair.</summary>
public sealed record AuthTokens
{
    [JsonConstructor]
    public AuthTokens(
        string accessToken = "",
        string refreshToken = "",
        int expiresInSeconds = 0,
        string tokenType = "",
        RelayUser? user = null)
    {
        AccessToken = accessToken;
        RefreshToken = refreshToken;
        ExpiresInSeconds = expiresInSeconds;
        TokenType = tokenType;
        User = user;
    }

    [JsonPropertyName("access_token")]
    public string AccessToken { get; init; } = string.Empty;

    [JsonPropertyName("refresh_token")]
    public string RefreshToken { get; init; } = string.Empty;

    [JsonPropertyName("expires_in")]
    public int ExpiresInSeconds { get; init; }

    [JsonPropertyName("token_type")]
    public string TokenType { get; init; } = string.Empty;

    [JsonPropertyName("user")]
    public RelayUser? User { get; init; }
}

/// <summary>
/// The two-factor step, returned instead of tokens when the account has TOTP on.
/// </summary>
internal sealed record TotpChallengePayload
{
    [JsonPropertyName("requires_2fa")]
    public bool Requires2Fa { get; init; }

    [JsonPropertyName("temp_token")]
    public string? TempToken { get; init; }

    [JsonPropertyName("user_email_masked")]
    public string? UserEmailMasked { get; init; }
}

/// <summary>
/// What a sign-in attempt produced: either tokens, or a demand for a TOTP code.
/// </summary>
/// <remarks>
/// Modelled as a result rather than an exception because needing a second factor
/// is a normal branch of a successful sign-in, not a failure.
/// </remarks>
public sealed record LoginOutcome
{
    private LoginOutcome()
    {
    }

    public AuthTokens? Tokens { get; private init; }

    public string? TempToken { get; private init; }

    public string? MaskedEmail { get; private init; }

    /// <summary>True when the caller must collect a 6-digit code and call the 2FA endpoint.</summary>
    public bool RequiresTwoFactor => Tokens is null;

    public static LoginOutcome Authenticated(AuthTokens tokens) =>
        new() { Tokens = tokens ?? throw new ArgumentNullException(nameof(tokens)) };

    public static LoginOutcome TwoFactorRequired(string tempToken, string? maskedEmail) =>
        new() { TempToken = tempToken, MaskedEmail = maskedEmail };
}

/// <summary>The server's answer to a verification-code request.</summary>
public sealed record VerifyCodeDispatch
{
    [JsonPropertyName("message")]
    public string? Message { get; init; }

    /// <summary>
    /// Seconds the client must wait before offering "resend".
    /// </summary>
    /// <remarks>
    /// F1'.2 requires driving the resend button from this value rather than a
    /// locally invented countdown, so the client stays in step with the server's
    /// own rate limiting.
    /// </remarks>
    [JsonPropertyName("countdown")]
    public int CountdownSeconds { get; init; }
}

/// <summary>Everything needed to create an account, minus what the server does not ask for.</summary>
public sealed record RegistrationRequest
{
    public required string Email { get; init; }

    public required string Password { get; init; }

    public string? VerifyCode { get; init; }

    public string? InvitationCode { get; init; }

    public string? PromoCode { get; init; }

    /// <summary>
    /// A token produced by the user completing a Cloudflare Turnstile challenge.
    /// </summary>
    /// <remarks>
    /// Supplied by the embedded browser component that renders the widget; the
    /// user solves it themselves. Nothing in this client attempts to satisfy a
    /// challenge on their behalf.
    /// </remarks>
    public string? TurnstileToken { get; init; }
}
