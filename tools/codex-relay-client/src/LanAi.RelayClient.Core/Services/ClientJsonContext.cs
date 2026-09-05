using System.Text.Json;
using System.Text.Json.Serialization;

namespace LanAi.RelayClient.Services;

/// <summary>
/// Source-generated metadata for the small state files the client keeps on disk.
/// </summary>
/// <remarks>
/// <para>
/// The sibling of <c>RelayJsonContext</c>, for local files rather than the wire.
/// These four types were only found because the trim analyzer was switched on for
/// this project: none of them appears in the server contracts, so the audit of the
/// transport layer missed all of them.
/// </para>
/// <para>
/// What they hold is worth noticing before dismissing them as unimportant — the
/// announcement read-state, the remembered Codex account, the selected group, and
/// the update manifest. Under a trimmed build with reflection binding, these fail as
/// silently as the wire contracts would: announcements re-notify as if unread, the
/// group preference resets to the default on every launch, and the update check
/// stops finding new versions. Nothing crashes; the client just quietly forgets
/// things.
/// </para>
/// <para>
/// The four state records are <c>internal</c> rather than <c>private</c> for one
/// reason only: a source-generated context cannot reference a private nested type.
/// They remain nested inside the store that owns them.
/// </para>
/// </remarks>
[JsonSourceGenerationOptions(
    PropertyNamingPolicy = JsonKnownNamingPolicy.CamelCase,
    PropertyNameCaseInsensitive = true)]
[JsonSerializable(typeof(AnnouncementNotifyStateStore.State), TypeInfoPropertyName = "NotifyState")]
// Disambiguated: AnnouncementNotifyStateStore.State reaches a nested type also
// called AccountState, and the generator keys properties by short name.
[JsonSerializable(typeof(CodexAccountStore.AccountState), TypeInfoPropertyName = "CodexAccountState")]
[JsonSerializable(typeof(GroupPreferenceStore.Preferences), TypeInfoPropertyName = "GroupPreferences")]
// Every property carries an explicit [JsonPropertyName] in snake_case, so the
// CamelCase policy above does not reach it — the names on disk are unchanged. That
// matters more here than elsewhere: this is the signed-in session, and a rename
// would silently sign every existing user out.
[JsonSerializable(typeof(StoredSession))]
internal sealed partial class ClientJsonContext : JsonSerializerContext;
