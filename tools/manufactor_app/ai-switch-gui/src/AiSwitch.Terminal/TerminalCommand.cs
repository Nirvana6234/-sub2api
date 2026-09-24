namespace LanAi.Workspace.Terminal;

public sealed record TerminalCommand(
    string FileName,
    IReadOnlyList<string> Arguments,
    string WorkingDirectory,
    IReadOnlyDictionary<string, string?>? Environment = null,
    string? DisplayName = null)
{
    public string BuildCommandLine()
    {
        var parts = new List<string>(Arguments.Count + 1)
        {
            CommandLineEscaper.Quote(FileName)
        };

        parts.AddRange(Arguments.Select(CommandLineEscaper.Quote));
        return string.Join(' ', parts);
    }
}

internal static class CommandLineEscaper
{
    public static string Quote(string value)
    {
        if (string.IsNullOrEmpty(value))
        {
            return "\"\"";
        }

        if (!value.Any(char.IsWhiteSpace) && !value.Contains('"'))
        {
            return value;
        }

        var result = new System.Text.StringBuilder(value.Length + 2);
        result.Append('"');
        var backslashes = 0;

        foreach (var character in value)
        {
            if (character == '\\')
            {
                backslashes++;
                continue;
            }

            if (character == '"')
            {
                result.Append('\\', backslashes * 2 + 1);
                result.Append('"');
                backslashes = 0;
                continue;
            }

            result.Append('\\', backslashes);
            backslashes = 0;
            result.Append(character);
        }

        result.Append('\\', backslashes * 2);
        result.Append('"');
        return result.ToString();
    }
}

