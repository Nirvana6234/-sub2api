using System.IO;
using LanAi.Workspace.Core;
using LanAi.Workspace.Wpf.ViewModels;

namespace LanAi.Workspace.Wpf.Views;

internal sealed record TerminalConversationIntent(
    string Id,
    string NativeSessionId,
    string? Title,
    ResumePolicy ResumePolicy);

/// <summary>
/// Immutable snapshot of every UI choice that can affect one CLI launch. It is
/// captured synchronously before detection or profile I/O starts, preventing a
/// later selection change from mixing project A with connection or history B.
/// </summary>
internal sealed record TerminalLaunchIntent(
    string ProjectId,
    string ProjectPathFingerprint,
    string WorkingDirectory,
    CliKind Cli,
    string? ConnectionProfileId,
    string ConnectionLabel,
    string? Model,
    ResumePolicy ProjectResumePolicy,
    TerminalConversationIntent? Conversation)
{
    public static TerminalLaunchIntent Capture(TerminalViewModel viewModel)
    {
        ArgumentNullException.ThrowIfNull(viewModel);

        ProjectRecord project = viewModel.SelectedProjectRecord
            ?? throw new InvalidOperationException("请先选择一个项目。");
        string workingDirectory = PathIdentity.Normalize(project.RootPath);
        if (!Directory.Exists(workingDirectory))
        {
            throw new DirectoryNotFoundException($"项目目录不存在：{workingDirectory}");
        }

        CliKind cli = viewModel.SelectedCliKind;
        ConversationRecord? pending = viewModel.PendingConversation;
        bool conversationMatches = pending is not null &&
            pending.NativeClient == cli &&
            (string.Equals(pending.ProjectId, project.Id, StringComparison.OrdinalIgnoreCase) ||
             string.Equals(
                 pending.ProjectId,
                 project.PathFingerprint,
                 StringComparison.OrdinalIgnoreCase) ||
             PathsEqual(pending.OriginalWorkingDirectory, workingDirectory));

        TerminalConversationIntent? conversation = conversationMatches
            ? new TerminalConversationIntent(
                pending!.Id,
                pending.NativeSessionId,
                pending.Title,
                pending.ResumePolicy)
            : null;
        string? connectionProfileId = viewModel.EffectiveConnectionProfileId;
        if (string.IsNullOrWhiteSpace(connectionProfileId))
        {
            string message = pending?.ResumePolicy == ResumePolicy.PinnedConnection
                ? "该历史会话没有可用的绑定连接来源，请先在连接中心恢复该来源后再继续。"
                : "请先在连接中心选择一个有效连接来源，再启动官方 CLI。";
            throw new InvalidOperationException(message);
        }

        return new TerminalLaunchIntent(
            project.Id,
            project.PathFingerprint,
            workingDirectory,
            cli,
            connectionProfileId,
            viewModel.SelectedConnection,
            project.DefaultCli == cli ? project.DefaultModel : null,
            project.ResumePolicy,
            conversation);
    }

    public CliLaunchRequest CreateRequest(ConnectionProfile? connection)
        => new()
        {
            ProjectId = ProjectId,
            Cli = Cli,
            WorkingDirectory = WorkingDirectory,
            Mode = Conversation is null ? CliLaunchMode.New : CliLaunchMode.Resume,
            ConnectionProfileId = connection?.Id,
            Model = Model,
            ConversationId = Conversation?.Id,
            NativeSessionId = Conversation?.NativeSessionId,
            ResumePolicy = Conversation?.ResumePolicy ?? ProjectResumePolicy,
        };

    private static bool PathsEqual(string left, string right)
    {
        try
        {
            return string.Equals(
                PathIdentity.Normalize(left),
                PathIdentity.Normalize(right),
                OperatingSystem.IsWindows()
                    ? StringComparison.OrdinalIgnoreCase
                    : StringComparison.Ordinal);
        }
        catch (Exception exception) when (
            exception is ArgumentException or NotSupportedException or PathTooLongException)
        {
            return false;
        }
    }
}
