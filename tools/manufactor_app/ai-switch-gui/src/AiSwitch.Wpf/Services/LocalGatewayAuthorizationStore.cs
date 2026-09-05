using System.Runtime.InteropServices;
using System.Security;
using System.Security.Cryptography;
using System.Text;

namespace LanAi.Workspace.Wpf.Services;

/// <summary>
/// Gives the desktop workspace a deliberately narrow way to reuse an
/// administrator API key for the fixed local Sub2API installation.  The
/// application never attempts to read a backend .env file, a running process,
/// a browser profile, or an administrator password.  A key is available only
/// when the user has explicitly supplied it through Windows Credential Manager
/// or through the current user's environment.
/// </summary>
internal interface ILocalGatewayAuthorizationStore
{
    LocalGatewayAuthorization GetCurrentAuthorization();

    LocalGatewayAuthorizationSaveResult SaveAdministratorApiKey(string administratorApiKey);

    bool ClearSavedAuthorization();
}

internal enum LocalGatewayAuthorizationSource
{
    None,
    WindowsCredentialManager,
    ProcessEnvironment,
    UserEnvironment,
}

internal enum LocalGatewayAuthorizationSaveResult
{
    Saved,
    Invalid,
    Unavailable,
}

/// <summary>
/// The secret itself remains internal to the import workflow.  Its public
/// surface intentionally exposes only source/state information, and its
/// string representation cannot reveal a credential in diagnostics.
/// </summary>
internal sealed class LocalGatewayAuthorization
{
    private LocalGatewayAuthorization(string? administratorApiKey, LocalGatewayAuthorizationSource source)
    {
        AdministratorApiKey = administratorApiKey;
        Source = source;
    }

    internal string? AdministratorApiKey { get; }

    public LocalGatewayAuthorizationSource Source { get; }

    public bool IsAvailable => !string.IsNullOrWhiteSpace(AdministratorApiKey);

    public static LocalGatewayAuthorization Unavailable { get; } = new(null, LocalGatewayAuthorizationSource.None);

    internal static LocalGatewayAuthorization Available(
        string administratorApiKey,
        LocalGatewayAuthorizationSource source)
        => new(administratorApiKey, source);

    public override string ToString() => "Local gateway administrator authorization";
}

/// <summary>
/// Stores an explicitly supplied administrator API key in the current Windows
/// user's Credential Manager.  The saved value is never put in appsettings,
/// profiles, logs, a bindable property, or telemetry.  The optional environment
/// lookups support deliberate unattended setups without reading Sub2API's own
/// process/configuration secrets.
/// </summary>
internal sealed class LocalGatewayAuthorizationStore : ILocalGatewayAuthorizationStore
{
    private const string CredentialTarget = "LanAi.Workspace/Sub2ApiLocalAdminApiKey/v1";
    private const string EnvironmentVariableName = "SUB2API_ADMIN_API_KEY";
    private const uint CredTypeGeneric = 1;
    private const uint CredPersistLocalMachine = 2;
    private const int MaximumBlobBytes = 512;

    public LocalGatewayAuthorization GetCurrentAuthorization()
    {
        if (TryReadWindowsCredential(out string? savedKey) &&
            TryNormalizeAdministratorApiKey(savedKey, out string? normalizedSavedKey))
        {
            return LocalGatewayAuthorization.Available(
                normalizedSavedKey!,
                LocalGatewayAuthorizationSource.WindowsCredentialManager);
        }

        if (TryNormalizeAdministratorApiKey(
                GetEnvironmentValue(EnvironmentVariableTarget.Process),
                out string? processKey))
        {
            return LocalGatewayAuthorization.Available(
                processKey!,
                LocalGatewayAuthorizationSource.ProcessEnvironment);
        }

        if (TryNormalizeAdministratorApiKey(
                GetEnvironmentValue(EnvironmentVariableTarget.User),
                out string? userKey))
        {
            return LocalGatewayAuthorization.Available(
                userKey!,
                LocalGatewayAuthorizationSource.UserEnvironment);
        }

        return LocalGatewayAuthorization.Unavailable;
    }

    public LocalGatewayAuthorizationSaveResult SaveAdministratorApiKey(string administratorApiKey)
    {
        if (!TryNormalizeAdministratorApiKey(administratorApiKey, out string? normalizedKey))
        {
            return LocalGatewayAuthorizationSaveResult.Invalid;
        }

        if (!OperatingSystem.IsWindows())
        {
            return LocalGatewayAuthorizationSaveResult.Unavailable;
        }

        byte[] keyBytes = Encoding.UTF8.GetBytes(normalizedKey!);
        if (keyBytes.Length is 0 or > MaximumBlobBytes)
        {
            CryptographicOperations.ZeroMemory(keyBytes);
            return LocalGatewayAuthorizationSaveResult.Invalid;
        }

        IntPtr blob = IntPtr.Zero;
        try
        {
            blob = Marshal.AllocCoTaskMem(keyBytes.Length);
            Marshal.Copy(keyBytes, 0, blob, keyBytes.Length);
            var credential = new NativeCredential
            {
                Type = CredTypeGeneric,
                TargetName = CredentialTarget,
                CredentialBlobSize = (uint)keyBytes.Length,
                CredentialBlob = blob,
                Persist = CredPersistLocalMachine,
                UserName = "LanAi Workspace",
            };

            return CredWrite(ref credential, 0)
                ? LocalGatewayAuthorizationSaveResult.Saved
                : LocalGatewayAuthorizationSaveResult.Unavailable;
        }
        catch (DllNotFoundException)
        {
            return LocalGatewayAuthorizationSaveResult.Unavailable;
        }
        catch (EntryPointNotFoundException)
        {
            return LocalGatewayAuthorizationSaveResult.Unavailable;
        }
        finally
        {
            CryptographicOperations.ZeroMemory(keyBytes);
            if (blob != IntPtr.Zero)
            {
                Marshal.Copy(keyBytes, 0, blob, keyBytes.Length);
                Marshal.FreeCoTaskMem(blob);
            }
        }
    }

    public bool ClearSavedAuthorization()
    {
        if (!OperatingSystem.IsWindows())
        {
            return false;
        }

        try
        {
            return CredDelete(CredentialTarget, CredTypeGeneric, 0);
        }
        catch (DllNotFoundException)
        {
            return false;
        }
        catch (EntryPointNotFoundException)
        {
            return false;
        }
    }

    internal static bool TryNormalizeAdministratorApiKey(string? value, out string? normalizedKey)
    {
        normalizedKey = null;
        if (string.IsNullOrWhiteSpace(value))
        {
            return false;
        }

        string candidate = value.Trim();
        if (candidate.Length == 0 || candidate.Any(char.IsControl))
        {
            return false;
        }

        byte[] candidateBytes = Encoding.UTF8.GetBytes(candidate);
        try
        {
            if (candidateBytes.Length is 0 or > MaximumBlobBytes)
            {
                return false;
            }
        }
        finally
        {
            CryptographicOperations.ZeroMemory(candidateBytes);
        }

        normalizedKey = candidate;
        return true;
    }

    private static string? GetEnvironmentValue(EnvironmentVariableTarget target)
    {
        try
        {
            return Environment.GetEnvironmentVariable(EnvironmentVariableName, target);
        }
        catch (PlatformNotSupportedException)
        {
            return null;
        }
        catch (SecurityException)
        {
            return null;
        }
    }

    private static bool TryReadWindowsCredential(out string? administratorApiKey)
    {
        administratorApiKey = null;
        if (!OperatingSystem.IsWindows())
        {
            return false;
        }

        IntPtr credentialPointer = IntPtr.Zero;
        try
        {
            if (!CredRead(CredentialTarget, CredTypeGeneric, 0, out credentialPointer) ||
                credentialPointer == IntPtr.Zero)
            {
                return false;
            }

            NativeCredential credential = Marshal.PtrToStructure<NativeCredential>(credentialPointer);
            if (credential.CredentialBlob == IntPtr.Zero ||
                credential.CredentialBlobSize is 0 or > MaximumBlobBytes)
            {
                return false;
            }

            byte[] bytes = new byte[checked((int)credential.CredentialBlobSize)];
            try
            {
                Marshal.Copy(credential.CredentialBlob, bytes, 0, bytes.Length);
                administratorApiKey = Encoding.UTF8.GetString(bytes);
                return true;
            }
            finally
            {
                CryptographicOperations.ZeroMemory(bytes);
            }
        }
        catch (DllNotFoundException)
        {
            return false;
        }
        catch (EntryPointNotFoundException)
        {
            return false;
        }
        finally
        {
            if (credentialPointer != IntPtr.Zero)
            {
                CredFree(credentialPointer);
            }
        }
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct NativeCredential
    {
        public uint Flags;
        public uint Type;
        public string? TargetName;
        public string? Comment;
        public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
        public uint CredentialBlobSize;
        public IntPtr CredentialBlob;
        public uint Persist;
        public uint AttributeCount;
        public IntPtr Attributes;
        public string? TargetAlias;
        public string? UserName;
    }

    [DllImport("Advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CredWrite(ref NativeCredential credential, uint flags);

    [DllImport("Advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CredRead(
        string targetName,
        uint type,
        uint flags,
        out IntPtr credential);

    [DllImport("Advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CredDelete(string targetName, uint type, uint flags);

    [DllImport("Advapi32.dll", SetLastError = true)]
    private static extern void CredFree(IntPtr buffer);
}
