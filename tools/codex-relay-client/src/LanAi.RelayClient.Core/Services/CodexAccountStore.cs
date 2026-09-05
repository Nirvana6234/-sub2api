using System.IO;
using System.Text.Json;

using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

internal interface ICodexAccountStore
{
    string? Load();

    void Save(string email);
}

/// <summary>Remembers which relay account last activated Codex on this machine.</summary>
internal sealed class CodexAccountStore : ICodexAccountStore
{
    private static readonly JsonSerializerOptions SerializerOptions = new(JsonSerializerDefaults.Web);

    private readonly string _filePath;

    public CodexAccountStore(string? filePath = null) =>
        _filePath = filePath ?? DefaultFilePath();

    internal static string DefaultFilePath() => AppPaths.InData("codex-account.json");

    public string? Load()
    {
        if (!File.Exists(_filePath))
        {
            return null;
        }

        try
        {
            AccountState? state = JsonSerializer.Deserialize(
                File.ReadAllBytes(_filePath),
                ClientJsonContext.Default.CodexAccountState);
            return Normalize(state?.Email);
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return null;
        }
    }

    public void Save(string email)
    {
        string normalized = Normalize(email)
            ?? throw new ArgumentException("账户邮箱不能为空。", nameof(email));

        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);
            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(
                temporaryPath,
                JsonSerializer.SerializeToUtf8Bytes(new AccountState { Email = normalized }, ClientJsonContext.Default.CodexAccountState));
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // Account tracking is advisory; inability to persist it must not block login.
        }
    }

    private static string? Normalize(string? email)
    {
        string trimmed = email?.Trim() ?? string.Empty;
        return string.IsNullOrWhiteSpace(trimmed) ? null : trimmed.ToLowerInvariant();
    }

    internal sealed record AccountState
    {
        public string? Email { get; init; }
    }
}
