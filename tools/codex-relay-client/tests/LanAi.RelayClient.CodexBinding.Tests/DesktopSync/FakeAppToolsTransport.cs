using System.Buffers.Binary;
using System.Text;
using System.Text.Json;
using System.Threading.Channels;
using LanAi.RelayClient.CodexBinding.DesktopSync;

namespace LanAi.RelayClient.CodexBinding.Tests.DesktopSync;

/// <summary>What a scripted pipe does with one request.</summary>
internal abstract record PipeReply
{
    public sealed record Json(string Body) : PipeReply;

    /// <summary>Never answer, like a pipe that speaks some other protocol.</summary>
    public sealed record Silence : PipeReply;

    /// <summary>Close the pipe instead of answering, like the desktop app exiting.</summary>
    public sealed record Hangup : PipeReply;
}

/// <summary>A set of named pipes, each answering requests through a script.</summary>
internal sealed class FakeAppToolsTransport : IAppToolsTransport
{
    private readonly Dictionary<string, Func<JsonElement, PipeReply>> _pipes = new(StringComparer.Ordinal);

    public List<(string Pipe, string Method, string? Tool)> Requests { get; } = [];

    public int Connects { get; private set; }

    public void Add(string name, Func<JsonElement, PipeReply> script) => _pipes[name] = script;

    public void Remove(string name) => _pipes.Remove(name);

    public IReadOnlyList<string> ListCandidates() => _pipes.Keys.Order(StringComparer.Ordinal).ToList();

    public Task<Stream> ConnectAsync(string pipeName, CancellationToken cancellationToken)
    {
        if (!_pipes.TryGetValue(pipeName, out Func<JsonElement, PipeReply>? script))
        {
            throw new IOException($"No pipe {pipeName}.");
        }

        Connects++;
        return Task.FromResult<Stream>(new ScriptedPipeStream(request =>
        {
            string method = request.GetProperty("method").GetString()!;
            string? tool = method == "tools/call" ? request.GetProperty("params").GetProperty("tool").GetString() : null;
            Requests.Add((pipeName, method, tool));
            return script(request);
        }));
    }

    /// <summary>The standard app-tools answers, for pipes that behave.</summary>
    public static Func<JsonElement, PipeReply> AppTools(
        Func<string, JsonElement, PipeReply> onCall,
        string toolsJson = ToolSchemas.All) => request =>
        request.GetProperty("method").GetString() switch
        {
            "tools/list" => new PipeReply.Json(Response(request, $$"""{"tools":{{toolsJson}}}""")),
            "tools/call" => onCall(request.GetProperty("params").GetProperty("tool").GetString()!, request),
            _ => new PipeReply.Json(Error(request, -1, "No handler registered for method")),
        };

    /// <summary>What the other <c>codex-browser-use-*</c> pipes answer.</summary>
    public static PipeReply NotAppTools(JsonElement request) =>
        new PipeReply.Json(Error(request, -1, $"No handler registered for method: {request.GetProperty("method").GetString()}"));

    public static string Response(JsonElement request, string resultJson) =>
        $$"""{"id":{{request.GetProperty("id").GetRawText()}},"jsonrpc":"2.0","result":{{resultJson}}}""";

    public static string Error(JsonElement request, int code, string message) =>
        $$"""{"error":{"code":{{code}},"message":{{JsonSerializer.Serialize(message)}}},"id":{{request.GetProperty("id").GetRawText()}},"jsonrpc":"2.0"}""";

    /// <summary>A tool result: the payload travels as JSON text inside <c>contentItems</c>.</summary>
    public static PipeReply ToolResult(JsonElement request, string payloadJson, bool success = true) =>
        new PipeReply.Json(Response(request,
            $$"""{"contentItems":[{"text":{{JsonSerializer.Serialize(payloadJson)}},"type":"inputText"}],"success":{{(success ? "true" : "false")}}}"""));

    /// <summary>A duplex stream that frames like the real pipe and answers from a script.</summary>
    private sealed class ScriptedPipeStream(Func<JsonElement, PipeReply> script) : Stream
    {
        private readonly MemoryStream _incoming = new();
        private readonly Channel<byte[]> _outgoing = Channel.CreateUnbounded<byte[]>();
        private byte[] _current = [];
        private int _position;

        public override bool CanRead => true;
        public override bool CanSeek => false;
        public override bool CanWrite => true;
        public override long Length => throw new NotSupportedException();
        public override long Position { get => throw new NotSupportedException(); set => throw new NotSupportedException(); }

        public override void Write(byte[] buffer, int offset, int count) => Write(buffer.AsSpan(offset, count));

        public override void Write(ReadOnlySpan<byte> buffer)
        {
            _incoming.Write(buffer);
            byte[] data = _incoming.ToArray();
            while (data.Length >= 4)
            {
                int length = (int)BinaryPrimitives.ReadUInt32LittleEndian(data);
                if (data.Length < 4 + length)
                {
                    break;
                }

                using (JsonDocument request = JsonDocument.Parse(data.AsMemory(4, length)))
                {
                    switch (script(request.RootElement))
                    {
                        case PipeReply.Json json:
                            byte[] body = Encoding.UTF8.GetBytes(json.Body);
                            byte[] frame = new byte[4 + body.Length];
                            BinaryPrimitives.WriteUInt32LittleEndian(frame, (uint)body.Length);
                            body.CopyTo(frame, 4);
                            _outgoing.Writer.TryWrite(frame);
                            break;
                        case PipeReply.Hangup:
                            _outgoing.Writer.TryComplete();
                            break;
                    }
                }

                data = data[(4 + length)..];
            }

            _incoming.SetLength(0);
            _incoming.Write(data);
        }

        public override ValueTask WriteAsync(ReadOnlyMemory<byte> buffer, CancellationToken cancellationToken = default)
        {
            Write(buffer.Span);
            return ValueTask.CompletedTask;
        }

        public override async ValueTask<int> ReadAsync(Memory<byte> buffer, CancellationToken cancellationToken = default)
        {
            if (_position >= _current.Length)
            {
                if (!await _outgoing.Reader.WaitToReadAsync(cancellationToken))
                {
                    return 0;
                }

                _current = await _outgoing.Reader.ReadAsync(cancellationToken);
                _position = 0;
            }

            int count = Math.Min(buffer.Length, _current.Length - _position);
            _current.AsMemory(_position, count).CopyTo(buffer);
            _position += count;
            return count;
        }

        public override int Read(byte[] buffer, int offset, int count) =>
            ReadAsync(buffer.AsMemory(offset, count)).AsTask().GetAwaiter().GetResult();

        public override void Flush() { }
        public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();
        public override void SetLength(long value) => throw new NotSupportedException();
    }
}

/// <summary>
/// The four tool schemas sync depends on, as the desktop app (26.903) declares them —
/// trimmed to the members the contract check reads. Descriptions are omitted: they
/// change with the model list and the check deliberately ignores them.
/// </summary>
internal static class ToolSchemas
{
    public const string ListThreads =
        """{"name":"list_threads","namespace":"codex_app","inputSchema":{"type":"object","additionalProperties":false,"properties":{"limit":{"type":"integer","minimum":1,"maximum":50}}}}""";

    public const string ReadThread =
        """{"name":"read_thread","namespace":"codex_app","inputSchema":{"type":"object","additionalProperties":false,"properties":{"threadId":{"type":"string"},"hostId":{"type":"string"},"cursor":{"type":"string"},"turnLimit":{"type":"integer"},"includeOutputs":{"type":"boolean"},"maxOutputCharsPerItem":{"type":"integer"}},"required":["threadId"]}}""";

    public const string SendMessage =
        """{"name":"send_message_to_thread","namespace":"codex_app","inputSchema":{"type":"object","additionalProperties":false,"properties":{"threadId":{"type":"string"},"hostId":{"type":"string"},"prompt":{"type":"string"},"model":{"type":"string"},"thinking":{"type":"string"}},"required":["threadId","prompt"]}}""";

    public const string Navigate =
        """{"name":"navigate_to_codex_page","namespace":"codex_app","inputSchema":{"type":"object","additionalProperties":false,"properties":{"threadId":{"type":"string","minLength":1}},"required":["threadId"]}}""";

    public const string Unrelated =
        """{"name":"fire_confetti","namespace":"codex_app","inputSchema":{"type":"object","properties":{}}}""";

    public const string All = "[" + ListThreads + "," + ReadThread + "," + SendMessage + "," + Navigate + "," + Unrelated + "]";
}
