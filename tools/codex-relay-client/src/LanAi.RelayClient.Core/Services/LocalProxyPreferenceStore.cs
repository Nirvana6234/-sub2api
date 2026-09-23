using System.IO;
using System.Text.Json;
using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>Which of the user's own accounts each tool's local proxy is on, if any.</summary>
internal sealed record LocalProxyChoice
{
    public long? CodexAccountId { get; init; }

    public string? CodexAccountName { get; init; }

    public long? ClaudeAccountId { get; init; }

    public string? ClaudeAccountName { get; init; }

    public static LocalProxyChoice None { get; } = new();
}

internal interface ILocalProxyPreferenceStore
{
    LocalProxyChoice Load();

    void Save(LocalProxyChoice choice);
}

/// <summary>
/// Remembers the local-proxy choice across restarts: it was the user's own decision, and
/// silently going back to the relay server after a reboot would start spending their
/// balance. Cleared on sign-out — the next person to sign in chooses for themselves.
/// </summary>
internal sealed class LocalProxyPreferenceStore : ILocalProxyPreferenceStore
{
    private readonly string _filePath;

    public LocalProxyPreferenceStore(string? filePath = null)
    {
        _filePath = filePath ?? AppPaths.InData("local-proxy.json");
    }

    public LocalProxyChoice Load()
    {
        if (!File.Exists(_filePath))
        {
            return LocalProxyChoice.None;
        }

        try
        {
            return JsonSerializer.Deserialize(File.ReadAllBytes(_filePath), ClientJsonContext.Default.LocalProxyChoice)
                ?? LocalProxyChoice.None;
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            return LocalProxyChoice.None;
        }
    }

    public void Save(LocalProxyChoice choice)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);
            string temporaryPath = _filePath + ".tmp";
            File.WriteAllBytes(temporaryPath, JsonSerializer.SerializeToUtf8Bytes(choice, ClientJsonContext.Default.LocalProxyChoice));
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            ClientLog.Warning("保存本地代理选择失败", ex);
        }
    }
}
