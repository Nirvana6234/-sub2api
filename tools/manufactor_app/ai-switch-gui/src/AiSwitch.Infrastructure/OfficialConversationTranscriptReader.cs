using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

/// <summary>
/// Loads display-safe history from the official CLI-owned transcript selected
/// by native session id and exact working directory.
/// </summary>
public sealed class OfficialConversationTranscriptReader : IConversationTranscriptReader
{
    private readonly AppDataPaths _paths;

    public OfficialConversationTranscriptReader(AppDataPaths paths)
    {
        _paths = paths ?? throw new ArgumentNullException(nameof(paths));
    }

    public Task<ConversationTranscript> ReadAsync(
        ConversationRecord conversation,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(conversation);
        return conversation.NativeClient switch
        {
            CliKind.Codex => CodexConversationTranscriptReader.ReadAsync(
                _paths,
                conversation,
                cancellationToken),
            CliKind.ClaudeCode => ClaudeConversationTranscriptReader.ReadAsync(
                _paths,
                conversation,
                cancellationToken),
            CliKind.GeminiCli => GeminiConversationTranscriptReader.ReadAsync(
                _paths,
                conversation,
                cancellationToken),
            _ => Task.FromResult(ConversationTranscript.NotFound("不支持的官方会话类型。")),
        };
    }
}
