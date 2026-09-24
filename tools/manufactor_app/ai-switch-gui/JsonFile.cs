using System.Text;
using System.Text.Json;

namespace AiSwitchGui;

internal static class JsonFile
{
    private static readonly JsonSerializerOptions SerializerOptions = new()
    {
        WriteIndented = true
    };

    public static T ReadOrDefault<T>(string path, Func<T> fallbackFactory) where T : class
    {
        if (!File.Exists(path))
        {
            return fallbackFactory();
        }

        var text = ReadText(path);
        return JsonSerializer.Deserialize<T>(text, SerializerOptions) ?? fallbackFactory();
    }

    public static void Write<T>(string path, T value)
    {
        var json = JsonSerializer.Serialize(value, SerializerOptions);
        WriteText(path, json);
    }

    public static string ReadText(string path)
    {
        return File.ReadAllText(path, Encoding.UTF8).TrimStart('\uFEFF');
    }

    public static void WriteText(string path, string text)
    {
        var directory = Path.GetDirectoryName(path);
        if (!string.IsNullOrWhiteSpace(directory))
        {
            Directory.CreateDirectory(directory);
        }

        var encoding = new UTF8Encoding(encoderShouldEmitUTF8Identifier: false);
        File.WriteAllText(path, text, encoding);
    }
}
