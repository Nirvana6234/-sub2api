using System.Text.Json.Serialization;

namespace LanAi.RelayClient.WeChatIntent;

// The wire between the client and wechat-reader.exe: one JSON object per line, commands on
// the helper's stdin and events on its stdout.
//
// This one file is compiled into both assemblies (the helper links it in), so the two sides
// cannot drift apart. Keep it free of anything either side lacks: no Avalonia, no WinRT, no
// references to other client types.

/// <summary>A command from the client to the helper.</summary>
internal sealed record ReaderCommand
{
    /// <summary><c>start</c>, <c>snapshot</c> or <c>stop</c>.</summary>
    [JsonPropertyName("cmd")]
    public string Cmd { get; init; } = string.Empty;

    /// <summary>With <c>start</c>: how often an unchanged window is looked at again.</summary>
    [JsonPropertyName("interval_ms")]
    public int? IntervalMs { get; init; }

    public const string Start = "start";
    public const string Snapshot = "snapshot";
    public const string Stop = "stop";
}

/// <summary>A rectangle in physical pixels.</summary>
internal sealed record ReaderRect
{
    [JsonPropertyName("x")]
    public int X { get; init; }

    [JsonPropertyName("y")]
    public int Y { get; init; }

    [JsonPropertyName("w")]
    public int W { get; init; }

    [JsonPropertyName("h")]
    public int H { get; init; }

    [JsonIgnore]
    public int Right => X + W;

    [JsonIgnore]
    public int Bottom => Y + H;
}

/// <summary>One line of recognised text, in the captured window's own pixels.</summary>
internal sealed record ReaderLine
{
    [JsonPropertyName("text")]
    public string Text { get; init; } = string.Empty;

    [JsonPropertyName("x")]
    public int X { get; init; }

    [JsonPropertyName("y")]
    public int Y { get; init; }

    [JsonPropertyName("w")]
    public int W { get; init; }

    [JsonPropertyName("h")]
    public int H { get; init; }

    /// <summary>
    /// The colour behind the text, sampled in the bubble's padding on several points and
    /// taking the most common — a single point lands on a glyph often enough to matter.
    /// </summary>
    [JsonPropertyName("bg")]
    public int[] Bg { get; init; } = [];
}

/// <summary>An event from the helper to the client.</summary>
/// <remarks>
/// One flat shape with a <see cref="Type"/> tag rather than a hierarchy: a line of JSON per
/// event is read by a source-generated context on both sides, and a discriminated hierarchy
/// would need polymorphism metadata on each for no gain at this size.
/// </remarks>
internal sealed record ReaderEvent
{
    public const string Ready = "ready";
    public const string Window = "window";
    public const string Scrolling = "scrolling";
    public const string Frame = "frame";
    public const string Alive = "alive";
    public const string Error = "error";

    public const string StateForeground = "foreground";
    public const string StateBackground = "background";
    public const string StateMinimized = "minimized";
    public const string StateGone = "gone";

    [JsonPropertyName("type")]
    public string Type { get; init; } = string.Empty;

    // window

    /// <summary><c>foreground</c>, <c>background</c>, <c>minimized</c> or <c>gone</c>.</summary>
    [JsonPropertyName("state")]
    public string? State { get; init; }

    /// <summary>The window on screen, physical pixels.</summary>
    [JsonPropertyName("rect")]
    public ReaderRect? Rect { get; init; }

    /// <summary>
    /// Screen units per design pixel: the window's DPI / 96 on Windows (screen units are physical
    /// pixels), 1 on macOS (screen units are points).
    /// </summary>
    [JsonPropertyName("scale")]
    public double? Scale { get; init; }

    /// <summary>
    /// Captured-image pixels per screen unit: 1 on Windows, the backing scale (2 on Retina) on
    /// macOS. Frame coordinates are image pixels; <see cref="Rect"/> is screen units. Absent
    /// means 1.
    /// </summary>
    [JsonPropertyName("image_scale")]
    public double? ImageScale { get; init; }

    /// <summary>
    /// The process whose window is in front. When it is the client's own (a card was clicked,
    /// which on macOS activates the app), the cards stay up.
    /// </summary>
    [JsonPropertyName("front_pid")]
    public int? FrontPid { get; init; }

    // frame

    [JsonPropertyName("seq")]
    public long? Seq { get; init; }

    /// <summary>Of the message area only, so the caret in the input box does not count as a change.</summary>
    [JsonPropertyName("hash")]
    public string? Hash { get; init; }

    /// <summary>Size of the captured image the coordinates below refer to.</summary>
    [JsonPropertyName("size")]
    public ReaderRect? Size { get; init; }

    /// <summary>The message list: between the chat header and the input box, right of the conversation list.</summary>
    [JsonPropertyName("area")]
    public ReaderRect? Area { get; init; }

    /// <summary>The message list's background colour, sampled the same way as <see cref="ReaderLine.Bg"/>.</summary>
    [JsonPropertyName("bg")]
    public int[]? Background { get; init; }

    /// <summary>The open conversation's title, as recognised from the chat header. Empty when none is open.</summary>
    [JsonPropertyName("title")]
    public string? Title { get; init; }

    /// <summary>Recognised lines inside <see cref="Area"/>, top to bottom.</summary>
    [JsonPropertyName("lines")]
    public ReaderLine[]? Lines { get; init; }

    // error

    [JsonPropertyName("code")]
    public string? Code { get; init; }

    [JsonPropertyName("message")]
    public string? Message { get; init; }

    public const string ErrorNoWindow = "no_window";
    public const string ErrorCaptureFailed = "capture_failed";
    public const string ErrorOcrLanguageMissing = "ocr_language_missing";
    public const string ErrorOsUnsupported = "os_unsupported";
    public const string ErrorNoChatArea = "no_chat_area";

    /// <summary>macOS: 「屏幕录制」 is not allowed for the client yet.</summary>
    public const string ErrorScreenRecordingDenied = "screen_recording_denied";
}

[JsonSourceGenerationOptions(DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull)]
[JsonSerializable(typeof(ReaderCommand))]
[JsonSerializable(typeof(ReaderEvent))]
internal sealed partial class ReaderJsonContext : JsonSerializerContext;
