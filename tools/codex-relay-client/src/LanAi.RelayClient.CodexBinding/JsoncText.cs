using System.Text;
using System.Text.Json;

namespace LanAi.RelayClient.CodexBinding;

/// <summary>
/// Reads and edits the top level of a JSON-with-comments document in place, without
/// rewriting the rest of it.
/// </summary>
/// <remarks>
/// <para>
/// Exists because VS Code's <c>settings.json</c> is JSONC: comments and trailing commas are
/// ordinary there. Parsing it and writing it back — the only thing a JSON library offers —
/// would drop every comment and reformat the file, which is a lot to do to something the user
/// keeps by hand. This scans for structure instead and edits the text: insert one line,
/// replace one value, remove one line.
/// </para>
/// <para>
/// It looks only at the outermost object. It understands strings (with escapes), both comment
/// forms and nesting, so a key-shaped string inside a value or a comment is not mistaken for a
/// key. Anything it cannot parse as one object and nothing else makes <see cref="Scan"/>
/// return null, and callers treat that as "leave the file alone".
/// </para>
/// </remarks>
internal static class JsoncText
{
    /// <param name="ValueEnd">Exclusive. Excludes trailing whitespace and comments.</param>
    /// <param name="CommaIndex">The comma after this property, or -1 for the last one without one.</param>
    internal sealed record Property(string Name, int KeyStart, int ValueStart, int ValueEnd, int CommaIndex);

    internal sealed record Document(int OpenBrace, int CloseBrace, IReadOnlyList<Property> Properties);

    /// <summary>The result of an edit, with what it displaced.</summary>
    /// <param name="Text">The document after the edit.</param>
    /// <param name="Inserted">True when the key did not exist and a line was added.</param>
    /// <param name="PreviousValue">The value text that was replaced, when the key existed.</param>
    internal sealed record Edit(string Text, bool Inserted, string? PreviousValue);

    /// <summary>Finds the top-level properties, or returns null if this is not exactly one object.</summary>
    internal static Document? Scan(string text)
    {
        int i = 0;
        if (!SkipTrivia(text, ref i) || i >= text.Length || text[i] != '{')
        {
            return null;
        }

        int open = i++;
        var properties = new List<Property>();
        int close;
        while (true)
        {
            if (!SkipTrivia(text, ref i) || i >= text.Length)
            {
                return null;
            }

            if (text[i] == '}')
            {
                close = i++;
                break;
            }

            if (text[i] != '"')
            {
                return null;
            }

            int keyStart = i;
            if (!SkipString(text, ref i))
            {
                return null;
            }

            string? name = DecodeString(text, keyStart, i);
            if (name is null || !SkipTrivia(text, ref i) || i >= text.Length || text[i] != ':')
            {
                return null;
            }

            i++;
            if (!SkipTrivia(text, ref i))
            {
                return null;
            }

            int valueStart = i;
            if (!SkipValue(text, ref i) || i == valueStart)
            {
                return null;
            }

            int valueEnd = i;
            if (!SkipTrivia(text, ref i) || i >= text.Length)
            {
                return null;
            }

            int comma = -1;
            if (text[i] == ',')
            {
                comma = i++;
            }
            else if (text[i] != '}')
            {
                return null;
            }

            properties.Add(new Property(name, keyStart, valueStart, valueEnd, comma));
        }

        // Nothing but whitespace and comments after the object. A file with a second value
        // in it is not one this understands well enough to edit.
        return SkipTrivia(text, ref i) && i == text.Length ? new Document(open, close, properties) : null;
    }

    /// <summary>The last top-level property with this name — the one VS Code honours.</summary>
    internal static Property? Find(Document document, string name) =>
        document.Properties.LastOrDefault(p => string.Equals(p.Name, name, StringComparison.Ordinal));

    /// <summary>
    /// Sets a top-level property to <paramref name="valueText"/>, replacing its value or adding a line.
    /// </summary>
    internal static Edit? Set(string text, string name, string valueText)
    {
        Document? document = Scan(text);
        if (document is null)
        {
            return null;
        }

        Property? existing = Find(document, name);
        if (existing is not null)
        {
            string previous = text[existing.ValueStart..existing.ValueEnd];
            string replaced = string.Concat(text.AsSpan(0, existing.ValueStart), valueText, text.AsSpan(existing.ValueEnd));
            return new Edit(replaced, Inserted: false, previous);
        }

        return new Edit(Insert(text, document, name, valueText), Inserted: true, PreviousValue: null);
    }

    /// <summary>
    /// Undoes <see cref="Set"/> on a document that has since been edited, changing as little as it can.
    /// </summary>
    /// <remarks>
    /// Only used when the file no longer matches what was written, which is when an exact
    /// restore is impossible. It removes the line that was added, or puts the old value back,
    /// and leaves anything the user did in the meantime alone. Removing the last property can
    /// leave the previous one with a trailing comma; that is valid JSONC and VS Code reads it.
    /// </remarks>
    internal static string? Unset(string text, string name, string expectedValue, string? previousValue)
    {
        Document? document = Scan(text);
        Property? existing = document is null ? null : Find(document, name);
        if (document is null || existing is null)
        {
            return null;
        }

        // Not what was written: the user changed it, so it is theirs now.
        if (!string.Equals(text[existing.ValueStart..existing.ValueEnd], expectedValue, StringComparison.Ordinal))
        {
            return null;
        }

        if (previousValue is not null)
        {
            return string.Concat(text.AsSpan(0, existing.ValueStart), previousValue, text.AsSpan(existing.ValueEnd));
        }

        return Remove(text, existing);
    }

    private static string Insert(string text, Document document, string name, string valueText)
    {
        string newline = text.Contains("\r\n", StringComparison.Ordinal) ? "\r\n" : "\n";
        string indent = DetectIndent(text, document);
        string entry = $"\"{EscapeKey(name)}\": {valueText}";

        if (document.Properties.Count == 0)
        {
            // An empty object, possibly with comments in it. Whatever is there stays.
            return InsertBeforeClose(text, document.CloseBrace, indent + entry, trailingComma: false, newline);
        }

        Property last = document.Properties[^1];
        bool trailingCommaStyle = last.CommaIndex != -1;

        // Placed before the closing brace, and the comma that the previous last property now
        // needs goes right after its value — before any comment on that line, so the comment
        // still sits next to the property it was written about.
        string inserted = InsertBeforeClose(text, document.CloseBrace, indent + entry, trailingCommaStyle, newline);
        return trailingCommaStyle
            ? inserted
            : string.Concat(inserted.AsSpan(0, last.ValueEnd), ",", inserted.AsSpan(last.ValueEnd));
    }

    private static string InsertBeforeClose(string text, int closeBrace, string entryLine, bool trailingComma, string newline)
    {
        string entry = entryLine + (trailingComma ? "," : string.Empty);
        int lineStart = text.LastIndexOfAny(['\n', '\r'], Math.Max(0, closeBrace - 1)) + 1;
        bool closeStartsItsLine = closeBrace == 0 || text[lineStart..closeBrace].All(c => c is ' ' or '\t');

        if (closeStartsItsLine && closeBrace > 0)
        {
            return string.Concat(text.AsSpan(0, lineStart), entry, newline, text.AsSpan(lineStart));
        }

        if (text[..closeBrace].TrimEnd().EndsWith('{'))
        {
            // {} on one line: give the entry a line of its own, without leaving the space that
            // was between the braces dangling at the end of the first one.
            return text[..closeBrace].TrimEnd() + newline + entry + newline + text[closeBrace..];
        }

        // A single-line object such as { "a": 1 }: stays on one line.
        return string.Concat(text.AsSpan(0, closeBrace), entry.TrimStart(), " ", text.AsSpan(closeBrace));
    }

    private static string DetectIndent(string text, Document document)
    {
        if (document.Properties.Count == 0)
        {
            return "    ";
        }

        int keyStart = document.Properties[0].KeyStart;
        int lineStart = text.LastIndexOfAny(['\n', '\r'], Math.Max(0, keyStart - 1)) + 1;
        if (keyStart == 0 || lineStart > keyStart)
        {
            return "    ";
        }

        string between = text[lineStart..keyStart];
        return between.Length > 0 && between.All(c => c is ' ' or '\t') ? between : "    ";
    }

    private static string Remove(string text, Property property)
    {
        int lineStart = text.LastIndexOfAny(['\n', '\r'], Math.Max(0, property.KeyStart - 1)) + 1;
        bool startsItsLine = text[lineStart..property.KeyStart].All(c => c is ' ' or '\t');

        int end = property.CommaIndex != -1 ? property.CommaIndex + 1 : property.ValueEnd;
        int lineEnd = end;
        while (lineEnd < text.Length && text[lineEnd] is ' ' or '\t')
        {
            lineEnd++;
        }

        bool endsItsLine = lineEnd >= text.Length || text[lineEnd] is '\n' or '\r';
        if (startsItsLine && endsItsLine)
        {
            // The whole line, including its newline.
            int after = lineEnd;
            if (after < text.Length && text[after] == '\r')
            {
                after++;
            }

            if (after < text.Length && text[after] == '\n')
            {
                after++;
            }

            return string.Concat(text.AsSpan(0, lineStart), text.AsSpan(after));
        }

        // Shares its line with something else: take the property and one following space.
        int removeEnd = end;
        if (removeEnd < text.Length && text[removeEnd] == ' ')
        {
            removeEnd++;
        }

        return string.Concat(text.AsSpan(0, property.KeyStart), text.AsSpan(removeEnd));
    }

    // ---- Scanning primitives ------------------------------------------------------------

    /// <summary>Skips whitespace and comments. False only for a comment that never ends.</summary>
    private static bool SkipTrivia(string text, ref int i)
    {
        while (i < text.Length)
        {
            char c = text[i];
            if (char.IsWhiteSpace(c) || c == '\xFEFF')
            {
                i++;
            }
            else if (c == '/' && i + 1 < text.Length && text[i + 1] == '/')
            {
                while (i < text.Length && text[i] is not ('\n' or '\r'))
                {
                    i++;
                }
            }
            else if (c == '/' && i + 1 < text.Length && text[i + 1] == '*')
            {
                int end = text.IndexOf("*/", i + 2, StringComparison.Ordinal);
                if (end < 0)
                {
                    return false;
                }

                i = end + 2;
            }
            else
            {
                return true;
            }
        }

        return true;
    }

    /// <summary>Advances past a string starting at the opening quote. False if it never closes.</summary>
    private static bool SkipString(string text, ref int i)
    {
        i++;
        while (i < text.Length)
        {
            char c = text[i++];
            if (c == '\\')
            {
                i++;
            }
            else if (c == '"')
            {
                return true;
            }
            else if (c is '\n' or '\r')
            {
                return false;
            }
        }

        return false;
    }

    private static bool SkipValue(string text, ref int i)
    {
        if (i >= text.Length)
        {
            return false;
        }

        char c = text[i];
        if (c == '"')
        {
            return SkipString(text, ref i);
        }

        if (c is '{' or '[')
        {
            int depth = 0;
            while (i < text.Length)
            {
                char d = text[i];
                if (d == '"')
                {
                    if (!SkipString(text, ref i))
                    {
                        return false;
                    }

                    continue;
                }

                if (d == '/' && i + 1 < text.Length && text[i + 1] is '/' or '*')
                {
                    if (!SkipTrivia(text, ref i))
                    {
                        return false;
                    }

                    continue;
                }

                i++;
                if (d is '{' or '[')
                {
                    depth++;
                }
                else if (d is '}' or ']')
                {
                    depth--;
                    if (depth == 0)
                    {
                        return true;
                    }
                }
            }

            return false;
        }

        while (i < text.Length && text[i] is not (',' or '}' or ']' or ' ' or '\t' or '\r' or '\n' or '/'))
        {
            i++;
        }

        return true;
    }

    /// <remarks>Read with JsonDocument, which is trim-safe; this assembly is trimmed.</remarks>
    private static string? DecodeString(string text, int start, int endExclusive)
    {
        try
        {
            using JsonDocument document = JsonDocument.Parse(text.AsMemory(start, endExclusive - start));
            return document.RootElement.GetString();
        }
        catch (JsonException)
        {
            return null;
        }
    }

    private static string EscapeKey(string name)
    {
        var builder = new StringBuilder(name.Length);
        foreach (char c in name)
        {
            builder.Append(c switch
            {
                '"' => "\\\"",
                '\\' => "\\\\",
                _ => c.ToString(),
            });
        }

        return builder.ToString();
    }
}
