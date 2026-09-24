using LanAi.RelayClient.DesktopSync;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests.DesktopSync;

/// <summary>How the audit list on 「同步会话」 words each outcome the agent writes.</summary>
public sealed class SyncAuditItemTests
{
    [Theory]
    [InlineData("queued_for_desktop", "排队中（等 ChatGPT 启动）", false)]
    [InlineData("expired", "已丢弃", true)]
    [InlineData("unavailable", "未发送（ChatGPT 没有运行）", true)]
    [InlineData("unavailable: 这个账号还没有可用于 Codex 的分组", "未发送（这个账号还没有可用于 Codex 的分组）", true)]
    [InlineData("unconfirmed", "未确认", true)]
    [InlineData("failed: boom", "失败（boom）", true)]
    [InlineData("ok", "成功", false)]
    [InlineData("started", "已开始", false)]
    public void EachOutcomeReadsAsWhatHappened(string outcome, string expected, bool flagged)
    {
        var item = new SyncAuditItem(new SyncAuditEntry(DateTimeOffset.UtcNow, 5, "iPhone", DesktopSyncCommands.SendMessage, "t", "你好", outcome));

        Assert.Contains(expected, item.Text);
        Assert.DoesNotContain("已拒绝", item.Text);
        Assert.Equal(flagged, item.IsRefusal);
    }
}
