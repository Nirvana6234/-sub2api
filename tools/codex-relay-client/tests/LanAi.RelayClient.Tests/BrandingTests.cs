using System.IO;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class BrandingTests
{
    [Fact]
    public void VisibleBrandingUsesTheAssistantNameAndKeepsChatGptCopy()
    {
        string shell = AppSource.Read("ShellWindow.axaml");
        string signIn = AppSource.Read("Views", "SignInView.axaml");
        string project = AppSource.Read("LanAi.RelayClient.App.csproj");
        string tray = AppSource.Read("Services", "TrayPresence.cs");
        string app = AppSource.Read("App.axaml.cs");

        Assert.Contains("Title=\"共飞 AI 助手\"", shell, StringComparison.Ordinal);
        Assert.Contains("拒绝高价，拒绝包月，畅快使用 ChatGPT 工作生活", signIn, StringComparison.Ordinal);
        Assert.Contains("登录后即可使用", signIn, StringComparison.Ordinal);
        Assert.Contains("Text=\"共飞 AI 助手\"", signIn, StringComparison.Ordinal);

        // File properties stay as shipped: the exe and shortcut names are what the
        // self-updater and the installed shortcuts already point at.
        Assert.Contains("<AssemblyTitle>共飞-ChatGPT助手</AssemblyTitle>", project, StringComparison.Ordinal);
        Assert.Contains("<Product>共飞-ChatGPT助手</Product>", project, StringComparison.Ordinal);
        Assert.Contains("ToolTipText = \"共飞 AI 助手\"", tray, StringComparison.Ordinal);
        Assert.Contains("new NativeMenuItem(\"启动 ChatGPT\")", tray, StringComparison.Ordinal);
        Assert.Contains("保持运行，ChatGPT 才能继续使用共飞额度。", app, StringComparison.Ordinal);
        Assert.DoesNotContain("共飞直连客户端", shell, StringComparison.Ordinal);
        Assert.DoesNotContain("共飞直连客户端", signIn, StringComparison.Ordinal);
        Assert.DoesNotContain("共飞直连客户端", tray, StringComparison.Ordinal);
        Assert.DoesNotContain("启动 Codex", tray, StringComparison.Ordinal);
    }
}

/// <summary>Reads the shipped (Avalonia) head's source files from the repository.</summary>
internal static class AppSource
{
    private static readonly Lazy<string> Directory = new(Locate);

    public static string Read(params string[] relativePath) =>
        File.ReadAllText(Path.Combine([Directory.Value, .. relativePath]));

    private static string Locate()
    {
        for (DirectoryInfo? directory = new(AppContext.BaseDirectory); directory is not null; directory = directory.Parent)
        {
            string candidate = Path.Combine(
                directory.FullName,
                "tools",
                "codex-relay-client",
                "src",
                "LanAi.RelayClient.App");
            if (File.Exists(Path.Combine(candidate, "LanAi.RelayClient.App.csproj")))
            {
                return candidate;
            }
        }

        throw new DirectoryNotFoundException("Could not locate the App head from the test output directory.");
    }
}
