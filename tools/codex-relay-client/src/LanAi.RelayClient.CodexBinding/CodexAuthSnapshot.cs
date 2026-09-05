using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace LanAi.RelayClient.CodexBinding;

/// <summary>
/// Keeps the user's original Codex credentials so they can be handed back (F3.2.7).
/// </summary>
/// <remarks>
/// <para>
/// Needed because pointing Codex at the relay is not additive. Codex chooses its
/// credential by what is present in <c>auth.json</c>: an OAuth <c>tokens</c>
/// object means "use the signed-in ChatGPT account", and it wins over any
/// <c>OPENAI_API_KEY</c> sitting beside it. So the relay key only takes effect
/// once the account material is out of the file — which means the client is
/// holding the user's login for them, and owes them it back.
/// </para>
/// <para>
/// Stored beside the client's own data rather than in <c>~/.codex</c>: a snapshot
/// living next to the file it protects would be overwritten by the same tools
/// that overwrite the original.
/// </para>
/// </remarks>
public sealed class CodexAuthSnapshot
{
    private readonly ISnapshotProtector _protector;
    private readonly string _filePath;

    /// <param name="filePath">
    /// Where the snapshot lives. Required rather than defaulted: this project is
    /// referenced by code that compiles for macOS, and a default built from
    /// <see cref="Environment.SpecialFolder.LocalApplicationData"/> is a Windows
    /// answer that resolves to a Linux-shaped path there. The caller knows the
    /// platform; this type does not. See AppPaths in the Core project.
    /// </param>
    public CodexAuthSnapshot(ISnapshotProtector protector, string filePath)
    {
        _protector = protector ?? throw new ArgumentNullException(nameof(protector));
        _filePath = !string.IsNullOrWhiteSpace(filePath)
            ? filePath
            : throw new ArgumentException("A snapshot path is required.", nameof(filePath));
    }

    public bool Exists => File.Exists(_filePath);

    /// <summary>
    /// Records <paramref name="original"/> unless something is already recorded.
    /// </summary>
    /// <remarks>
    /// Never overwrites. The second call happens after the client has already
    /// replaced <c>auth.json</c> with its own, so overwriting would capture the
    /// client's own key as though it were the user's login and lose the real one
    /// for good.
    /// </remarks>
    public void CaptureOnce(JsonObject original)
    {
        ArgumentNullException.ThrowIfNull(original);

        if (Exists)
        {
            return;
        }

        Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);

        byte[] plaintext = Encoding.UTF8.GetBytes(
            original.ToJsonString(new JsonSerializerOptions { WriteIndented = true }));
        try
        {
            AtomicWrite(SnapshotBlobFormat.Protect(plaintext, _protector));
        }
        finally
        {
            Array.Clear(plaintext, 0, plaintext.Length);
        }
    }

    /// <summary>Returns what was recorded, or null when nothing was.</summary>
    public JsonObject? Read()
    {
        if (!Exists)
        {
            return null;
        }

        try
        {
            SnapshotBlob blob = SnapshotBlobFormat.Read(
                File.ReadAllBytes(_filePath),
                _protector,
                requireProtection: false);
            try
            {
                JsonObject? original = JsonNode.Parse(blob.Plaintext) as JsonObject;
                if (original is null)
                {
                    return null;
                }

                if (!blob.WasProtected)
                {
                    TryMigratePlaintext(blob.Plaintext);
                }

                return original;
            }
            finally
            {
                Array.Clear(blob.Plaintext, 0, blob.Plaintext.Length);
            }
        }
        catch (Exception ex) when (ex is
            InvalidDataException or
            JsonException or
            CryptographicException or
            IOException or
            UnauthorizedAccessException)
        {
            // Reporting "nothing recorded" is the honest answer; the caller then
            // leaves auth.json alone rather than writing something invented.
            return null;
        }
    }

    /// <summary>Forgets the snapshot, once it has been handed back.</summary>
    public void Clear()
    {
        foreach (string path in new[] { _filePath, _filePath + ".tmp" })
        {
            try
            {
                if (File.Exists(path))
                {
                    File.Delete(path);
                }
            }
            catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
            {
                // A stale snapshot is harmless: it is only ever read during a restore,
                // and CaptureOnce refuses to overwrite it with the client's own key.
            }
        }
    }

    private void TryMigratePlaintext(byte[] plaintext)
    {
        try
        {
            AtomicWrite(SnapshotBlobFormat.Protect(plaintext, _protector));
        }
        catch (Exception ex) when (ex is
            CryptographicException or
            IOException or
            UnauthorizedAccessException)
        {
            // A valid legacy snapshot is still usable when migration cannot be committed.
        }
    }

    private void AtomicWrite(byte[] contents)
    {
        string temporaryPath = _filePath + ".tmp";
        File.WriteAllBytes(temporaryPath, contents);
        File.Move(temporaryPath, _filePath, overwrite: true);
    }

}
