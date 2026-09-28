using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Runtime.Versioning;
using System.Text;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Transport;

/// <summary>Runs <c>/usr/bin/security</c>. Faked in tests.</summary>
internal interface ISecurityCommand
{
    /// <returns>The exit code and standard output; a timeout kills the child and returns null.</returns>
    SecurityResult? Run(IReadOnlyList<string> arguments, TimeSpan timeout);
}

internal sealed record SecurityResult(int ExitCode, string Output)
{
    public override string ToString() => $"SecurityResult {{ ExitCode = {ExitCode} }}";
}

/// <summary>Replaces a keychain item's data in place. Faked in tests.</summary>
internal interface IKeychainItemWriter
{
    /// <returns>The Security framework status; 0 on success.</returns>
    int Replace(string service, string account, byte[] data);
}

/// <summary>
/// Claude Code's sign-in in the macOS login keychain (任务计划 D10, §3.9).
/// </summary>
/// <remarks>
/// <para>
/// Claude Code keeps its sign-in in the generic password item <c>Claude Code-credentials</c> of
/// the current user, holding the same JSON as <c>.credentials.json</c> elsewhere. Reading another
/// program's item raises the system's permission prompt, so:
/// </para>
/// <list type="bullet">
/// <item><b>Looking</b> only asks whether the item exists — attributes, no secret, no prompt
/// (<see cref="Exists"/>). Opening the page never prompts.</item>
/// <item><b>Reading</b> goes through Apple's <c>security … -w</c>, the secret coming back on its
/// standard output, never in an argument. The program asking the keychain is then Apple's
/// signed <c>security</c>: 「始终允许」 is remembered for it, and survives every update of this
/// ad-hoc-signed client. The first read happens when the user switches the account on; until
/// then <see cref="ReadNeedsConsent"/> tells the caller not to read on the UI thread.</item>
/// <item>After that the secret is kept in memory; each read compares the item's attributes
/// (modification date included) and reads the secret again only when Claude Code changed it.</item>
/// <item><b>Writing</b> a refreshed pair back goes through the Security framework in this
/// process, replacing only the item's data — never <c>security add-generic-password -w</c>,
/// which would put the secret in <c>argv</c> for every process of this user to read from
/// <c>ps</c> (the same reason <see cref="Platform.MacOS.KeychainMasterKeyStore"/> refuses it).</item>
/// </list>
/// <para>
/// When Claude Code fell back to <c>.credentials.json</c> (no keychain), the file is used as on
/// other platforms. Nothing here has run on a Mac yet: service and account names, the prompts,
/// and how Claude Code rewrites the item are to be confirmed there (A0 ⑧).
/// </para>
/// </remarks>
internal sealed class ClaudeKeychainStore : IClaudeCredentialStore
{
    public const string ServiceName = "Claude Code-credentials";

    /// <summary>How long the user has to answer the permission prompt.</summary>
    internal static readonly TimeSpan ReadTimeout = TimeSpan.FromSeconds(60);

    private static readonly TimeSpan LookTimeout = TimeSpan.FromSeconds(5);
    private static readonly TimeSpan ExistsMaxAge = TimeSpan.FromSeconds(30);

    private const int ItemNotFound = 44;

    private readonly ClaudeCredentialFile _file;
    private readonly ISecurityCommand _security;
    private readonly IKeychainItemWriter _writer;
    private readonly string _account;
    private readonly Func<DateTimeOffset> _clock;
    private readonly object _gate = new();

    private string? _secret;
    private string? _attributes;
    private (bool Exists, DateTimeOffset At)? _exists;

    public ClaudeKeychainStore(
        ClaudeCredentialFile file,
        ISecurityCommand? security = null,
        IKeychainItemWriter? writer = null,
        string? account = null,
        Func<DateTimeOffset>? clock = null)
    {
        _file = file ?? throw new ArgumentNullException(nameof(file));
        _security = security ?? new SecurityCommand();
        _writer = writer ?? CreateWriter();
        _account = account ?? Environment.UserName;
        _clock = clock ?? (() => DateTimeOffset.UtcNow);
    }

    public string? UnsupportedReason => null;

    /// <summary>
    /// True until the secret has been read once in this run: the next read may bring up the
    /// system prompt and wait up to a minute for the user.
    /// </summary>
    public bool ReadNeedsConsent
    {
        get
        {
            if (_file.Exists())
            {
                return false;
            }

            lock (_gate)
            {
                return _secret is null;
            }
        }
    }

    /// <summary>Reads may wait on the user (the prompt) or a child process; callers keep them off the UI thread.</summary>
    public bool ReadMayBlock => !_file.Exists();

    public bool Exists()
    {
        if (_file.Exists())
        {
            return true;
        }

        lock (_gate)
        {
            if (_secret is not null)
            {
                return true;
            }

            if (_exists is { } cached && _clock() - cached.At < ExistsMaxAge)
            {
                return cached.Exists;
            }
        }

        bool exists = LookUp() is not null;
        lock (_gate)
        {
            _exists = (exists, _clock());
        }

        return exists;
    }

    public string? ReadCached()
    {
        if (_file.Exists())
        {
            return _file.Read();
        }

        lock (_gate)
        {
            return _secret;
        }
    }

    public string? Read()
    {
        if (_file.Exists())
        {
            return _file.Read();
        }

        string? attributes = LookUp();
        if (attributes is null)
        {
            lock (_gate)
            {
                _secret = null;
                _attributes = null;
                _exists = (false, _clock());
            }

            return null;
        }

        lock (_gate)
        {
            if (_secret is not null && attributes == _attributes)
            {
                return _secret;
            }
        }

        SecurityResult? result = _security.Run(
            ["find-generic-password", "-s", ServiceName, "-a", _account, "-w"], ReadTimeout);
        // Said to the user as it is (LocalProxyCredentialException), not folded into a generic
        // read failure: what to do next differs.
        if (result is null)
        {
            ClientLog.Warning("读取钥匙串中的 Claude Code 登录超时（授权框无人回应）");
            throw new LocalProxyCredentialException("没有在一分钟内得到 macOS 的授权。请重新点「开启」，在弹出的对话框里点「始终允许」。");
        }

        if (result.ExitCode != 0)
        {
            ClientLog.Warning($"读取钥匙串中的 Claude Code 登录失败，退出码 {result.ExitCode}");
            throw new LocalProxyCredentialException(result.ExitCode == ItemNotFound
                ? "钥匙串里没有找到 Claude Code 的登录。"
                : "macOS 没有允许读取 Claude Code 的登录（授权框被拒绝）。可以重新点「开启」再试，或改用「在共飞里登录」。");
        }

        string secret = result.Output.TrimEnd('\r', '\n');
        lock (_gate)
        {
            _secret = secret;
            _attributes = attributes;
            _exists = (true, _clock());
        }

        return secret;
    }

    public void Write(string json)
    {
        if (_file.Exists())
        {
            _file.Write(json);
            return;
        }

        int status = _writer.Replace(ServiceName, _account, Encoding.UTF8.GetBytes(json));
        if (status != 0)
        {
            ClientLog.Warning($"写回钥匙串中的 Claude Code 登录失败，状态码 {status}");
            throw new IOException($"写回钥匙串失败（状态码 {status}）");
        }

        // Claude Code will find the new pair; this copy now matches it, and the attributes are
        // looked up again on the next read.
        lock (_gate)
        {
            _secret = json;
            _attributes = LookUp();
        }
    }

    public string ReadEmail() => _file.ReadEmail();

    /// <summary>The item's attributes as <c>security</c> prints them (no secret, no prompt); null when there is none.</summary>
    private string? LookUp()
    {
        SecurityResult? result = _security.Run(["find-generic-password", "-s", ServiceName, "-a", _account], LookTimeout);
        return result is { ExitCode: 0 } ? result.Output : null;
    }

    private static IKeychainItemWriter CreateWriter() =>
        OperatingSystem.IsMacOS() ? new SecurityFrameworkWriter() : new UnavailableWriter();

    private sealed class UnavailableWriter : IKeychainItemWriter
    {
        public int Replace(string service, string account, byte[] data) => -4; // errSecUnimplemented
    }

    /// <summary>Runs <c>/usr/bin/security</c>, killing it when it outlives the timeout.</summary>
    private sealed class SecurityCommand : ISecurityCommand
    {
        public SecurityResult? Run(IReadOnlyList<string> arguments, TimeSpan timeout)
        {
            var info = new ProcessStartInfo("/usr/bin/security")
            {
                UseShellExecute = false,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                CreateNoWindow = true,
                StandardOutputEncoding = Encoding.UTF8,
            };
            foreach (string argument in arguments)
            {
                info.ArgumentList.Add(argument);
            }

            try
            {
                using Process process = Process.Start(info) ?? throw new InvalidOperationException("security 没有启动");
                Task<string> output = process.StandardOutput.ReadToEndAsync();
                _ = process.StandardError.ReadToEndAsync();
                if (!process.WaitForExit(timeout))
                {
                    try
                    {
                        process.Kill(entireProcessTree: true);
                    }
                    catch (InvalidOperationException)
                    {
                    }

                    return null;
                }

                return new SecurityResult(process.ExitCode, output.GetAwaiter().GetResult());
            }
            catch (Exception ex) when (ex is Win32Exception or InvalidOperationException or IOException)
            {
                ClientLog.Warning($"无法运行 security：{ex.GetType().Name}");
                return new SecurityResult(-1, string.Empty);
            }
        }
    }

    /// <summary>
    /// Finds the item and replaces its data through the Security framework, in this process —
    /// the secret never passes through an argument list. Written without a Mac to run it on,
    /// like <see cref="Platform.MacOS.KeychainMasterKeyStore"/>.
    /// </summary>
    [SupportedOSPlatform("macos")]
    private sealed class SecurityFrameworkWriter : IKeychainItemWriter
    {
        private const string Security = "/System/Library/Frameworks/Security.framework/Security";
        private const string CoreFoundation = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation";

        public int Replace(string service, string account, byte[] data)
        {
            byte[] serviceBytes = Encoding.UTF8.GetBytes(service);
            byte[] accountBytes = Encoding.UTF8.GetBytes(account);
            IntPtr item = IntPtr.Zero;
            try
            {
                int status = SecKeychainFindGenericPassword(
                    IntPtr.Zero,
                    (uint)serviceBytes.Length, serviceBytes,
                    (uint)accountBytes.Length, accountBytes,
                    IntPtr.Zero, IntPtr.Zero,
                    out item);
                if (status != 0)
                {
                    return status;
                }

                return SecKeychainItemModifyAttributesAndData(item, IntPtr.Zero, (uint)data.Length, data);
            }
            catch (Exception ex) when (ex is EntryPointNotFoundException or DllNotFoundException)
            {
                ClientLog.Warning("钥匙串接口不可用", ex);
                return -4;
            }
            finally
            {
                if (item != IntPtr.Zero)
                {
                    CFRelease(item);
                }
            }
        }

        [DllImport(Security)]
        private static extern int SecKeychainFindGenericPassword(
            IntPtr keychainOrArray,
            uint serviceNameLength,
            byte[] serviceName,
            uint accountNameLength,
            byte[] accountName,
            IntPtr passwordLength,
            IntPtr passwordData,
            out IntPtr itemRef);

        [DllImport(Security)]
        private static extern int SecKeychainItemModifyAttributesAndData(
            IntPtr itemRef,
            IntPtr attrList,
            uint length,
            byte[] data);

        [DllImport(CoreFoundation)]
        private static extern void CFRelease(IntPtr cf);
    }
}
