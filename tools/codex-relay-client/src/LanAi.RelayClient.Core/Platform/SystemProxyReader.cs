using System.Net;
using System.Runtime.Versioning;
using System.Text.RegularExpressions;
using Microsoft.Win32;

namespace LanAi.RelayClient.Platform;

/// <summary>The proxy a direct connection to the official API would use right now.</summary>
/// <param name="Proxy">What to hand an <see cref="HttpClientHandler"/>; null means connect directly.</param>
/// <param name="Description">The same thing in words, for the user.</param>
internal sealed record SystemProxy(IWebProxy? Proxy, string Description);

/// <summary>
/// Reads the proxy settings as they are <em>now</em>, rather than as they were when this
/// process started.
/// </summary>
/// <remarks>
/// <para>
/// .NET's own <see cref="HttpClient.DefaultProxy"/> is one instance per process, and on
/// Windows it takes a snapshot of the WinINET settings when it is created (measured: an
/// <c>HttpWindowsProxy</c> holding a <c>WinInetProxyHelper</c>, nothing watching for
/// changes). A user who opens this client and only then turns on their VPN's system proxy
/// would have the local proxy connect directly — and fail — with nothing in the client able
/// to tell why. So the settings are read here, fresh, each time a connection is built.
/// </para>
/// <para>
/// Same precedence as .NET: the <c>HTTPS_PROXY</c> / <c>ALL_PROXY</c> environment
/// variables first (measured on the dev machine, where they win), then on Windows the
/// user's Internet settings in the registry. A PAC script (<c>AutoConfigURL</c>) cannot be
/// evaluated here, so that case keeps .NET's own reading. A VPN in TUN / global mode needs
/// no proxy at all and shows up as a direct connection that nevertheless works.
/// </para>
/// </remarks>
internal static class SystemProxyReader
{
    private const string InternetSettings = @"Software\Microsoft\Windows\CurrentVersion\Internet Settings";

    internal static SystemProxy Current(Uri target)
    {
        if (FromEnvironment() is { } fromEnvironment)
        {
            return fromEnvironment;
        }

        if (OperatingSystem.IsWindows())
        {
            return FromWindowsSettings(target);
        }

        // macOS: .NET asks the system per request there, so its default is current.
        Uri? proxied = HttpClient.DefaultProxy.GetProxy(target);
        return proxied is null || proxied == target
            ? new SystemProxy(null, "直连（未检测到系统代理）")
            : new SystemProxy(HttpClient.DefaultProxy, $"系统代理 {proxied.Authority}");
    }

    private static SystemProxy? FromEnvironment()
    {
        string? value = Environment.GetEnvironmentVariable("HTTPS_PROXY")
            ?? Environment.GetEnvironmentVariable("https_proxy")
            ?? Environment.GetEnvironmentVariable("ALL_PROXY")
            ?? Environment.GetEnvironmentVariable("all_proxy");
        if (string.IsNullOrWhiteSpace(value) || ParseProxyAddress(value) is not { } address)
        {
            return null;
        }

        string? noProxy = Environment.GetEnvironmentVariable("NO_PROXY") ?? Environment.GetEnvironmentVariable("no_proxy");
        var proxy = new HostBypassProxy(address, SplitList(noProxy, ','), bypassLocal: false);
        return new SystemProxy(proxy, $"环境变量代理 {address.Authority}");
    }

    [SupportedOSPlatform("windows")]
    private static SystemProxy FromWindowsSettings(Uri target)
    {
        try
        {
            using RegistryKey? settings = Registry.CurrentUser.OpenSubKey(InternetSettings);
            if (settings?.GetValue("AutoConfigURL") is string pac && !string.IsNullOrWhiteSpace(pac))
            {
                return new SystemProxy(HttpClient.DefaultProxy, "系统代理（自动配置脚本）");
            }

            bool enabled = settings?.GetValue("ProxyEnable") is int flag && flag != 0;
            string? server = settings?.GetValue("ProxyServer") as string;
            if (enabled && ParseWindowsProxyServer(server) is { } address)
            {
                string[] overrides = SplitList(settings?.GetValue("ProxyOverride") as string, ';');
                var proxy = new HostBypassProxy(
                    address,
                    [.. overrides.Where(o => !o.Equals("<local>", StringComparison.OrdinalIgnoreCase))],
                    bypassLocal: overrides.Any(o => o.Equals("<local>", StringComparison.OrdinalIgnoreCase)));
                return new SystemProxy(proxy, $"系统代理 {address.Authority}");
            }
        }
        catch (Exception ex) when (ex is System.Security.SecurityException or UnauthorizedAccessException or IOException)
        {
            return new SystemProxy(HttpClient.DefaultProxy, "系统代理（读取失败，按启动时的设置）");
        }

        return new SystemProxy(null, "直连（未检测到系统代理）");
    }

    /// <summary>
    /// Windows' <c>ProxyServer</c>: either <c>host:port</c> for every scheme, or
    /// <c>http=host:port;https=host:port;…</c>. The https entry is the one that matters here.
    /// </summary>
    internal static Uri? ParseWindowsProxyServer(string? value)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return null;
        }

        if (!value.Contains('='))
        {
            return ParseProxyAddress(value);
        }

        string[] entries = value.Split(';', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);
        string? pick(string scheme) => entries
            .Select(e => e.Split('=', 2))
            .Where(p => p.Length == 2 && p[0].Trim().Equals(scheme, StringComparison.OrdinalIgnoreCase))
            .Select(p => p[1].Trim())
            .FirstOrDefault();
        return ParseProxyAddress(pick("https") ?? pick("http") ?? string.Empty);
    }

    /// <summary><c>host:port</c> or a full URI; an address without a scheme is an HTTP proxy.</summary>
    internal static Uri? ParseProxyAddress(string value)
    {
        value = value.Trim();
        if (value.Length == 0)
        {
            return null;
        }

        if (!value.Contains("://", StringComparison.Ordinal))
        {
            value = "http://" + value;
        }

        return Uri.TryCreate(value, UriKind.Absolute, out Uri? uri) && !string.IsNullOrEmpty(uri.Host) ? uri : null;
    }

    private static string[] SplitList(string? value, char separator) =>
        string.IsNullOrWhiteSpace(value)
            ? []
            : value.Split(separator, StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);
}

/// <summary>A fixed proxy address with a bypass list matched on the host name.</summary>
/// <remarks>
/// Not <see cref="WebProxy"/>: its bypass entries are regular expressions matched against
/// <c>scheme://host:port</c>, which neither <c>NO_PROXY</c> nor Windows' <c>ProxyOverride</c>
/// is written as. Loopback is never proxied.
/// </remarks>
internal sealed class HostBypassProxy(Uri address, IReadOnlyList<string> bypass, bool bypassLocal) : IWebProxy
{
    public ICredentials? Credentials { get; set; }

    public Uri? GetProxy(Uri destination) => IsBypassed(destination) ? null : address;

    public bool IsBypassed(Uri host)
    {
        string name = host.IdnHost.Trim('[', ']');
        if (host.IsLoopback || name.Equals("localhost", StringComparison.OrdinalIgnoreCase))
        {
            return true;
        }

        if (bypassLocal && !name.Contains('.') && !name.Contains(':'))
        {
            return true;
        }

        string withPort = $"{name}:{host.Port}";
        return bypass.Any(entry => Matches(entry, name) || Matches(entry, withPort));
    }

    /// <summary>
    /// <c>*</c> for everything, wildcards (<c>*.corp.com</c>, <c>10.*</c>), a leading dot for a
    /// domain and its subdomains, or a plain name — which also covers its subdomains, as
    /// <c>NO_PROXY</c> means it.
    /// </summary>
    private static bool Matches(string entry, string host)
    {
        if (entry == "*")
        {
            return true;
        }

        if (entry.Contains('*'))
        {
            string pattern = "^" + Regex.Escape(entry).Replace(@"\*", ".*", StringComparison.Ordinal) + "$";
            return Regex.IsMatch(host, pattern, RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);
        }

        string domain = entry.TrimStart('.');
        return host.Equals(domain, StringComparison.OrdinalIgnoreCase)
            || host.EndsWith("." + domain, StringComparison.OrdinalIgnoreCase);
    }
}
