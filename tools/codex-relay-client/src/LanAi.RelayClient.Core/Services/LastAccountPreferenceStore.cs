using System.IO;
using System.Text.Json;

using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>Remembers which email last signed in, so the field does not start blank.</summary>
internal interface ILastAccountPreferenceStore
{
    string? Load();

    void Save(string email);
}

/// <summary>
/// Keeps the last email that successfully authenticated, in a small file beside
/// the session.
/// </summary>
/// <remarks>
/// <para>
/// The password is still typed every time — this only saves re-typing the email,
/// which is not a credential and is safe to keep in the clear (same reasoning as
/// <see cref="GroupPreferenceStore"/>).
/// </para>
/// <para>
/// Scoped by server address for the same reason a group id is: an email that
/// signed in on one relay says nothing about an account on another, and
/// pre-filling it there would be actively misleading rather than merely useless.
/// </para>
/// </remarks>
internal sealed class LastAccountPreferenceStore : ILastAccountPreferenceStore
{
    private readonly string _filePath;
    private readonly string _serverAddress;

    public LastAccountPreferenceStore(string serverAddress, string? filePath = null)
    {
        _serverAddress = serverAddress ?? throw new ArgumentNullException(nameof(serverAddress));
        _filePath = filePath ?? DefaultFilePath();
    }

    internal static string DefaultFilePath() => AppPaths.InData("last-account.json");

    public string? Load()
    {
        if (!File.Exists(_filePath))
        {
            return null;
        }

        try
        {
            LastAccount? saved = JsonSerializer.Deserialize(
                File.ReadAllBytes(_filePath),
                ClientJsonContext.Default.LastAccount);

            if (string.IsNullOrWhiteSpace(saved?.Email))
            {
                return null;
            }

            // Predates this scoping, or belongs to a different relay: neither case
            // can be attributed to this server, so it is discarded rather than
            // guessed at — same rule GroupPreferenceStore applies to its group id.
            return string.Equals(saved.ServerAddress, _serverAddress, StringComparison.OrdinalIgnoreCase)
                ? saved.Email
                : null;
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return null;
        }
    }

    public void Save(string email)
    {
        string trimmed = email?.Trim() ?? string.Empty;
        if (trimmed.Length == 0)
        {
            return;
        }

        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);

            var saved = new LastAccount { ServerAddress = _serverAddress, Email = trimmed };

            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(
                temporaryPath,
                JsonSerializer.SerializeToUtf8Bytes(saved, ClientJsonContext.Default.LastAccount));
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Losing this preference costs the user one re-typed email, not the
            // sign-in itself — not worth failing the login over.
        }
    }

    internal sealed record LastAccount
    {
        public string? ServerAddress { get; init; }

        public string? Email { get; init; }
    }
}
