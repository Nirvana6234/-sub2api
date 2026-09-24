using System.Text.Json;
using LanAi.Workspace.Chat;
using LanAi.Workspace.Core;
using LanAi.Workspace.Terminal;

namespace AiSwitch.Chat.Tests;

internal sealed class FakeStructuredCliProcess : IStructuredCliProcess
{
    public event EventHandler<string>? OutputLineReceived;

    public event EventHandler<string>? ErrorLineReceived;

    public event EventHandler<int>? Exited;

    public bool IsRunning { get; private set; }

    public TerminalCommand? StartedCommand { get; private set; }

    public List<string> WrittenLines { get; } = [];

    public Func<JsonElement, IReadOnlyList<string>>? AutoResponder { get; set; }

    public Task StartAsync(TerminalCommand command, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        StartedCommand = command;
        IsRunning = true;
        return Task.CompletedTask;
    }

    public Task WriteLineAsync(string line, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        WrittenLines.Add(line);
        if (AutoResponder is not null)
        {
            using JsonDocument document = JsonDocument.Parse(line);
            foreach (string response in AutoResponder(document.RootElement))
            {
                EmitOutput(response);
            }
        }

        return Task.CompletedTask;
    }

    public Task StopAsync(TimeSpan timeout, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        IsRunning = false;
        return Task.CompletedTask;
    }

    public void EmitOutput(string line) => OutputLineReceived?.Invoke(this, line);

    public void EmitError(string line) => ErrorLineReceived?.Invoke(this, line);

    public void EmitExit(int exitCode)
    {
        IsRunning = false;
        Exited?.Invoke(this, exitCode);
    }

    public ValueTask DisposeAsync()
    {
        IsRunning = false;
        return ValueTask.CompletedTask;
    }
}

internal sealed class StubCredentialProvider(string? secret = null) : IConnectionCredentialProvider
{
    public ValueTask<string?> GetSecretAsync(
        string connectionProfileId,
        CliKind client,
        CancellationToken cancellationToken = default) => ValueTask.FromResult(secret);
}

internal static class ChatEngineTestSupport
{
    public static ChatEngineContext CreateContext(
        CliKind kind,
        CliLaunchMode mode = CliLaunchMode.New,
        string? nativeSessionId = null,
        ConnectionProfile? connection = null,
        ChatPermissionMode permissionMode = ChatPermissionMode.WorkspaceWrite) => new()
    {
        LaunchRequest = new CliLaunchRequest
        {
            ProjectId = "project-1",
            Cli = kind,
            WorkingDirectory = Environment.CurrentDirectory,
            Mode = mode,
            NativeSessionId = nativeSessionId,
            Model = "test-model",
        },
        Installation = new CliInstallation
        {
            Kind = kind,
            IsInstalled = true,
            ExecutablePath = kind switch
            {
                CliKind.Codex => @"C:\tools\codex.exe",
                CliKind.ClaudeCode => @"C:\tools\claude.exe",
                CliKind.GeminiCli => @"C:\tools\gemini.exe",
                _ => throw new ArgumentOutOfRangeException(nameof(kind)),
            },
            Version = kind switch
            {
                CliKind.Codex => "0.144.1",
                CliKind.ClaudeCode => "2.1.175",
                CliKind.GeminiCli => "0.43.0",
                _ => null,
            },
            DetectedAt = DateTimeOffset.UtcNow,
        },
        Connection = connection,
        PermissionMode = permissionMode,
    };

    public static JsonDocument ParseWritten(FakeStructuredCliProcess process, int index) =>
        JsonDocument.Parse(process.WrittenLines[index]);

    public static IReadOnlyList<string> GeminiHandshakeResponder(JsonElement message)
    {
        if (!message.TryGetProperty("method", out JsonElement methodElement))
        {
            return Array.Empty<string>();
        }

        if (!message.TryGetProperty("id", out JsonElement idElement))
        {
            return Array.Empty<string>();
        }

        string? method = methodElement.GetString();
        long id = idElement.GetInt64();
        return method switch
        {
            "initialize" => [$"{{\"jsonrpc\":\"2.0\",\"id\":{id},\"result\":{{\"protocolVersion\":1}}}}"],
            "session/new" => [$"{{\"jsonrpc\":\"2.0\",\"id\":{id},\"result\":{{\"sessionId\":\"gemini-session-1\"}}}}"],
            "session/load" => [$"{{\"jsonrpc\":\"2.0\",\"id\":{id},\"result\":{{}}}}"],
            _ => Array.Empty<string>(),
        };
    }
}
