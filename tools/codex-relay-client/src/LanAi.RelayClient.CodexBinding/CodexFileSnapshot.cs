using System.Text.Json;

namespace LanAi.RelayClient.CodexBinding;

/// <summary>
/// Captures the user's complete Codex files once and restores them byte for byte.
/// </summary>
public sealed class CodexFileSnapshot
{
    private const string ManifestFileName = "manifest.json";
    private const string AuthFileName = "auth.bin";
    private const string ConfigFileName = "config.bin";

    private readonly CodexPaths _paths;
    private readonly string _root;
    private readonly ISnapshotProtector _protector;

    public CodexFileSnapshot(CodexPaths paths, string root, ISnapshotProtector protector)
    {
        _paths = paths ?? throw new ArgumentNullException(nameof(paths));
        _root = string.IsNullOrWhiteSpace(root)
            ? throw new ArgumentException("Snapshot directory cannot be empty.", nameof(root))
            : Path.GetFullPath(root);
        _protector = protector ?? throw new ArgumentNullException(nameof(protector));
    }

    public bool CaptureOnce()
    {
        string manifestPath = Path.Combine(_root, ManifestFileName);
        if (File.Exists(manifestPath))
        {
            MigrateExistingSnapshot(ReadManifest(manifestPath));
            return false;
        }

        Directory.CreateDirectory(_root);

        bool authExists = File.Exists(_paths.AuthPath);
        bool configExists = File.Exists(_paths.ConfigPath);
        WriteCapturedFile(_paths.AuthPath, Path.Combine(_root, AuthFileName), authExists);
        WriteCapturedFile(_paths.ConfigPath, Path.Combine(_root, ConfigFileName), configExists);

        var manifest = new SnapshotManifest(
            authExists,
            configExists,
            SnapshotBlobFormat.CurrentProtectionVersion);
        AtomicWrite(manifestPath, JsonSerializer.SerializeToUtf8Bytes(manifest, CodexJsonContext.Default.SnapshotManifest));
        return true;
    }

    public bool Restore()
    {
        string manifestPath = Path.Combine(_root, ManifestFileName);
        if (!File.Exists(manifestPath))
        {
            return false;
        }

        SnapshotManifest manifest = ReadManifest(manifestPath);
        byte[]? auth = null;
        byte[]? config = null;
        try
        {
            auth = ReadCapturedFile(
                Path.Combine(_root, AuthFileName),
                manifest.AuthExisted,
                manifest.ProtectionVersion);
            config = ReadCapturedFile(
                Path.Combine(_root, ConfigFileName),
                manifest.ConfigExisted,
                manifest.ProtectionVersion);

            Directory.CreateDirectory(_paths.Home);
            RestoreFile(_paths.AuthPath, auth, manifest.AuthExisted);
            RestoreFile(_paths.ConfigPath, config, manifest.ConfigExisted);
            Clear();
            return true;
        }
        finally
        {
            if (auth is not null)
            {
                Array.Clear(auth, 0, auth.Length);
            }

            if (config is not null)
            {
                Array.Clear(config, 0, config.Length);
            }
        }
    }

    public void Clear()
    {
        DeleteWithTemporary(Path.Combine(_root, ManifestFileName));
        DeleteWithTemporary(Path.Combine(_root, AuthFileName));
        DeleteWithTemporary(Path.Combine(_root, ConfigFileName));
    }

    private void WriteCapturedFile(string source, string destination, bool exists)
    {
        if (!exists)
        {
            DeleteWithTemporary(destination);
            return;
        }

        byte[] plaintext = File.ReadAllBytes(source);
        try
        {
            AtomicWrite(destination, SnapshotBlobFormat.Protect(plaintext, _protector));
        }
        finally
        {
            Array.Clear(plaintext, 0, plaintext.Length);
        }
    }

    private static void RestoreFile(string destination, byte[]? contents, bool existed)
    {
        if (!existed)
        {
            DeleteWithTemporary(destination);
            return;
        }

        if (contents is null)
        {
            throw new InvalidDataException("Codex snapshot contents are missing.");
        }

        AtomicWrite(destination, contents);
    }

    private SnapshotManifest ReadManifest(string manifestPath)
    {
        try
        {
            using JsonDocument document = JsonDocument.Parse(File.ReadAllText(manifestPath));
            JsonElement root = document.RootElement;
            if (root.ValueKind != JsonValueKind.Object ||
                !root.TryGetProperty(nameof(SnapshotManifest.AuthExisted), out JsonElement authExisted) ||
                !root.TryGetProperty(nameof(SnapshotManifest.ConfigExisted), out JsonElement configExisted) ||
                authExisted.ValueKind is not (JsonValueKind.True or JsonValueKind.False) ||
                configExisted.ValueKind is not (JsonValueKind.True or JsonValueKind.False))
            {
                throw new InvalidDataException("Codex snapshot manifest is invalid.");
            }

            int protectionVersion = 0;
            if (root.TryGetProperty(
                    nameof(SnapshotManifest.ProtectionVersion),
                    out JsonElement protectionVersionElement) &&
                (protectionVersionElement.ValueKind != JsonValueKind.Number ||
                    !protectionVersionElement.TryGetInt32(out protectionVersion)))
            {
                throw new InvalidDataException("Codex snapshot protection version is invalid.");
            }

            if (protectionVersion is not 0 and not SnapshotBlobFormat.CurrentProtectionVersion)
            {
                throw new InvalidDataException("Codex snapshot protection version is unsupported.");
            }

            return new SnapshotManifest(
                authExisted.GetBoolean(),
                configExisted.GetBoolean(),
                protectionVersion);
        }
        catch (JsonException ex)
        {
            throw new InvalidDataException("Codex snapshot manifest is invalid.", ex);
        }
    }

    private void MigrateExistingSnapshot(SnapshotManifest manifest)
    {
        string authPath = Path.Combine(_root, AuthFileName);
        string configPath = Path.Combine(_root, ConfigFileName);
        if (manifest.ProtectionVersion == SnapshotBlobFormat.CurrentProtectionVersion)
        {
            ValidateProtectedFile(authPath, manifest.AuthExisted);
            ValidateProtectedFile(configPath, manifest.ConfigExisted);
            return;
        }

        MigrateFile(authPath, manifest.AuthExisted);
        MigrateFile(configPath, manifest.ConfigExisted);
        AtomicWrite(
            Path.Combine(_root, ManifestFileName),
            JsonSerializer.SerializeToUtf8Bytes(
                manifest with
                {
                    ProtectionVersion = SnapshotBlobFormat.CurrentProtectionVersion,
                },
                CodexJsonContext.Default.SnapshotManifest));
    }

    private void ValidateProtectedFile(string path, bool shouldExist)
    {
        byte[]? plaintext = ReadCapturedFile(
            path,
            shouldExist,
            SnapshotBlobFormat.CurrentProtectionVersion);
        if (plaintext is not null)
        {
            Array.Clear(plaintext, 0, plaintext.Length);
        }
    }

    private void MigrateFile(string path, bool shouldExist)
    {
        if (!shouldExist)
        {
            DeleteWithTemporary(path);
            return;
        }

        if (!File.Exists(path))
        {
            throw new InvalidDataException($"Codex snapshot is missing {Path.GetFileName(path)}.");
        }

        byte[] stored = File.ReadAllBytes(path);
        try
        {
            SnapshotBlob blob = SnapshotBlobFormat.Read(
                stored,
                _protector,
                requireProtection: false);
            try
            {
                if (!blob.WasProtected)
                {
                    AtomicWrite(path, SnapshotBlobFormat.Protect(blob.Plaintext, _protector));
                }
            }
            finally
            {
                Array.Clear(blob.Plaintext, 0, blob.Plaintext.Length);
            }
        }
        finally
        {
            Array.Clear(stored, 0, stored.Length);
        }
    }

    private byte[]? ReadCapturedFile(string path, bool shouldExist, int protectionVersion)
    {
        if (!shouldExist)
        {
            return null;
        }

        if (!File.Exists(path))
        {
            throw new InvalidDataException($"Codex snapshot is missing {Path.GetFileName(path)}.");
        }

        byte[] stored = File.ReadAllBytes(path);
        try
        {
            return SnapshotBlobFormat.Read(
                stored,
                _protector,
                requireProtection:
                    protectionVersion == SnapshotBlobFormat.CurrentProtectionVersion).Plaintext;
        }
        finally
        {
            Array.Clear(stored, 0, stored.Length);
        }
    }

    private static void AtomicWrite(string path, byte[] contents)
    {
        string? directory = Path.GetDirectoryName(path);
        if (directory is not null)
        {
            Directory.CreateDirectory(directory);
        }

        string temporaryPath = path + ".tmp";
        File.WriteAllBytes(temporaryPath, contents);
        File.Move(temporaryPath, path, overwrite: true);
    }

    private static void DeleteWithTemporary(string path)
    {
        DeleteIfPresent(path);
        DeleteIfPresent(path + ".tmp");
    }

    private static void DeleteIfPresent(string path)
    {
        if (File.Exists(path))
        {
            File.Delete(path);
        }
    }

    internal sealed record SnapshotManifest(
        bool AuthExisted,
        bool ConfigExisted,
        int ProtectionVersion);

}
