using System.IO;
using System.Text;

using LanAi.RelayClient.Platform;

namespace LanAi.RelayClient.Services;

/// <summary>Supplies the identifier that distinguishes this installation.</summary>
internal interface IInstallIdProvider
{
    /// <summary>Returns the id, creating and persisting one on first use.</summary>
    string Get();
}

/// <summary>
/// A short, stable id for this installation (F3.2.1).
/// </summary>
/// <remarks>
/// <para>
/// Part of the managed key's name, so the client can recognise its own lease
/// among the user's other keys. The machine name alone is not enough: a reinstall
/// on the same machine would otherwise adopt the previous installation's lease,
/// and two installations would fight over one key — each renewing and rebinding
/// what the other believes it owns.
/// </para>
/// <para>
/// Random, not derived from hardware. A hardware fingerprint would be stable
/// across reinstalls — the opposite of what is wanted — and would put an
/// identifier the user never consented to into a name the server stores.
/// </para>
/// <para>
/// A read failure yields a fresh id rather than an exception. The consequence is
/// a new lease and one orphaned old one, which the cleanup in F3.2.1 removes;
/// failing to start because an id file went bad would be far worse.
/// </para>
/// </remarks>
internal sealed class InstallId : IInstallIdProvider
{
    /// <summary>Characters permitted in the id, chosen to survive being part of a key name.</summary>
    private const string Alphabet = "abcdefghijklmnopqrstuvwxyz0123456789";

    private const int Length = 8;

    private readonly string _filePath;
    private readonly object _gate = new();

    private string? _cached;

    public InstallId(string? filePath = null) => _filePath = filePath ?? DefaultFilePath();

    internal static string DefaultFilePath() => AppPaths.InData("install-id");

    public string Get()
    {
        lock (_gate)
        {
            if (_cached is not null)
            {
                return _cached;
            }

            _cached = Read() ?? CreateAndPersist();
            return _cached;
        }
    }

    private string? Read()
    {
        try
        {
            if (!File.Exists(_filePath))
            {
                return null;
            }

            string value = File.ReadAllText(_filePath, Encoding.UTF8).Trim();

            // Validated rather than trusted: a corrupted file could otherwise put
            // whitespace or punctuation into a key name and make the naming rule
            // stop matching, at which point the client silently loses its lease.
            return IsWellFormed(value) ? value : null;
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            return null;
        }
    }

    private string CreateAndPersist()
    {
        string value = Generate();

        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(_filePath)!);

            string temporaryPath = _filePath + ".tmp";
            File.WriteAllText(temporaryPath, value, Encoding.UTF8);
            File.Move(temporaryPath, _filePath, overwrite: true);
        }
        catch (Exception ex) when (ex is IOException or UnauthorizedAccessException)
        {
            // The id still serves this run; the next run gets a different one and
            // leaves an orphan lease behind, which is recoverable. Refusing to
            // start would not be.
            ClientLog.Warning($"安装 ID 无法写入 {_filePath}，本次运行使用临时 ID", ex);
        }

        return value;
    }

    internal static bool IsWellFormed(string? value) =>
        value is { Length: Length } && value.All(Alphabet.Contains);

    private static string Generate()
    {
        var builder = new StringBuilder(Length);
        for (int i = 0; i < Length; i++)
        {
            builder.Append(Alphabet[System.Security.Cryptography.RandomNumberGenerator.GetInt32(Alphabet.Length)]);
        }

        return builder.ToString();
    }
}
