using System.Text;
using System.Text.Json;

namespace LanAi.RelayClient.CodexBinding.Tests.DesktopSync;

/// <summary>
/// Writes synthetic rollout lines in the shapes Codex desktop 26.903 writes them.
/// </summary>
/// <remarks>
/// Every shape here was copied from a real rollout, field for field; only the content
/// is made up. Real rollouts are not used as fixtures because they hold the user's own
/// conversations.
/// </remarks>
internal sealed class RolloutBuilder
{
    private readonly StringBuilder _text = new();
    private int _clock;

    public string Text => _text.ToString();

    public RolloutBuilder SessionMeta(string cwd) =>
        Line("session_meta", $$"""{"id":"thread-1","cwd":{{Q(cwd)}},"originator":"Codex Desktop","cli_version":"0.153.4","model_provider":"gongfei","base_instructions":"…"}""");

    public RolloutBuilder TurnStarted(string turn) =>
        Event($$"""{"type":"task_started","turn_id":{{Q(turn)}},"started_at":1790176763,"model_context_window":258400,"collaboration_mode_kind":"default"}""");

    public RolloutBuilder TurnContext(string turn, string cwd) =>
        Line("turn_context", $$"""{"turn_id":{{Q(turn)}},"cwd":{{Q(cwd)}},"approval_policy":"never","sandbox_policy":{"type":"danger-full-access"},"model":"gpt-5.5"}""");

    public RolloutBuilder TurnComplete(string turn, string? error = null) =>
        Event(error is null
            ? $$"""{"type":"task_complete","turn_id":{{Q(turn)}},"last_agent_message":"done","started_at":1,"completed_at":9,"duration_ms":8159}"""
            : $$"""{"type":"task_complete","turn_id":{{Q(turn)}},"last_agent_message":null,"error":{"message":{{Q(error)}},"codex_error_info":"other"},"started_at":1,"completed_at":2,"duration_ms":18838}""");

    public RolloutBuilder TurnAborted(string turn) =>
        Event($$"""{"type":"turn_aborted","turn_id":{{Q(turn)}},"reason":"interrupted","started_at":1,"completed_at":2,"duration_ms":6884}""");

    public RolloutBuilder TokenCount() =>
        Event("""{"type":"token_count","info":{"total_token_usage":{"input_tokens":1}},"rate_limits":null}""");

    public RolloutBuilder User(string turn, string id, string text, params string[] imagePaths)
    {
        string images = string.Concat(imagePaths.Select(p => $$""",{"type":"local_image","path":{{Q(p)}}}"""));
        return Item(turn, $$"""{"type":"UserMessage","id":{{Q(id)}},"client_id":"c","content":[{"type":"text","text":{{Q(text)}},"text_elements":[]}{{images}}]}""");
    }

    public RolloutBuilder Delegated(string turn, string id, string source, string input)
    {
        string output = $"<codex_delegation>\n  <source_thread_id>{source}</source_thread_id>\n  <input>{input}</input>\n</codex_delegation>";
        Line("response_item", $$"""{"type":"function_call_output","id":{{Q(id)}},"name":"send_message_to_thread","namespace":"codex_app","output":{{Q(output)}}}""");
        return Item(turn, $$"""{"type":"FunctionCallOutput","id":{{Q(id)}},"name":"send_message_to_thread","namespace":"codex_app","output":{{Q(output)}}}""");
    }

    public RolloutBuilder ToolOutput(string turn, string id, string name, string output) =>
        Item(turn, $$"""{"type":"FunctionCallOutput","id":{{Q(id)}},"name":{{Q(name)}},"namespace":"codex_app","output":{{Q(output)}}}""");

    public RolloutBuilder Agent(string turn, string id, string text, string phase = "commentary") =>
        Item(turn, $$"""{"type":"AgentMessage","id":{{Q(id)}},"content":[{"type":"Text","text":{{Q(text)}}}],"phase":{{Q(phase)}}}""");

    public RolloutBuilder Reasoning(string turn, string id, params string[] summary) =>
        Item(turn, $$"""{"type":"Reasoning","id":{{Q(id)}},"summary_text":[{{string.Join(',', summary.Select(Q))}}],"raw_content":[]}""")
        .Line("response_item", $$"""{"type":"reasoning","id":{{Q(id)}},"summary":[],"encrypted_content":"gAAAAAB-secret"}""");

    /// <summary>The line written when a command starts, before it has any result.</summary>
    public RolloutBuilder ExecCall(string callId, string cmd) =>
        Line("response_item", $$"""{"type":"function_call","id":"fc_1","name":"exec_command","arguments":{{Q(JsonSerializer.Serialize(new { cmd, workdir = "C:\\w", yield_time_ms = 10000 }))}},"call_id":{{Q(callId)}}}""");

    /// <summary>
    /// A <c>shell_command</c> call. Unlike <c>exec_command</c>, Codex never follows it
    /// with a completed item; only <see cref="ShellOutput"/> says how it ended.
    /// </summary>
    public RolloutBuilder ShellCall(string callId, string command) =>
        Line("response_item", $$"""{"type":"function_call","name":"shell_command","arguments":{{Q(JsonSerializer.Serialize(new { command, workdir = "C:\\w", timeout_ms = 120000 }))}},"call_id":{{Q(callId)}}}""");

    /// <summary>The output line of a call, in the <c>Exit code: N / Wall time / Output:</c> form.</summary>
    public RolloutBuilder ShellOutput(string callId, string output) =>
        Line("response_item", $$"""{"type":"function_call_output","call_id":{{Q(callId)}},"output":{{Q(output)}}}""");

    public RolloutBuilder SubAgent(string turn, string id) =>
        Item(turn, $$"""{"type":"SubAgentActivity","id":{{Q(id)}},"kind":"spawned","agent_path":"/root/plugin_filter_tests","agent_thread_id":"child-1"}""");

    /// <summary>The same for a conversation in code mode, where commands run from a JS cell.</summary>
    public RolloutBuilder CodeModeCall(string callId, string js) =>
        Line("response_item", $$"""{"type":"custom_tool_call","id":"ctc_1","status":"completed","call_id":{{Q(callId)}},"name":"exec","input":{{Q(js)}}}""");

    public RolloutBuilder Command(string turn, string id, string command, string output, int exitCode = 0, string status = "completed")
    {
        string argv = $$"""[{{Q(@"C:\Users\tester\.cache\codex-runtimes\pwsh.exe")}},"-Command",{{Q(command)}}]""";
        return Item(turn, $$"""{"type":"CommandExecution","id":{{Q(id)}},"process_id":"1","command":{{argv}},"cwd":"file:///C:/w","parsed_cmd":[{"type":"unknown","cmd":{{Q(command)}}}],"source":"unified_exec_startup","status":{{Q(status)}},"stdout":{{Q(output)}},"stderr":"","aggregated_output":{{Q(output)}},"exit_code":{{exitCode}}}""",
            timing: ""","started_at_ms":1000,"completed_at_ms":1647""");
    }

    public RolloutBuilder CommandWithoutParse(string turn, string id, params string[] argv) =>
        Item(turn, $$"""{"type":"CommandExecution","id":{{Q(id)}},"command":[{{string.Join(',', argv.Select(Q))}}],"status":"completed","aggregated_output":""}""");

    public RolloutBuilder FileChange(string turn, string id, string path, string diff, string kind = "update") =>
        Item(turn, $$$"""{"type":"FileChange","id":{{{Q(id)}}},"changes":{{{{Q(path)}}}:{"type":{{{Q(kind)}}},"unified_diff":{{{Q(diff)}}},"move_path":null}},"status":"completed","stdout":"Success."}""");

    public RolloutBuilder ImageView(string turn, string id, string path) =>
        Item(turn, $$"""{"type":"ImageView","id":{{Q(id)}},"path":{{Q(path)}}}""");

    public RolloutBuilder McpCall(string turn, string id) =>
        Item(turn, $$$"""{"type":"McpToolCall","id":{{{Q(id)}}},"server":"codex","tool":"read_mcp_resource","arguments":{},"status":"failed","error":{"message":"unknown MCP server"}}""");

    public RolloutBuilder Compaction(string turn, string id) =>
        Line("compacted", """{"message":"Another language model started…","replacement_history":[]}""")
        .Item(turn, $$"""{"type":"ContextCompaction","id":{{Q(id)}}}""");

    public RolloutBuilder Raw(string turn, string itemJson) => Item(turn, itemJson);

    /// <summary>A line the desktop app has started writing but not finished.</summary>
    public string HalfLine(string turn) =>
        $$"""{"timestamp":"2026-09-23T16:00:00.000Z","type":"event_msg","payload":{"type":"item_completed","turn_id":{{Q(turn)}},"item":{"type":"AgentMes""";

    private RolloutBuilder Item(string turn, string itemJson, string timing = "") =>
        Event($$"""{"type":"item_completed","thread_id":"thread-1","turn_id":{{Q(turn)}},"item":{{itemJson}}{{timing}}}""");

    private RolloutBuilder Event(string payload) => Line("event_msg", payload);

    private RolloutBuilder Line(string type, string payload)
    {
        _clock++;
        _text.Append($$"""{"timestamp":"2026-09-23T15:{{_clock / 60:00}}:{{_clock % 60:00}}.000Z","type":{{Q(type)}},"payload":{{payload}}}""").Append('\n');
        return this;
    }

    private static string Q(string value) => JsonSerializer.Serialize(value);
}
