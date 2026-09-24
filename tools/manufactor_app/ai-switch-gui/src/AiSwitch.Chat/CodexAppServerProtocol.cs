using System.Globalization;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace LanAi.Workspace.Chat;

internal enum CodexProtocolMessageKind
{
    Response,
    ErrorResponse,
    ServerRequest,
    Notification,
}

internal sealed record CodexProtocolMessage(
    CodexProtocolMessageKind Kind,
    JsonElement? Id,
    string? Method,
    JsonElement? Params,
    JsonElement? Result,
    long? ErrorCode,
    string? ErrorMessage,
    JsonElement Root);

internal static class CodexAppServerProtocol
{
    private static readonly JsonSerializerOptions SerializerOptions = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    };

    public static bool TryParse(
        string line,
        out CodexProtocolMessage? message,
        out string? error)
    {
        message = null;
        error = null;

        if (string.IsNullOrWhiteSpace(line))
        {
            error = "app-server 返回了空消息。";
            return false;
        }

        try
        {
            using var document = JsonDocument.Parse(line);
            JsonElement root = document.RootElement.Clone();
            if (root.ValueKind != JsonValueKind.Object)
            {
                error = "app-server 消息不是 JSON 对象。";
                return false;
            }

            JsonElement? id = root.TryGetProperty("id", out JsonElement idElement)
                ? idElement.Clone()
                : null;
            string? method = root.TryGetProperty("method", out JsonElement methodElement) &&
                methodElement.ValueKind == JsonValueKind.String
                    ? methodElement.GetString()
                    : null;
            JsonElement? parameters = root.TryGetProperty("params", out JsonElement paramsElement)
                ? paramsElement.Clone()
                : null;

            if (id is not null && root.TryGetProperty("error", out JsonElement errorElement))
            {
                long? code = errorElement.ValueKind == JsonValueKind.Object &&
                    errorElement.TryGetProperty("code", out JsonElement codeElement) &&
                    codeElement.TryGetInt64(out long parsedCode)
                        ? parsedCode
                        : null;
                string? errorMessage = errorElement.ValueKind == JsonValueKind.Object &&
                    errorElement.TryGetProperty("message", out JsonElement errorMessageElement) &&
                    errorMessageElement.ValueKind == JsonValueKind.String
                        ? errorMessageElement.GetString()
                        : errorElement.GetRawText();

                message = new CodexProtocolMessage(
                    CodexProtocolMessageKind.ErrorResponse,
                    id,
                    method,
                    parameters,
                    null,
                    code,
                    errorMessage,
                    root);
                return true;
            }

            if (id is not null && root.TryGetProperty("result", out JsonElement resultElement))
            {
                message = new CodexProtocolMessage(
                    CodexProtocolMessageKind.Response,
                    id,
                    method,
                    parameters,
                    resultElement.Clone(),
                    null,
                    null,
                    root);
                return true;
            }

            if (id is not null && method is not null)
            {
                message = new CodexProtocolMessage(
                    CodexProtocolMessageKind.ServerRequest,
                    id,
                    method,
                    parameters,
                    null,
                    null,
                    null,
                    root);
                return true;
            }

            if (id is null && method is not null)
            {
                message = new CodexProtocolMessage(
                    CodexProtocolMessageKind.Notification,
                    null,
                    method,
                    parameters,
                    null,
                    null,
                    null,
                    root);
                return true;
            }

            error = "app-server 消息既不是响应、请求，也不是通知。";
            return false;
        }
        catch (JsonException exception)
        {
            error = $"无法解析 app-server JSON：{exception.Message}";
            return false;
        }
    }

    public static string SerializeRequest(string id, string method, object? parameters) =>
        JsonSerializer.Serialize(
            new Dictionary<string, object?>
            {
                ["id"] = id,
                ["method"] = method,
                ["params"] = parameters,
            },
            SerializerOptions);

    public static string SerializeResponse(JsonElement id, object? result) =>
        JsonSerializer.Serialize(
            new Dictionary<string, object?>
            {
                ["id"] = id,
                ["result"] = result,
            },
            SerializerOptions);

    public static string SerializeErrorResponse(
        JsonElement id,
        long code,
        string message) =>
        JsonSerializer.Serialize(
            new Dictionary<string, object?>
            {
                ["id"] = id,
                ["error"] = new Dictionary<string, object?>
                {
                    ["code"] = code,
                    ["message"] = message,
                },
            },
            SerializerOptions);

    public static string NormalizeId(JsonElement id) => id.ValueKind switch
    {
        JsonValueKind.String => id.GetString() ?? string.Empty,
        JsonValueKind.Number when id.TryGetInt64(out long value) =>
            value.ToString(CultureInfo.InvariantCulture),
        _ => id.GetRawText(),
    };
}

internal sealed class CodexAppServerException : Exception
{
    public CodexAppServerException(string method, long? code, string message)
        : base(code is null
            ? $"Codex app-server 请求 {method} 失败：{message}"
            : $"Codex app-server 请求 {method} 失败（{code.Value}）：{message}")
    {
        Method = method;
        Code = code;
    }

    public string Method { get; }

    public long? Code { get; }
}
