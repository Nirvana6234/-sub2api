using System.IO;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class BrandingTests
{
    [Fact]
    public void VisibleBrandingUsesTheChatGptAssistantNameAndCopy()
    {
        string root = FindRepositoryRoot();
        string client = Path.Combine(root, "tools", "codex-relay-client", "src", "LanAi.RelayClient");
        string mainWindow = File.ReadAllText(Path.Combine(client, "MainWindow.xaml"));
        string mainWindowCode = File.ReadAllText(Path.Combine(client, "MainWindow.xaml.cs"));
        string project = File.ReadAllText(Path.Combine(client, "LanAi.RelayClient.csproj"));
        string tray = File.ReadAllText(Path.Combine(client, "Services", "TrayPresence.cs"));

        Assert.Contains("Title=\"共飞-ChatGPT助手\"", mainWindow, StringComparison.Ordinal);
        Assert.Contains("拒绝高价，拒绝包月，畅快使用 ChatGPT 工作生活", mainWindow, StringComparison.Ordinal);
        Assert.Contains("登录后即可使用", mainWindow, StringComparison.Ordinal);
        Assert.Contains("<AssemblyTitle>共飞-ChatGPT助手</AssemblyTitle>", project, StringComparison.Ordinal);
        Assert.Contains("<Product>共飞-ChatGPT助手</Product>", project, StringComparison.Ordinal);
        Assert.Contains("Text = \"共飞-ChatGPT助手\"", tray, StringComparison.Ordinal);
        Assert.Contains("menu.Items.Add(\"启动 ChatGPT\"", tray, StringComparison.Ordinal);
        Assert.Contains("保持运行，ChatGPT 才能继续使用共飞额度。", tray, StringComparison.Ordinal);
        Assert.DoesNotContain("共飞直连客户端", mainWindow, StringComparison.Ordinal);
        Assert.DoesNotContain("共飞直连客户端", mainWindowCode, StringComparison.Ordinal);
        Assert.DoesNotContain("共飞直连客户端", tray, StringComparison.Ordinal);
        Assert.DoesNotContain("启动 Codex", tray, StringComparison.Ordinal);
    }

    private static string FindRepositoryRoot()
    {
        for (DirectoryInfo? directory = new(AppContext.BaseDirectory); directory is not null; directory = directory.Parent)
        {
            string candidate = Path.Combine(
                directory.FullName,
                "tools",
                "codex-relay-client",
                "src",
                "LanAi.RelayClient",
                "MainWindow.xaml");
            if (File.Exists(candidate))
            {
                return directory.FullName;
            }
        }

        throw new DirectoryNotFoundException("Could not locate the repository root from the test output directory.");
    }
}
