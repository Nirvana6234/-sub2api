using LanAi.RelayClient.Platform.MacOS;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// The half of the macOS notification path that can be tested without a Mac.
/// </summary>
/// <remarks>
/// The announcement title in these tests is operator-authored text that arrived over
/// the network and ends up inside an AppleScript string literal. Getting the escaping
/// wrong there is not a display bug — it is arbitrary script running as the user.
/// </remarks>
public sealed class AppleScriptNotificationTests
{
    [Fact]
    public void OrdinaryTextIsPlacedInBothLiterals()
    {
        string script = AppleScriptNotification.Compose("共飞有新公告", "服务器将于今晚维护。");

        Assert.Equal(
            "display notification \"服务器将于今晚维护。\" with title \"共飞有新公告\"",
            script);
    }

    /// <remarks>
    /// The attack this closes: a title ending the literal and appending a statement
    /// of its own. After escaping there is no unescaped quotation mark left, so
    /// everything the operator wrote stays inside the literal.
    /// </remarks>
    [Fact]
    public void AQuotationMarkCannotCloseTheLiteral()
    {
        string script = AppleScriptNotification.Compose(
            "标题\" & (do shell script \"whoami\") & \"",
            "正文");

        Assert.DoesNotContain("do shell script \"whoami\"", script, StringComparison.Ordinal);
        Assert.Contains("\\\" & (do shell script \\\"whoami\\\") & \\\"", script, StringComparison.Ordinal);
    }

    /// <remarks>
    /// The order-of-replacement bug this pins: escaping the quotation mark first would
    /// turn <c>"</c> into <c>\"</c> and then the backslash pass would turn that into
    /// <c>\\"</c> — a literal backslash and an unescaped quotation mark, which is the
    /// hole rather than the fix.
    /// </remarks>
    [Fact]
    public void ABackslashIsEscapedBeforeTheQuotationMark()
    {
        string script = AppleScriptNotification.Compose("t", "a\\\"b");

        Assert.Contains("\"a\\\\\\\"b\"", script, StringComparison.Ordinal);
    }

    /// <remarks>
    /// AppleScript string literals cannot span lines, so a multi-line announcement
    /// title would be a syntax error and the notification would silently not appear.
    /// </remarks>
    [Fact]
    public void NewlinesBecomeSpacesSoTheLiteralStaysOnOneLine()
    {
        string script = AppleScriptNotification.Compose("标题", "第一行\r\n第二行");

        Assert.DoesNotContain("\n", script, StringComparison.Ordinal);
        Assert.Contains("第一行  第二行", script, StringComparison.Ordinal);
    }

    [Fact]
    public void EmptyTextStillProducesAValidStatement()
    {
        string script = AppleScriptNotification.Compose(string.Empty, string.Empty);

        Assert.Equal("display notification \"\" with title \"\"", script);
    }
}
