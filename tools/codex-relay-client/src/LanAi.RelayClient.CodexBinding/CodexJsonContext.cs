using System.Text.Json.Serialization;

namespace LanAi.RelayClient.CodexBinding;

/// <summary>
/// Source-generated metadata for the snapshot manifest.
/// </summary>
/// <remarks>
/// <para>
/// The third project to need one, and the one where getting it wrong costs the most.
/// This manifest records whether the user had an <c>auth.json</c> and a
/// <c>config.toml</c> of their own before the client took over, and which protection
/// version guarded the copies. Restore reads it to decide what to put back.
/// </para>
/// <para>
/// Written through a reflection binder under a trimmed build, the flags can serialize
/// as their defaults — <c>false</c>, meaning "the user had no configuration of their
/// own". Restore would then delete rather than restore, and the user would never get
/// their own ChatGPT account back. Nothing throws at any point.
/// </para>
/// <para>
/// Reading already used <c>JsonDocument</c>, which is trim-safe, so only the two write
/// paths needed changing.
/// </para>
/// </remarks>
// No naming policy — and that is load-bearing, not an omission.
//
// The original write called SerializeToUtf8Bytes(manifest) with no options at all, so
// the manifest on every existing installation has PascalCase keys, and ReadManifest
// looks them up with nameof(SnapshotManifest.AuthExisted) — an exact match, not a
// case-insensitive one. Adding CamelCase here (as this file first did) makes new
// writes produce "authExisted", ReadManifest throws InvalidDataException, the snapshot
// is judged corrupt, and the user's own Codex configuration can no longer be restored.
//
// The CodexBinding tests catch it, which is how it was caught. Do not "harmonise" this
// with the camelCase used by the other two contexts: those talk to a server that
// chose camelCase, this one talks to files this client wrote itself.
[JsonSerializable(typeof(CodexFileSnapshot.SnapshotManifest))]
internal sealed partial class CodexJsonContext : JsonSerializerContext;
