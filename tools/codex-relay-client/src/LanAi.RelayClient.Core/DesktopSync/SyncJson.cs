using System.Buffers;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using LanAi.RelayClient.CodexBinding.DesktopSync;

namespace LanAi.RelayClient.DesktopSync;

/// <summary>
/// The JSON the phone receives. Written by hand with <see cref="Utf8JsonWriter"/>: this
/// project is trimmed, and a reflection serializer would silently drop members.
/// Names are snake_case like the rest of the server's API.
/// </summary>
internal static class SyncJson
{
    public static byte[] Write(Action<Utf8JsonWriter> body)
    {
        var buffer = new ArrayBufferWriter<byte>();
        using (var writer = new Utf8JsonWriter(buffer))
        {
            writer.WriteStartObject();
            body(writer);
            writer.WriteEndObject();
        }

        return buffer.WrittenSpan.ToArray();
    }

    public static byte[] Ok(Action<Utf8JsonWriter>? body = null) => Write(w =>
    {
        w.WriteBoolean("ok", true);
        body?.Invoke(w);
    });

    public static byte[] Error(string code, string message) => Write(w =>
    {
        w.WriteBoolean("ok", false);
        w.WriteString("error", code);
        w.WriteString("message", message);
    });

    /// <summary>
    /// The cursor a phone hands back: the offset plus a short hash of the rollout path,
    /// so a cursor for a different file is recognised rather than read at a wrong offset.
    /// The path itself stays on this computer.
    /// </summary>
    public static string EncodeCursor(SyncCursor cursor) =>
        $"{cursor.Offset}.{PathTag(cursor.RolloutPath)}";

    public static bool TryDecodeCursor(string? token, string currentPath, out SyncCursor cursor)
    {
        cursor = new SyncCursor(currentPath, 0);
        if (string.IsNullOrEmpty(token))
        {
            return false;
        }

        int dot = token.IndexOf('.');
        if (dot <= 0 || !long.TryParse(token.AsSpan(0, dot), out long offset) || offset < 0)
        {
            return false;
        }

        // A cursor for another file is kept as such, so ReadSince answers "resync".
        string path = token[(dot + 1)..] == PathTag(currentPath) ? currentPath : "<other>";
        cursor = new SyncCursor(path, offset);
        return true;
    }

    private static string PathTag(string path) =>
        Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(path.ToLowerInvariant())))[..8].ToLowerInvariant();

    public static void WriteHeader(Utf8JsonWriter w, SessionHeader header)
    {
        w.WriteStartObject("session");
        w.WriteString("thread_id", header.ThreadId);
        WriteNullable(w, "title", header.Title);
        WriteNullable(w, "cwd", header.Cwd);
        WriteNullable(w, "model", header.Model);
        w.WriteString("permission", PermissionName(header.Permission));
        WriteNullable(w, "open_turn_id", header.OpenTurnId);
        w.WriteEndObject();
    }

    public static void WritePage(Utf8JsonWriter w, SessionPage page, Func<SyncItem, bool>? fromPhone = null)
    {
        WriteItems(w, "items", page.Items, fromPhone);
        w.WriteBoolean("has_older", page.HasOlder);
        WriteNullable(w, "truncated_turn_id", page.TruncatedTurnId);
    }

    public static void WriteItems(Utf8JsonWriter w, string name, IEnumerable<SyncItem> items, Func<SyncItem, bool>? fromPhone = null)
    {
        w.WriteStartArray(name);
        foreach (SyncItem item in items)
        {
            WriteItem(w, item, fromPhone?.Invoke(item) == true);
        }

        w.WriteEndArray();
    }

    public static void WriteItem(Utf8JsonWriter w, SyncItem item, bool fromPhone)
    {
        w.WriteStartObject();
        w.WriteNumber("seq", item.Seq);
        WriteNullable(w, "turn_id", item.TurnId);
        w.WriteString("item_id", item.ItemId);
        w.WriteString("kind", KindName(item.Kind));
        WriteNullable(w, "text", item.Text);
        if (item.Origin is UserMessageOrigin origin)
        {
            w.WriteString("origin", fromPhone ? "phone" : origin == UserMessageOrigin.Desktop ? "desktop" : "delegated");
        }

        if (item.ImageCount > 0)
        {
            w.WriteNumber("image_count", item.ImageCount);
        }

        WriteNullable(w, "command", item.Command);
        if (item.ExitCode is int exit)
        {
            w.WriteNumber("exit_code", exit);
        }

        WriteNullable(w, "status", item.Status);
        if (item.DurationMs is long duration)
        {
            w.WriteNumber("duration_ms", duration);
        }

        WriteNullable(w, "output_preview", item.OutputPreview);
        if (item.OutputTruncated)
        {
            w.WriteBoolean("output_truncated", true);
        }

        if (item.Files is { Count: > 0 } files)
        {
            w.WriteStartArray("files");
            foreach (SyncFileChange file in files)
            {
                w.WriteStartObject();
                w.WriteString("path", file.Path);
                w.WriteString("change", file.Change);
                w.WriteNumber("added", file.Added);
                w.WriteNumber("removed", file.Removed);
                w.WriteEndObject();
            }

            w.WriteEndArray();
        }

        if (item.Outcome is TurnOutcome outcome)
        {
            w.WriteString("outcome", outcome switch
            {
                TurnOutcome.Completed => "completed",
                TurnOutcome.Failed => "failed",
                _ => "aborted",
            });
        }

        w.WriteEndObject();
    }

    public static string PermissionName(PermissionMode mode) => mode switch
    {
        PermissionMode.FullAccess => "full_access",
        PermissionMode.Auto => "auto",
        PermissionMode.SandboxedNoApproval => "sandboxed",

        // Shown to the phone as full access: the one wrong guess that matters is
        // calling a full-access conversation safe (see PermissionModes).
        _ => "full_access",
    };

    private static string KindName(SyncItemKind kind) => kind switch
    {
        SyncItemKind.User => "user",
        SyncItemKind.Progress => "progress",
        SyncItemKind.Reply => "reply",
        SyncItemKind.Thinking => "thinking",
        SyncItemKind.Command => "command",
        SyncItemKind.FileChange => "file_change",
        SyncItemKind.Tool => "tool",
        SyncItemKind.Image => "image",
        SyncItemKind.Running => "running",
        SyncItemKind.Notice => "notice",
        SyncItemKind.TurnStarted => "turn_started",
        SyncItemKind.TurnEnded => "turn_ended",
        _ => "unknown",
    };

    public static void WriteNullable(Utf8JsonWriter w, string name, string? value)
    {
        if (value is null)
        {
            w.WriteNull(name);
        }
        else
        {
            w.WriteString(name, value);
        }
    }
}
