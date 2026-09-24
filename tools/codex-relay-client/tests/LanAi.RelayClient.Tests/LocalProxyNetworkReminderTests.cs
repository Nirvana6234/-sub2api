using System.Net;
using LanAi.RelayClient.Platform;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.Transport;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>The network check and the proxy reminder shown before a tool is switched onto a local proxy.</summary>
public sealed class LocalProxyNetworkReminderTests
{
    private sealed class ChoiceStore : ILocalProxyPreferenceStore
    {
        public LocalProxyChoice Saved { get; set; } = LocalProxyChoice.None;

        public LocalProxyChoice Load() => Saved;

        public void Save(LocalProxyChoice choice) => Saved = choice;
    }

    private sealed class NoUsage : ILocalProxyUsageStore
    {
        public IReadOnlyList<LocalProxyUsageDay> Load() => [];

        public void Add(LocalProxyUsage usage) { }
    }

    private sealed record Rig(
        DashboardViewModel Dashboard,
        LocalProxyViewModelTests.FakeReachability Network,
        FakeCodexStartup Codex,
        List<(string Message, string Label)> Asked,
        List<string> Notified);

    private static async Task<Rig> SignedInAsync(bool reachable, bool userSaysYes, LocalProxyChoice? saved = null)
    {
        var relay = new FakeRelayClient
        {
            OnListContributionAccounts = () =>
            [
                new ContributionAccount(id: 7, name: "我的 Plus", platform: "openai", type: "oauth", status: "active"),
                new ContributionAccount(id: 8, name: "Max", platform: "anthropic", type: "oauth", status: "active"),
            ],
            OnLocalProxyCredential = id => new LocalProxyCredential(accountId: id, accessToken: "at"),
        };
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/", new TestClock().Read);
        var codex = new FakeCodexStartup { UsesLocalTransport = true };
        var network = new LocalProxyViewModelTests.FakeReachability { Reachable = reachable };
        var dashboard = new DashboardViewModel(
            relay,
            session,
            new FakeGroupPreferenceStore(),
            new ManagedKeyNaming(new FixedInstallId("t")),
            codex,
            pluginSupportPreferences: new FakePluginSupportPreferenceStore(),
            localProxyCredentials: new LocalProxyCredentialCache(relay, _ => Task.FromResult("jwt")),
            localProxyPreferences: new ChoiceStore { Saved = saved ?? LocalProxyChoice.None },
            localProxyUsage: new NoUsage(),
            localProxyReachability: network);
        var asked = new List<(string, string)>();
        var notified = new List<string>();
        dashboard.LocalProxy.ConfirmEnable = (message, label) =>
        {
            asked.Add((message, label));
            return Task.FromResult(userSaysYes);
        };
        dashboard.LocalProxy.FailureRaised += notified.Add;
        await session.SignInAsync("a@b.com", "pw");
        await dashboard.RefreshAsync();
        return new Rig(dashboard, network, codex, asked, notified);
    }

    [Fact]
    public async Task SwitchingOnChecksTheRightOfficialHostAndRemindsAboutTheProxy()
    {
        Rig rig = await SignedInAsync(reachable: true, userSaysYes: true);

        await rig.Dashboard.LocalProxy.ToggleAsync(rig.Dashboard.LocalProxy.CodexAccounts[0]);
        await rig.Dashboard.LocalProxy.ToggleAsync(rig.Dashboard.LocalProxy.ClaudeAccounts[0]);

        Assert.Equal(["chatgpt.com", "api.anthropic.com"], rig.Network.Checked.Select(u => u.Host));
        (string message, string label) = rig.Asked[0];
        Assert.Contains("系统代理 127.0.0.1:7897", message);
        Assert.Contains("保持代理/VPN", message);
        Assert.Contains("\n", message);
        Assert.Equal("开启", label);
        Assert.Contains(rig.Codex.LocalProxies, p => p.Kind == LocalProxyKind.Codex && p.Target?.AccountId == 7);
        Assert.False(rig.Dashboard.LocalProxy.HasError);
    }

    [Fact]
    public async Task DecliningLeavesTheToolOnTheRelayServer()
    {
        Rig rig = await SignedInAsync(reachable: true, userSaysYes: false);

        await rig.Dashboard.LocalProxy.ToggleAsync(rig.Dashboard.LocalProxy.CodexAccounts[0]);

        Assert.Empty(rig.Codex.LocalProxies);
        Assert.False(rig.Dashboard.LocalProxy.IsCodexActive);
    }

    [Fact]
    public async Task AnUnreachableHostIsSaidPlainlyAndSwitchingOnAnywayShowsTheProblem()
    {
        Rig rig = await SignedInAsync(reachable: false, userSaysYes: true);

        await rig.Dashboard.LocalProxy.ToggleAsync(rig.Dashboard.LocalProxy.CodexAccounts[0]);

        (string message, string label) = Assert.Single(rig.Asked);
        Assert.Contains("连不上官方服务器", message);
        Assert.Contains("代理/VPN", message);
        Assert.Equal("仍然开启", label);
        Assert.True(rig.Dashboard.LocalProxy.IsCodexActive);
        Assert.Contains("代理/VPN", rig.Dashboard.LocalProxy.CodexError);
    }

    [Fact]
    public async Task WithNoOneToAskAnUnreachableHostIsRefused()
    {
        Rig rig = await SignedInAsync(reachable: false, userSaysYes: true);
        rig.Dashboard.LocalProxy.ConfirmEnable = null;

        await rig.Dashboard.LocalProxy.ToggleAsync(rig.Dashboard.LocalProxy.CodexAccounts[0]);

        Assert.Empty(rig.Codex.LocalProxies);
        Assert.Contains("代理/VPN", rig.Dashboard.LocalProxy.ActionMessage);
    }

    [Fact]
    public async Task SwitchingCodexOnStartsChatGptWhenItIsNotRunning()
    {
        Rig rig = await SignedInAsync(reachable: true, userSaysYes: true);

        await rig.Dashboard.LocalProxy.ToggleAsync(rig.Dashboard.LocalProxy.CodexAccounts[0]);

        Assert.Contains("确定后会自动启动", rig.Asked[0].Message);
        Assert.Equal(1, rig.Codex.RunCount);
        Assert.False(rig.Codex.LastAllowRestart);
        Assert.True(rig.Dashboard.IsCodexRunning);
        Assert.EndsWith("ChatGPT 已启动。", rig.Dashboard.LocalProxy.ActionMessage);
    }

    [Fact]
    public async Task ARunningChatGptIsLeftAlone()
    {
        Rig rig = await SignedInAsync(reachable: true, userSaysYes: true);
        rig.Dashboard.IsCodexRunning = true;

        await rig.Dashboard.LocalProxy.ToggleAsync(rig.Dashboard.LocalProxy.CodexAccounts[0]);

        Assert.DoesNotContain("自动启动", rig.Asked[0].Message);
        Assert.Equal(0, rig.Codex.RunCount);
        Assert.DoesNotContain("ChatGPT", rig.Dashboard.LocalProxy.ActionMessage.Replace("ChatGPT 账号", string.Empty));
    }

    [Fact]
    public async Task DecliningOrSwitchingClaudeCodeOnStartsNothing()
    {
        Rig rig = await SignedInAsync(reachable: true, userSaysYes: false);
        await rig.Dashboard.LocalProxy.ToggleAsync(rig.Dashboard.LocalProxy.CodexAccounts[0]);

        Rig claude = await SignedInAsync(reachable: true, userSaysYes: true);
        await claude.Dashboard.LocalProxy.ToggleAsync(claude.Dashboard.LocalProxy.ClaudeAccounts[0]);

        Assert.Equal(0, rig.Codex.RunCount);
        Assert.Equal(0, claude.Codex.RunCount);
        Assert.DoesNotContain("自动启动", claude.Asked[0].Message);
    }

    [Fact]
    public async Task AFailedStartIsSaidOnThePage()
    {
        Rig rig = await SignedInAsync(reachable: true, userSaysYes: true);
        rig.Codex.OnRun = (_, _) => new CodexStartupResult(CodexStartupStatus.RelayUnavailable, "本机 Relay 启动失败。");

        await rig.Dashboard.LocalProxy.ToggleAsync(rig.Dashboard.LocalProxy.CodexAccounts[0]);

        Assert.True(rig.Dashboard.LocalProxy.IsCodexActive);
        Assert.EndsWith("ChatGPT 没有启动：本机 Relay 启动失败。", rig.Dashboard.LocalProxy.ActionMessage);
    }

    [Fact]
    public async Task ARestoredChoiceThatCannotReachTheOfficialHostSaysSoOnce()
    {
        Rig rig = await SignedInAsync(
            reachable: false,
            userSaysYes: true,
            saved: new LocalProxyChoice { CodexAccountId = 7, CodexAccountName = "我的 Plus" });

        Assert.True(rig.Dashboard.LocalProxy.IsCodexActive);
        Assert.Contains("代理/VPN", rig.Dashboard.LocalProxy.CodexError);
        Assert.Single(rig.Network.Checked);
        Assert.Contains("未切回中转站", Assert.Single(rig.Notified));
    }
}

/// <summary>Reading the proxy settings the way Windows stores them.</summary>
public sealed class SystemProxyReaderTests
{
    [Theory]
    [InlineData("127.0.0.1:7897", "http://127.0.0.1:7897/")]
    [InlineData("http=127.0.0.1:1080;https=127.0.0.1:1081;ftp=x:1", "http://127.0.0.1:1081/")]
    [InlineData("http=127.0.0.1:1080", "http://127.0.0.1:1080/")]
    [InlineData("socks5://127.0.0.1:7890", "socks5://127.0.0.1:7890/")]
    public void WindowsProxyServerValuesAreUnderstood(string value, string expected)
    {
        Assert.Equal(new Uri(expected), SystemProxyReader.ParseWindowsProxyServer(value));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    [InlineData("ftp=x:1")]
    public void NothingUsableMeansNoProxy(string? value)
    {
        Assert.Null(SystemProxyReader.ParseWindowsProxyServer(value));
    }

    [Theory]
    [InlineData("http://127.0.0.1:5000/", true)]
    [InlineData("http://localhost:5000/", true)]
    [InlineData("http://[::1]:5000/", true)]
    [InlineData("https://chatgpt.com/backend-api/codex/responses", false)]
    [InlineData("https://api.anthropic.com/v1/messages", false)]
    [InlineData("https://relay.corp.com/", true)]
    [InlineData("https://corp.com/", true)]
    [InlineData("http://10.1.2.3/", true)]
    [InlineData("http://intranet/", true)]
    [InlineData("https://notcorp.com/", false)]
    public void BypassIsMatchedOnTheHostAndNeverProxiesLoopback(string url, bool bypassed)
    {
        var proxy = new HostBypassProxy(new Uri("http://127.0.0.1:7897"), [".corp.com", "10.*"], bypassLocal: true);

        Assert.Equal(bypassed, proxy.IsBypassed(new Uri(url)));
        Assert.Equal(bypassed ? null : new Uri("http://127.0.0.1:7897"), proxy.GetProxy(new Uri(url)));
    }

    [Fact]
    public void NoProxyStyleNamesCoverTheirSubdomains()
    {
        var proxy = new HostBypassProxy(new Uri("http://p:1"), ["example.com"], bypassLocal: false);

        Assert.True(proxy.IsBypassed(new Uri("https://api.example.com/")));
        Assert.False(proxy.IsBypassed(new Uri("http://intranet/")));
    }
}

/// <summary>The one real request made before switching on.</summary>
public sealed class OfficialReachabilityTests
{
    [Fact]
    public async Task AnyHttpAnswerCountsAsReachable()
    {
        using HttpListener listener = LoopbackHttpListener.Start(null, out int port);
        Task answer = Task.Run(async () =>
        {
            HttpListenerContext context = await listener.GetContextAsync();
            context.Response.StatusCode = 403;
            context.Response.Close();
        });

        Reachability result = await new OfficialReachability().CheckAsync(new Uri($"http://127.0.0.1:{port}/backend-api/codex/responses"));
        await answer;

        Assert.True(result.Reachable);
        Assert.False(string.IsNullOrEmpty(result.ProxyDescription));
    }

    [Fact]
    public async Task NoAnswerIsUnreachableWithAReason()
    {
        int port = FreePort();

        Reachability result = await new OfficialReachability().CheckAsync(new Uri($"http://127.0.0.1:{port}/"));

        Assert.False(result.Reachable);
        Assert.False(string.IsNullOrEmpty(result.Problem));
    }

    private static int FreePort() => LoopbackHttpListener.ProbeFreePort();
}
