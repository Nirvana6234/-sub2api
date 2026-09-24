using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.Views;

namespace AiSwitch.Wpf.Tests;

public sealed class TerminalConnectionResolutionTests
{
    [Fact]
    public void ExplicitMissingId_DoesNotFallBackToMatchingNameOrKind()
    {
        ConnectionProfile[] profiles =
        [
            new ConnectionProfile
            {
                Id = "actual-id",
                Name = "局域网中转",
                Kind = ConnectionProfileKind.Lan,
                BaseUrl = "https://lan.example.test",
                EnabledClients = [CliKind.Codex],
            },
        ];

        ConnectionProfile? resolved = TerminalView.ResolveConnection(
            profiles,
            selectedId: "deleted-or-stale-id",
            selectedName: "局域网中转",
            CliKind.Codex);

        Assert.Null(resolved);
    }

    [Fact]
    public void ExplicitExistingId_ResolvesThatExactProfile()
    {
        ConnectionProfile expected = new()
        {
            Id = "exact-id",
            Name = "连接 A",
            BaseUrl = "https://a.example.test",
            EnabledClients = [CliKind.ClaudeCode],
        };

        ConnectionProfile? resolved = TerminalView.ResolveConnection(
            [expected],
            selectedId: "exact-id",
            selectedName: "已被界面改名的标签",
            CliKind.ClaudeCode);

        Assert.Same(expected, resolved);
    }
}
