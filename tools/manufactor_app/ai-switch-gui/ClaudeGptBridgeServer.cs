using System.Collections.Concurrent;
using System.Net;
using System.Net.Http.Headers;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace AiSwitchGui;

/// <summary>
/// Local Anthropic Messages -> OpenAI Responses bridge for Claude Code.
///
/// Claude Code only knows Anthropic's /v1/messages protocol. GPT/Grok style
/// upstreams normally expose OpenAI's /v1/responses protocol. Pointing Claude
/// Code directly at those upstreams is therefore structurally wrong; this
/// loopback bridge owns the protocol conversion and keeps the upstream secret
/// inside the APP process.
/// </summary>
internal sealed class ClaudeGptBridgeServer : IDisposable
{
    private const int MaxHeaderBytes = 64 * 1024;
    private const int MaxBodyBytes = 24 * 1024 * 1024;
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        WriteIndented = false,
    };

    private readonly TcpListener _listener;
    private readonly HttpClient _httpClient;
    private readonly BridgeConfiguration _configuration;
    private readonly CancellationTokenSource _shutdown = new();
    private readonly ConcurrentDictionary<Task, byte> _connections = new();
    private Task? _acceptLoop;
    private bool _disposed;

    private ClaudeGptBridgeServer(
        TcpListener listener,
        HttpClient httpClient,
        BridgeConfiguration configuration)
    {
        _listener = listener;
        _httpClient = httpClient;
        _configuration = configuration;
        Port = ((IPEndPoint)_listener.LocalEndpoint).Port;
        BaseUrl = $"http://127.0.0.1:{Port}";
        AuthToken = "lanai-bridge-" + Convert.ToHexString(RandomNumberGenerator.GetBytes(18)).ToLowerInvariant();
    }

    public int Port { get; }

    public string BaseUrl { get; }

    public string AuthToken { get; }

    public bool IsRunning => !_disposed;

    public static ClaudeGptBridgeServer Start(
        ClientProfile upstream,
        ClaudeGptModelMapping mapping,
        HttpClient httpClient)
    {
        ArgumentNullException.ThrowIfNull(upstream);
        ArgumentNullException.ThrowIfNull(mapping);
        ArgumentNullException.ThrowIfNull(httpClient);

        var configuration = new BridgeConfiguration(
            SwitchService.NormalizeOpenAiApiBaseUrl(upstream.BaseUrl),
            upstream.Secret.Trim(),
            mapping.OpusModel.Trim(),
            mapping.SonnetModel.Trim(),
            mapping.HaikuModel.Trim());

        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        var server = new ClaudeGptBridgeServer(listener, httpClient, configuration);
        server.StartAcceptLoop();
        return server;
    }

    private void StartAcceptLoop()
    {
        _acceptLoop = Task.Run(AcceptLoopAsync);
    }

    private async Task AcceptLoopAsync()
    {
        while (!_shutdown.IsCancellationRequested)
        {
            TcpClient? client = null;
            try
            {
                client = await _listener.AcceptTcpClientAsync(_shutdown.Token).ConfigureAwait(false);
                Task task = HandleClientAndDisposeAsync(client, _shutdown.Token);
                _connections.TryAdd(task, 0);
                _ = task.ContinueWith(
                    completed => _connections.TryRemove(completed, out _),
                    CancellationToken.None,
                    TaskContinuationOptions.ExecuteSynchronously,
                    TaskScheduler.Default);
            }
            catch (OperationCanceledException) when (_shutdown.IsCancellationRequested)
            {
                client?.Dispose();
                break;
            }
            catch (SocketException) when (_shutdown.IsCancellationRequested)
            {
                client?.Dispose();
                break;
            }
            catch (ObjectDisposedException)
            {
                client?.Dispose();
                break;
            }
            catch
            {
                client?.Dispose();
            }
        }
    }

    private async Task HandleClientAndDisposeAsync(TcpClient client, CancellationToken cancellationToken)
    {
        using (client)
        {
            client.NoDelay = true;
            using NetworkStream stream = client.GetStream();
            try
            {
                HttpRequestData request = await ReadRequestAsync(stream, cancellationToken).ConfigureAwait(false);
                HttpResponseData response = await DispatchAsync(request, cancellationToken).ConfigureAwait(false);
                await WriteResponseAsync(stream, response, cancellationToken).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
            }
            catch (Exception exception)
            {
                HttpResponseData response = JsonResponse(
                    500,
                    new JsonObject
                    {
                        ["type"] = "error",
                        ["error"] = new JsonObject
                        {
                            ["type"] = "bridge_error",
                            ["message"] = Limit(exception.Message),
                        },
                    });
                await WriteResponseAsync(stream, response, CancellationToken.None).ConfigureAwait(false);
            }
        }
    }

    private async Task<HttpResponseData> DispatchAsync(
        HttpRequestData request,
        CancellationToken cancellationToken)
    {
        string path = request.Path.Split('?', 2)[0].TrimEnd('/');
        if (string.Equals(request.Method, "GET", StringComparison.OrdinalIgnoreCase) &&
            (string.Equals(path, "/health", StringComparison.OrdinalIgnoreCase) ||
             string.Equals(path, "/v1/models", StringComparison.OrdinalIgnoreCase)))
        {
            return path.Equals("/health", StringComparison.OrdinalIgnoreCase)
                ? JsonResponse(200, new JsonObject { ["status"] = "ok", ["mode"] = "anthropic-to-responses" })
                : JsonResponse(200, BuildModelList());
        }

        if (!IsAuthorized(request))
        {
            return JsonResponse(
                401,
                new JsonObject
                {
                    ["type"] = "error",
                    ["error"] = new JsonObject
                    {
                        ["type"] = "authentication_error",
                        ["message"] = "本地 Claude Code 协议桥认证失败。",
                    },
                });
        }

        if (!string.Equals(request.Method, "POST", StringComparison.OrdinalIgnoreCase))
        {
            return JsonResponse(405, ErrorBody("method_not_allowed", "当前接口只支持 POST。"));
        }

        if (string.Equals(path, "/v1/messages/count_tokens", StringComparison.OrdinalIgnoreCase))
        {
            using JsonDocument document = JsonDocument.Parse(request.Body);
            int estimated = EstimateTokens(document.RootElement);
            return JsonResponse(200, new JsonObject { ["input_tokens"] = Math.Max(1, estimated) });
        }

        if (!string.Equals(path, "/v1/messages", StringComparison.OrdinalIgnoreCase))
        {
            return JsonResponse(404, ErrorBody("not_found", "本地桥只提供 Claude Code 需要的 /v1/messages 接口。"));
        }

        using JsonDocument sourceDocument = JsonDocument.Parse(request.Body);
        bool stream = sourceDocument.RootElement.TryGetProperty("stream", out JsonElement streamElement) &&
                      streamElement.ValueKind == JsonValueKind.True;
        BridgeRequest bridgeRequest = BuildResponsesRequest(sourceDocument.RootElement, stream);
        if (stream)
        {
            HttpResponseData streamResponse = await CreateResponsesStreamResponseAsync(bridgeRequest.Payload, bridgeRequest.Model, cancellationToken)
                .ConfigureAwait(false);
            return streamResponse;
        }

        BridgeResponse upstream = await SendResponsesRequestAsync(bridgeRequest.Payload, cancellationToken)
            .ConfigureAwait(false);
        if (!upstream.Success)
        {
            return JsonResponse(upstream.StatusCode, ErrorBody("upstream_error", upstream.ErrorMessage));
        }

        JsonObject anthropic = BuildAnthropicMessageResponse(upstream.Root, bridgeRequest.Model);
        return JsonResponse(200, anthropic);
    }

    private async Task<BridgeResponse> SendResponsesRequestAsync(
        JsonObject payload,
        CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, $"{_configuration.UpstreamBaseUrl}/responses");
        AddUpstreamAuthorization(request);
        request.Content = new StringContent(payload.ToJsonString(JsonOptions), Encoding.UTF8, "application/json");

        using HttpResponseMessage response = await _httpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
        string body = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
        if (!response.IsSuccessStatusCode)
        {
            return BridgeResponse.Failed((int)response.StatusCode, ReadGatewayErrorMessage(body));
        }

        try
        {
            JsonNode? node = JsonNode.Parse(body);
            return node is JsonObject root
                ? BridgeResponse.Ok(root)
                : BridgeResponse.Failed(502, "上游 Responses 返回了非对象 JSON。");
        }
        catch (JsonException exception)
        {
            return BridgeResponse.Failed(502, $"上游 Responses 返回无法解析：{exception.Message}");
        }
    }

    private async Task<BridgeStreamResponse> SendResponsesStreamRequestAsync(
        JsonObject payload,
        CancellationToken cancellationToken)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, $"{_configuration.UpstreamBaseUrl}/responses");
        AddUpstreamAuthorization(request);
        request.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("text/event-stream"));
        request.Content = new StringContent(payload.ToJsonString(JsonOptions), Encoding.UTF8, "application/json");

        using HttpResponseMessage response = await _httpClient.SendAsync(request, cancellationToken).ConfigureAwait(false);
        string body = await response.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
        if (!response.IsSuccessStatusCode)
        {
            return BridgeStreamResponse.Failed((int)response.StatusCode, ReadGatewayErrorMessage(body));
        }

        return BridgeStreamResponse.Ok(body);
    }

    private Task<HttpResponseData> CreateResponsesStreamResponseAsync(
        JsonObject payload,
        string requestedModel,
        CancellationToken cancellationToken)
    {
        return Task.FromResult(SseStreamingResponse(async (clientStream, writeCancellationToken) =>
        {
            var streamState = new AnthropicStreamState();
            try
            {
                await WriteAnthropicStreamStartAsync(clientStream, requestedModel, writeCancellationToken).ConfigureAwait(false);
                using var request = new HttpRequestMessage(HttpMethod.Post, $"{_configuration.UpstreamBaseUrl}/responses");
                AddUpstreamAuthorization(request);
                request.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("text/event-stream"));
                request.Content = new StringContent(payload.ToJsonString(JsonOptions), Encoding.UTF8, "application/json");

                using HttpResponseMessage response = await _httpClient.SendAsync(
                        request,
                        HttpCompletionOption.ResponseHeadersRead,
                        writeCancellationToken)
                    .ConfigureAwait(false);

                if (!response.IsSuccessStatusCode)
                {
                    string body = await response.Content.ReadAsStringAsync(writeCancellationToken).ConfigureAwait(false);
                    await WriteAnthropicTextDeltaAsync(clientStream, streamState, $"上游返回错误：{ReadGatewayErrorMessage(body)}", writeCancellationToken)
                        .ConfigureAwait(false);
                    await WriteAnthropicStreamStopAsync(clientStream, streamState, EmptyAnthropicUsage(), "end_turn", writeCancellationToken)
                        .ConfigureAwait(false);
                    return;
                }

                await PumpResponsesStreamAsAnthropicAsync(
                        response,
                        requestedModel,
                        clientStream,
                        streamState,
                        writeCancellationToken)
                    .ConfigureAwait(false);
            }
            catch (Exception exception) when (!writeCancellationToken.IsCancellationRequested)
            {
                await TryWriteAnthropicStreamErrorStopAsync(clientStream, exception, streamState, writeCancellationToken)
                    .ConfigureAwait(false);
            }
        }));
    }

    private BridgeRequest BuildResponsesRequest(JsonElement root, bool stream)
    {
        string model = ResolveModel(root.TryGetProperty("model", out JsonElement modelElement)
            ? modelElement.GetString()
            : null);
        var payload = new JsonObject
        {
            ["model"] = model,
            ["input"] = BuildResponsesInput(root),
            ["stream"] = stream,
        };

        if (TryReadSystem(root, out string? instructions))
        {
            payload["instructions"] = instructions;
        }

        if (root.TryGetProperty("max_tokens", out JsonElement maxTokens) &&
            maxTokens.TryGetInt32(out int maxOutputTokens) &&
            maxOutputTokens > 0)
        {
            payload["max_output_tokens"] = maxOutputTokens;
        }

        CopyNumber(root, payload, "temperature");
        CopyNumber(root, payload, "top_p");

        if (root.TryGetProperty("tools", out JsonElement toolsElement) &&
            toolsElement.ValueKind == JsonValueKind.Array)
        {
            JsonArray tools = BuildResponsesTools(toolsElement);
            if (tools.Count > 0)
            {
                payload["tools"] = tools;
            }
        }

        return new BridgeRequest(model, payload);
    }

    private JsonArray BuildResponsesInput(JsonElement root)
    {
        var input = new JsonArray();
        if (!root.TryGetProperty("messages", out JsonElement messages) ||
            messages.ValueKind != JsonValueKind.Array)
        {
            return input;
        }

        foreach (JsonElement message in messages.EnumerateArray())
        {
            string role = message.TryGetProperty("role", out JsonElement roleElement)
                ? roleElement.GetString() ?? "user"
                : "user";
            if (!string.Equals(role, "assistant", StringComparison.OrdinalIgnoreCase))
            {
                role = "user";
            }

            JsonElement content = message.TryGetProperty("content", out JsonElement contentElement)
                ? contentElement
                : default;
            AppendAnthropicMessageToResponsesInput(input, role, content);
        }

        return input;
    }

    private static void AppendAnthropicMessageToResponsesInput(JsonArray input, string role, JsonElement content)
    {
        if (content.ValueKind != JsonValueKind.Array)
        {
            AppendResponsesMessage(input, role, ReadAnthropicContentAsText(content));
            return;
        }

        var textParts = new List<string>();
        foreach (JsonElement part in content.EnumerateArray())
        {
            string type = part.TryGetProperty("type", out JsonElement typeElement)
                ? typeElement.GetString() ?? string.Empty
                : string.Empty;
            if (string.Equals(type, "text", StringComparison.OrdinalIgnoreCase))
            {
                if (part.TryGetProperty("text", out JsonElement textElement) &&
                    textElement.GetString() is { Length: > 0 } text)
                {
                    textParts.Add(text);
                }
                continue;
            }

            if (string.Equals(type, "tool_use", StringComparison.OrdinalIgnoreCase) &&
                string.Equals(role, "assistant", StringComparison.OrdinalIgnoreCase))
            {
                FlushResponsesMessage(input, role, textParts);
                input.Add(BuildResponsesFunctionCall(part));
                continue;
            }

            if (string.Equals(type, "tool_result", StringComparison.OrdinalIgnoreCase) &&
                string.Equals(role, "user", StringComparison.OrdinalIgnoreCase))
            {
                FlushResponsesMessage(input, role, textParts);
                input.Add(BuildResponsesFunctionCallOutput(part));
                continue;
            }

            string fallback = string.Equals(type, "image", StringComparison.OrdinalIgnoreCase)
                ? "[图片内容：当前本地协议桥暂不转发图片二进制，仅保留图片占位]"
                : part.GetRawText();
            if (!string.IsNullOrWhiteSpace(fallback))
            {
                textParts.Add(fallback);
            }
        }

        FlushResponsesMessage(input, role, textParts);
    }

    private static void FlushResponsesMessage(JsonArray input, string role, List<string> textParts)
    {
        if (textParts.Count == 0)
        {
            return;
        }

        AppendResponsesMessage(input, role, string.Join("\n", textParts));
        textParts.Clear();
    }

    private static void AppendResponsesMessage(JsonArray input, string role, string content)
    {
        if (string.IsNullOrWhiteSpace(content))
        {
            return;
        }

        input.Add(new JsonObject
        {
            ["role"] = role,
            ["content"] = content,
        });
    }

    private static JsonObject BuildResponsesFunctionCall(JsonElement toolUse)
    {
        string id = toolUse.TryGetProperty("id", out JsonElement idElement)
            ? idElement.GetString() ?? $"call_{Guid.NewGuid():N}"
            : $"call_{Guid.NewGuid():N}";
        string name = toolUse.TryGetProperty("name", out JsonElement nameElement)
            ? nameElement.GetString() ?? "tool"
            : "tool";
        string arguments = toolUse.TryGetProperty("input", out JsonElement inputElement)
            ? inputElement.GetRawText()
            : "{}";
        return new JsonObject
        {
            ["type"] = "function_call",
            ["call_id"] = id,
            ["name"] = name,
            ["arguments"] = arguments,
        };
    }

    private static JsonObject BuildResponsesFunctionCallOutput(JsonElement toolResult)
    {
        string id = toolResult.TryGetProperty("tool_use_id", out JsonElement idElement)
            ? idElement.GetString() ?? string.Empty
            : string.Empty;
        string output = toolResult.TryGetProperty("content", out JsonElement contentElement)
            ? ReadAnthropicContentAsText(contentElement)
            : string.Empty;
        return new JsonObject
        {
            ["type"] = "function_call_output",
            ["call_id"] = id,
            ["output"] = output,
        };
    }

    private static JsonArray BuildResponsesTools(JsonElement toolsElement)
    {
        var tools = new JsonArray();
        foreach (JsonElement tool in toolsElement.EnumerateArray())
        {
            if (!tool.TryGetProperty("name", out JsonElement nameElement) ||
                nameElement.GetString() is not { Length: > 0 } name)
            {
                continue;
            }

            var converted = new JsonObject
            {
                ["type"] = "function",
                ["name"] = name,
            };
            if (tool.TryGetProperty("description", out JsonElement description) &&
                description.GetString() is { Length: > 0 } descriptionText)
            {
                converted["description"] = descriptionText;
            }
            if (tool.TryGetProperty("input_schema", out JsonElement schema))
            {
                converted["parameters"] = JsonNode.Parse(schema.GetRawText());
            }
            tools.Add(converted);
        }

        return tools;
    }

    private JsonObject BuildAnthropicMessageResponse(JsonObject upstream, string requestedModel)
    {
        JsonArray content = ExtractAnthropicContentBlocks(upstream);
        if (content.Count == 0)
        {
            content.Add(new JsonObject { ["type"] = "text", ["text"] = ExtractOutputText(upstream) });
        }

        JsonObject usage = ExtractUsage(upstream);
        return new JsonObject
        {
            ["id"] = upstream["id"]?.GetValue<string>() ?? $"msg_{Guid.NewGuid():N}",
            ["type"] = "message",
            ["role"] = "assistant",
            ["model"] = requestedModel,
            ["content"] = content,
            ["stop_reason"] = content.Any(IsToolUseBlock) ? "tool_use" : "end_turn",
            ["stop_sequence"] = null,
            ["usage"] = usage,
        };
    }

    private static JsonArray ExtractAnthropicContentBlocks(JsonObject upstream)
    {
        var blocks = new JsonArray();
        if (upstream["output"] is JsonArray output)
        {
            foreach (JsonNode? itemNode in output)
            {
                if (itemNode is not JsonObject item)
                {
                    continue;
                }

                string type = item["type"]?.GetValue<string>() ?? string.Empty;
                if (string.Equals(type, "message", StringComparison.OrdinalIgnoreCase) &&
                    item["content"] is JsonArray content)
                {
                    foreach (JsonNode? contentNode in content)
                    {
                        if (contentNode is not JsonObject contentItem)
                        {
                            continue;
                        }

                        string contentType = contentItem["type"]?.GetValue<string>() ?? string.Empty;
                        string? text = contentItem["text"]?.GetValue<string>() ??
                                       contentItem["output_text"]?.GetValue<string>();
                        if (!string.IsNullOrEmpty(text) &&
                            (string.Equals(contentType, "output_text", StringComparison.OrdinalIgnoreCase) ||
                             string.Equals(contentType, "text", StringComparison.OrdinalIgnoreCase)))
                        {
                            blocks.Add(new JsonObject { ["type"] = "text", ["text"] = text });
                        }
                    }
                }
                else if (string.Equals(type, "function_call", StringComparison.OrdinalIgnoreCase))
                {
                    blocks.Add(BuildToolUseBlock(item));
                }
            }
        }

        string outputText = ExtractOutputText(upstream);
        if (blocks.Count == 0 && !string.IsNullOrWhiteSpace(outputText))
        {
            blocks.Add(new JsonObject { ["type"] = "text", ["text"] = outputText });
        }

        return blocks;
    }

    private static JsonObject BuildToolUseBlock(JsonObject functionCall)
    {
        string id = functionCall["call_id"]?.GetValue<string>() ??
                    functionCall["id"]?.GetValue<string>() ??
                    $"toolu_{Guid.NewGuid():N}";
        string name = functionCall["name"]?.GetValue<string>() ?? "tool";
        JsonNode input = new JsonObject();
        string? arguments = functionCall["arguments"]?.GetValue<string>();
        if (!string.IsNullOrWhiteSpace(arguments))
        {
            try
            {
                input = JsonNode.Parse(arguments) ?? new JsonObject();
            }
            catch (JsonException)
            {
                input = new JsonObject { ["raw"] = arguments };
            }
        }

        return new JsonObject
        {
            ["type"] = "tool_use",
            ["id"] = id,
            ["name"] = name,
            ["input"] = input,
        };
    }

    private static string BuildAnthropicMessageSse(JsonObject message)
    {
        var builder = new StringBuilder();
        JsonObject messageStart = CloneObject(message);
        messageStart["content"] = new JsonArray();
        messageStart["stop_reason"] = null;
        WriteSse(builder, "message_start", new JsonObject { ["type"] = "message_start", ["message"] = messageStart });

        JsonArray content = message["content"] as JsonArray ?? [];
        for (int index = 0; index < content.Count; index++)
        {
            JsonObject block = content[index] as JsonObject ?? new JsonObject { ["type"] = "text", ["text"] = string.Empty };
            string blockType = block["type"]?.GetValue<string>() ?? "text";
            if (string.Equals(blockType, "tool_use", StringComparison.OrdinalIgnoreCase))
            {
                WriteSse(builder, "content_block_start", new JsonObject
                {
                    ["type"] = "content_block_start",
                    ["index"] = index,
                    ["content_block"] = CloneObject(block),
                });
            }
            else
            {
                WriteSse(builder, "content_block_start", new JsonObject
                {
                    ["type"] = "content_block_start",
                    ["index"] = index,
                    ["content_block"] = new JsonObject { ["type"] = "text", ["text"] = string.Empty },
                });
                WriteSse(builder, "content_block_delta", new JsonObject
                {
                    ["type"] = "content_block_delta",
                    ["index"] = index,
                    ["delta"] = new JsonObject
                    {
                        ["type"] = "text_delta",
                        ["text"] = block["text"]?.GetValue<string>() ?? string.Empty,
                    },
                });
            }

            WriteSse(builder, "content_block_stop", new JsonObject
            {
                ["type"] = "content_block_stop",
                ["index"] = index,
            });
        }

        WriteSse(builder, "message_delta", new JsonObject
        {
            ["type"] = "message_delta",
            ["delta"] = new JsonObject
            {
                ["stop_reason"] = message["stop_reason"]?.GetValue<string>() ?? "end_turn",
                ["stop_sequence"] = null,
            },
            ["usage"] = CloneObject(message["usage"] as JsonObject ?? new JsonObject()),
        });
        WriteSse(builder, "message_stop", new JsonObject { ["type"] = "message_stop" });
        return builder.ToString();
    }

    private static string BuildAnthropicMessageSseFromResponsesStream(string upstreamSse, string requestedModel)
    {
        var textDeltas = new List<string>();
        JsonObject usage = new()
        {
            ["input_tokens"] = 0,
            ["cache_creation_input_tokens"] = 0,
            ["cache_read_input_tokens"] = 0,
            ["output_tokens"] = 0,
        };

        foreach (JsonObject data in ReadSseJsonObjects(upstreamSse))
        {
            string type = data["type"]?.GetValue<string>() ?? string.Empty;
            if (string.Equals(type, "response.output_text.delta", StringComparison.OrdinalIgnoreCase) &&
                data["delta"]?.GetValue<string>() is { Length: > 0 } delta)
            {
                textDeltas.Add(delta);
            }
            else if (string.Equals(type, "response.completed", StringComparison.OrdinalIgnoreCase) &&
                     data["response"] is JsonObject response)
            {
                usage = ExtractUsage(response);
                if (textDeltas.Count == 0)
                {
                    string completedText = ExtractOutputText(response);
                    if (!string.IsNullOrWhiteSpace(completedText))
                    {
                        textDeltas.Add(completedText);
                    }
                }
            }
            else if (string.Equals(type, "response.output_text.done", StringComparison.OrdinalIgnoreCase) &&
                     textDeltas.Count == 0 &&
                     data["text"]?.GetValue<string>() is { Length: > 0 } doneText)
            {
                textDeltas.Add(doneText);
            }
        }

        if (textDeltas.Count == 0)
        {
            textDeltas.Add(string.Empty);
        }

        var builder = new StringBuilder();
        string messageId = $"msg_{Guid.NewGuid():N}";
        WriteSse(builder, "message_start", new JsonObject
        {
            ["type"] = "message_start",
            ["message"] = new JsonObject
            {
                ["id"] = messageId,
                ["type"] = "message",
                ["role"] = "assistant",
                ["model"] = requestedModel,
                ["content"] = new JsonArray(),
                ["stop_reason"] = null,
                ["stop_sequence"] = null,
                ["usage"] = new JsonObject
                {
                    ["input_tokens"] = usage["input_tokens"]?.GetValue<int>() ?? 0,
                    ["cache_creation_input_tokens"] = usage["cache_creation_input_tokens"]?.GetValue<int>() ?? 0,
                    ["cache_read_input_tokens"] = usage["cache_read_input_tokens"]?.GetValue<int>() ?? 0,
                    ["output_tokens"] = 0,
                },
            },
        });
        WriteSse(builder, "content_block_start", new JsonObject
        {
            ["type"] = "content_block_start",
            ["index"] = 0,
            ["content_block"] = new JsonObject { ["type"] = "text", ["text"] = string.Empty },
        });

        foreach (string delta in textDeltas)
        {
            if (string.IsNullOrEmpty(delta))
            {
                continue;
            }

            WriteSse(builder, "content_block_delta", new JsonObject
            {
                ["type"] = "content_block_delta",
                ["index"] = 0,
                ["delta"] = new JsonObject
                {
                    ["type"] = "text_delta",
                    ["text"] = delta,
                },
            });
        }

        WriteSse(builder, "content_block_stop", new JsonObject
        {
            ["type"] = "content_block_stop",
            ["index"] = 0,
        });
        WriteSse(builder, "message_delta", new JsonObject
        {
            ["type"] = "message_delta",
            ["delta"] = new JsonObject
            {
                ["stop_reason"] = "end_turn",
                ["stop_sequence"] = null,
            },
            ["usage"] = usage,
        });
        WriteSse(builder, "message_stop", new JsonObject { ["type"] = "message_stop" });
        return builder.ToString();
    }

    private static async Task PumpResponsesStreamAsAnthropicAsync(
        HttpResponseMessage upstreamResponse,
        string requestedModel,
        NetworkStream clientStream,
        AnthropicStreamState state,
        CancellationToken cancellationToken)
    {
        Stream upstreamStream = await upstreamResponse.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
        using var reader = new StreamReader(upstreamStream, Encoding.UTF8, detectEncodingFromByteOrderMarks: false, bufferSize: 4096, leaveOpen: false);
        var dataLines = new List<string>();
        while (!reader.EndOfStream && !state.IsClosed)
        {
            string? rawLine = await reader.ReadLineAsync(cancellationToken).ConfigureAwait(false);
            if (rawLine is null)
            {
                break;
            }

            string line = rawLine.Trim();
            if (line.Length == 0)
            {
                await ProcessResponsesEventsAsync(ParseSseDataLines(dataLines), requestedModel, clientStream, state, cancellationToken)
                    .ConfigureAwait(false);
                dataLines.Clear();
                continue;
            }

            if (line.StartsWith("data:", StringComparison.OrdinalIgnoreCase))
            {
                dataLines.Add(line[5..].TrimStart());
            }
        }

        if (!state.IsClosed)
        {
            await ProcessResponsesEventsAsync(ParseSseDataLines(dataLines), requestedModel, clientStream, state, cancellationToken)
                .ConfigureAwait(false);
        }

        if (!state.IsClosed)
        {
            await WriteAnthropicTextDeltaAsync(
                    clientStream,
                    state,
                    state.HasContent
                        ? "\n\n上游响应提前结束，未收到完成事件。"
                        : "上游响应提前结束，未返回可用内容。",
                    cancellationToken)
                .ConfigureAwait(false);
            await WriteAnthropicStreamStopAsync(clientStream, state, state.Usage, "end_turn", cancellationToken)
                .ConfigureAwait(false);
        }
    }

    private static async Task ProcessResponsesEventsAsync(
        IEnumerable<JsonObject> events,
        string requestedModel,
        NetworkStream clientStream,
        AnthropicStreamState state,
        CancellationToken cancellationToken)
    {
        foreach (JsonObject data in events)
        {
            await TryWriteAnthropicDeltaFromResponsesEventAsync(
                    data,
                    requestedModel,
                    clientStream,
                    state,
                    cancellationToken)
                .ConfigureAwait(false);
            if (state.IsClosed)
            {
                return;
            }
        }
    }

    private static async Task WriteAnthropicStreamStartAsync(
        NetworkStream clientStream,
        string requestedModel,
        CancellationToken cancellationToken)
    {
        await WriteSseAsync(clientStream, "message_start", new JsonObject
        {
            ["type"] = "message_start",
            ["message"] = new JsonObject
            {
                ["id"] = $"msg_{Guid.NewGuid():N}",
                ["type"] = "message",
                ["role"] = "assistant",
                ["model"] = requestedModel,
                ["content"] = new JsonArray(),
                ["stop_reason"] = null,
                ["stop_sequence"] = null,
                ["usage"] = EmptyAnthropicUsage(outputTokens: 0),
            },
        }, cancellationToken).ConfigureAwait(false);
    }

    private static async Task WriteAnthropicStreamStopAsync(
        NetworkStream clientStream,
        AnthropicStreamState state,
        JsonObject usage,
        string stopReason,
        CancellationToken cancellationToken)
    {
        if (state.IsClosed)
        {
            return;
        }

        await CloseOpenTextBlockAsync(clientStream, state, cancellationToken).ConfigureAwait(false);
        await WriteSseAsync(clientStream, "message_delta", new JsonObject
        {
            ["type"] = "message_delta",
            ["delta"] = new JsonObject
            {
                ["stop_reason"] = stopReason,
                ["stop_sequence"] = null,
            },
            ["usage"] = usage,
        }, cancellationToken).ConfigureAwait(false);
        await WriteSseAsync(clientStream, "message_stop", new JsonObject { ["type"] = "message_stop" }, cancellationToken)
            .ConfigureAwait(false);
        state.IsClosed = true;
    }

    private static async Task TryWriteAnthropicDeltaFromResponsesEventAsync(
        JsonObject data,
        string requestedModel,
        NetworkStream clientStream,
        AnthropicStreamState state,
        CancellationToken cancellationToken)
    {
        string type = data["type"]?.GetValue<string>() ?? string.Empty;
        if (string.Equals(type, "response.output_text.delta", StringComparison.OrdinalIgnoreCase) &&
            data["delta"]?.GetValue<string>() is { Length: > 0 } delta)
        {
            await WriteAnthropicTextDeltaAsync(clientStream, state, delta, cancellationToken).ConfigureAwait(false);
            return;
        }

        if (string.Equals(type, "response.output_text.done", StringComparison.OrdinalIgnoreCase) &&
            !state.HasText &&
            data["text"]?.GetValue<string>() is { Length: > 0 } doneText)
        {
            await WriteAnthropicTextDeltaAsync(clientStream, state, doneText, cancellationToken).ConfigureAwait(false);
            return;
        }

        if (string.Equals(type, "response.output_item.done", StringComparison.OrdinalIgnoreCase) &&
            data["item"] is JsonObject completedItem &&
            string.Equals(completedItem["type"]?.GetValue<string>(), "function_call", StringComparison.OrdinalIgnoreCase))
        {
            await WriteAnthropicToolUseAsync(clientStream, state, BuildToolUseBlock(completedItem), cancellationToken)
                .ConfigureAwait(false);
            return;
        }

        if ((string.Equals(type, "response.completed", StringComparison.OrdinalIgnoreCase) ||
             string.Equals(type, "response.done", StringComparison.OrdinalIgnoreCase)))
        {
            JsonObject response = data["response"] as JsonObject ?? new JsonObject();
            state.Usage = ExtractUsage(response);
            await WriteCompletedResponseContentAsync(clientStream, state, response, cancellationToken).ConfigureAwait(false);
            if (!state.HasContent)
            {
                await WriteAnthropicTextDeltaAsync(clientStream, state, "上游已完成请求，但没有返回正文或工具调用。", cancellationToken)
                    .ConfigureAwait(false);
            }
            await WriteAnthropicStreamStopAsync(
                    clientStream,
                    state,
                    state.Usage,
                    state.HasToolUse ? "tool_use" : "end_turn",
                    cancellationToken)
                .ConfigureAwait(false);
            return;
        }

        if (string.Equals(type, "response.failed", StringComparison.OrdinalIgnoreCase) ||
            string.Equals(type, "response.incomplete", StringComparison.OrdinalIgnoreCase) ||
            string.Equals(type, "error", StringComparison.OrdinalIgnoreCase))
        {
            string message = ReadResponsesStreamError(data);
            await WriteAnthropicTextDeltaAsync(clientStream, state, $"上游请求失败：{message}", cancellationToken)
                .ConfigureAwait(false);
            await WriteAnthropicStreamStopAsync(clientStream, state, state.Usage, "end_turn", cancellationToken)
                .ConfigureAwait(false);
        }
    }

    private static async Task WriteCompletedResponseContentAsync(
        NetworkStream stream,
        AnthropicStreamState state,
        JsonObject response,
        CancellationToken cancellationToken)
    {
        foreach (JsonNode? blockNode in ExtractAnthropicContentBlocks(response))
        {
            if (blockNode is not JsonObject block)
            {
                continue;
            }

            string type = block["type"]?.GetValue<string>() ?? string.Empty;
            if (string.Equals(type, "text", StringComparison.OrdinalIgnoreCase))
            {
                if (!state.HasText && block["text"]?.GetValue<string>() is { Length: > 0 } text)
                {
                    await WriteAnthropicTextDeltaAsync(stream, state, text, cancellationToken).ConfigureAwait(false);
                }
            }
            else if (string.Equals(type, "tool_use", StringComparison.OrdinalIgnoreCase))
            {
                await WriteAnthropicToolUseAsync(stream, state, block, cancellationToken).ConfigureAwait(false);
            }
        }
    }

    private static async Task WriteAnthropicTextDeltaAsync(
        NetworkStream stream,
        AnthropicStreamState state,
        string text,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrEmpty(text) || state.IsClosed)
        {
            return;
        }

        if (state.OpenTextBlockIndex is null)
        {
            int index = state.NextBlockIndex++;
            state.OpenTextBlockIndex = index;
            await WriteSseAsync(stream, "content_block_start", new JsonObject
            {
                ["type"] = "content_block_start",
                ["index"] = index,
                ["content_block"] = new JsonObject { ["type"] = "text", ["text"] = string.Empty },
            }, cancellationToken).ConfigureAwait(false);
        }

        await WriteSseAsync(stream, "content_block_delta", new JsonObject
        {
            ["type"] = "content_block_delta",
            ["index"] = state.OpenTextBlockIndex.Value,
            ["delta"] = new JsonObject
            {
                ["type"] = "text_delta",
                ["text"] = text,
            },
        }, cancellationToken).ConfigureAwait(false);
        state.HasText = true;
        state.HasContent = true;
    }

    private static async Task WriteAnthropicToolUseAsync(
        NetworkStream stream,
        AnthropicStreamState state,
        JsonObject block,
        CancellationToken cancellationToken)
    {
        string id = block["id"]?.GetValue<string>() ?? $"toolu_{Guid.NewGuid():N}";
        if (!state.EmittedToolCallIds.Add(id))
        {
            return;
        }

        await CloseOpenTextBlockAsync(stream, state, cancellationToken).ConfigureAwait(false);
        int index = state.NextBlockIndex++;
        string name = block["name"]?.GetValue<string>() ?? "tool";
        string arguments = block["input"]?.ToJsonString(JsonOptions) ?? "{}";
        await WriteSseAsync(stream, "content_block_start", new JsonObject
        {
            ["type"] = "content_block_start",
            ["index"] = index,
            ["content_block"] = new JsonObject
            {
                ["type"] = "tool_use",
                ["id"] = id,
                ["name"] = name,
                ["input"] = new JsonObject(),
            },
        }, cancellationToken).ConfigureAwait(false);
        await WriteSseAsync(stream, "content_block_delta", new JsonObject
        {
            ["type"] = "content_block_delta",
            ["index"] = index,
            ["delta"] = new JsonObject
            {
                ["type"] = "input_json_delta",
                ["partial_json"] = arguments,
            },
        }, cancellationToken).ConfigureAwait(false);
        await WriteSseAsync(stream, "content_block_stop", new JsonObject
        {
            ["type"] = "content_block_stop",
            ["index"] = index,
        }, cancellationToken).ConfigureAwait(false);
        state.HasToolUse = true;
        state.HasContent = true;
    }

    private static async Task CloseOpenTextBlockAsync(
        NetworkStream stream,
        AnthropicStreamState state,
        CancellationToken cancellationToken)
    {
        if (state.OpenTextBlockIndex is not int index)
        {
            return;
        }

        await WriteSseAsync(stream, "content_block_stop", new JsonObject
        {
            ["type"] = "content_block_stop",
            ["index"] = index,
        }, cancellationToken).ConfigureAwait(false);
        state.OpenTextBlockIndex = null;
    }

    private static string ReadResponsesStreamError(JsonObject data)
    {
        JsonObject? response = data["response"] as JsonObject;
        JsonObject? error = data["error"] as JsonObject ?? response?["error"] as JsonObject;
        return error?["message"]?.GetValue<string>() ??
               response?["incomplete_details"]?["reason"]?.GetValue<string>() ??
               data["message"]?.GetValue<string>() ??
               "上游没有返回错误详情。";
    }

    private static async Task TryWriteAnthropicStreamErrorStopAsync(
        NetworkStream stream,
        Exception exception,
        AnthropicStreamState state,
        CancellationToken cancellationToken)
    {
        if (state.IsClosed)
        {
            return;
        }

        try
        {
            string message = exception is TaskCanceledException or TimeoutException
                ? "上游响应超时，请稍后重试或检查当前来源的 Grok 线路。"
                : $"桥接请求失败：{exception.Message}";
            await WriteAnthropicTextDeltaAsync(stream, state, message, cancellationToken).ConfigureAwait(false);
            await WriteAnthropicStreamStopAsync(stream, state, state.Usage, "end_turn", cancellationToken).ConfigureAwait(false);
        }
        catch (IOException)
        {
        }
        catch (ObjectDisposedException)
        {
        }
    }

    private static JsonObject EmptyAnthropicUsage(int outputTokens = 0) => new()
    {
        ["input_tokens"] = 0,
        ["cache_creation_input_tokens"] = 0,
        ["cache_read_input_tokens"] = 0,
        ["output_tokens"] = outputTokens,
    };

    private static IEnumerable<JsonObject> ReadSseJsonObjects(string body)
    {
        var dataLines = new List<string>();
        foreach (string rawLine in body.Replace("\r\n", "\n").Split('\n'))
        {
            string line = rawLine.Trim();
            if (line.Length == 0)
            {
                foreach (JsonObject item in ParseSseDataLines(dataLines))
                {
                    yield return item;
                }
                dataLines.Clear();
                continue;
            }

            if (line.StartsWith("data:", StringComparison.OrdinalIgnoreCase))
            {
                dataLines.Add(line[5..].TrimStart());
            }
        }

        foreach (JsonObject item in ParseSseDataLines(dataLines))
        {
            yield return item;
        }
    }

    private static IEnumerable<JsonObject> ParseSseDataLines(IReadOnlyList<string> dataLines)
    {
        if (dataLines.Count == 0)
        {
            yield break;
        }

        string data = string.Join("\n", dataLines).Trim();
        if (data.Length == 0 || string.Equals(data, "[DONE]", StringComparison.OrdinalIgnoreCase))
        {
            yield break;
        }

        JsonNode? node;
        try
        {
            node = JsonNode.Parse(data);
        }
        catch (JsonException)
        {
            yield break;
        }

        if (node is JsonObject obj)
        {
            yield return obj;
        }
    }

    private static void WriteSse(StringBuilder builder, string eventName, JsonObject data)
    {
        builder.Append("event: ").Append(eventName).Append("\n");
        builder.Append("data: ").Append(data.ToJsonString(JsonOptions)).Append("\n\n");
    }

    private static async Task WriteSseAsync(
        NetworkStream stream,
        string eventName,
        JsonObject data,
        CancellationToken cancellationToken)
    {
        string text = $"event: {eventName}\n" +
                      $"data: {data.ToJsonString(JsonOptions)}\n\n";
        byte[] bytes = Encoding.UTF8.GetBytes(text);
        await stream.WriteAsync(bytes, cancellationToken).ConfigureAwait(false);
        await stream.FlushAsync(cancellationToken).ConfigureAwait(false);
    }

    private JsonObject BuildModelList()
    {
        return new JsonObject
        {
            ["data"] = new JsonArray
            {
                new JsonObject { ["id"] = _configuration.OpusModel, ["type"] = "model" },
                new JsonObject { ["id"] = _configuration.SonnetModel, ["type"] = "model" },
                new JsonObject { ["id"] = _configuration.HaikuModel, ["type"] = "model" },
            },
        };
    }

    private string ResolveModel(string? requested)
    {
        string model = requested?.Trim() ?? string.Empty;
        if (string.IsNullOrWhiteSpace(model))
        {
            return _configuration.SonnetModel;
        }

        if (model.Contains("opus", StringComparison.OrdinalIgnoreCase))
        {
            return _configuration.OpusModel;
        }
        if (model.Contains("haiku", StringComparison.OrdinalIgnoreCase) ||
            model.Contains("small", StringComparison.OrdinalIgnoreCase) ||
            model.Contains("fast", StringComparison.OrdinalIgnoreCase))
        {
            return _configuration.HaikuModel;
        }
        if (model.Contains("sonnet", StringComparison.OrdinalIgnoreCase))
        {
            return _configuration.SonnetModel;
        }

        return model;
    }

    private static bool TryReadSystem(JsonElement root, out string? instructions)
    {
        instructions = null;
        if (!root.TryGetProperty("system", out JsonElement system))
        {
            return false;
        }

        instructions = ReadAnthropicContentAsText(system);
        return !string.IsNullOrWhiteSpace(instructions);
    }

    private static string ReadAnthropicContentAsText(JsonElement element)
    {
        if (element.ValueKind == JsonValueKind.Undefined || element.ValueKind == JsonValueKind.Null)
        {
            return string.Empty;
        }
        if (element.ValueKind == JsonValueKind.String)
        {
            return element.GetString() ?? string.Empty;
        }
        if (element.ValueKind != JsonValueKind.Array)
        {
            return element.GetRawText();
        }

        var parts = new List<string>();
        foreach (JsonElement part in element.EnumerateArray())
        {
            string type = part.TryGetProperty("type", out JsonElement typeElement)
                ? typeElement.GetString() ?? string.Empty
                : string.Empty;
            switch (type)
            {
                case "text":
                    if (part.TryGetProperty("text", out JsonElement text))
                    {
                        parts.Add(text.GetString() ?? string.Empty);
                    }
                    break;
                case "tool_result":
                    parts.Add(ReadToolResultAsText(part));
                    break;
                case "tool_use":
                    parts.Add($"[工具调用] {part.GetRawText()}");
                    break;
                case "image":
                    parts.Add("[图片内容：当前本地协议桥暂不转发图片二进制，仅保留图片占位]");
                    break;
                default:
                    parts.Add(part.GetRawText());
                    break;
            }
        }

        return string.Join("\n", parts.Where(part => !string.IsNullOrWhiteSpace(part)));
    }

    private static string ReadToolResultAsText(JsonElement toolResult)
    {
        string id = toolResult.TryGetProperty("tool_use_id", out JsonElement idElement)
            ? idElement.GetString() ?? string.Empty
            : string.Empty;
        string content = toolResult.TryGetProperty("content", out JsonElement contentElement)
            ? ReadAnthropicContentAsText(contentElement)
            : string.Empty;
        return string.IsNullOrWhiteSpace(id)
            ? $"[工具结果]\n{content}"
            : $"[工具结果 {id}]\n{content}";
    }

    private static string ExtractOutputText(JsonObject upstream)
    {
        if (upstream["output_text"]?.GetValue<string>() is { Length: > 0 } outputText)
        {
            return outputText;
        }

        if (upstream["output"] is JsonArray output)
        {
            foreach (JsonNode? itemNode in output)
            {
                if (itemNode is not JsonObject item ||
                    item["content"] is not JsonArray content)
                {
                    continue;
                }

                foreach (JsonNode? contentNode in content)
                {
                    if (contentNode is JsonObject contentItem &&
                        (contentItem["text"]?.GetValue<string>() ??
                         contentItem["output_text"]?.GetValue<string>()) is { Length: > 0 } text)
                    {
                        return text;
                    }
                }
            }
        }

        return string.Empty;
    }

    private static JsonObject ExtractUsage(JsonObject upstream)
    {
        JsonObject source = upstream["usage"] as JsonObject ?? new JsonObject();
        return new JsonObject
        {
            ["input_tokens"] = ReadInt(source, "input_tokens", "prompt_tokens"),
            ["cache_creation_input_tokens"] = ReadInt(source, "cache_creation_input_tokens"),
            ["cache_read_input_tokens"] = ReadInt(source, "cache_read_input_tokens", "cached_tokens"),
            ["output_tokens"] = ReadInt(source, "output_tokens", "completion_tokens"),
        };
    }

    private static int ReadInt(JsonObject source, params string[] names)
    {
        foreach (string name in names)
        {
            if (source[name] is JsonValue value &&
                value.TryGetValue(out int intValue))
            {
                return Math.Max(0, intValue);
            }
        }

        return 0;
    }

    private static bool IsToolUseBlock(JsonNode? node) =>
        node is JsonObject block &&
        string.Equals(block["type"]?.GetValue<string>(), "tool_use", StringComparison.OrdinalIgnoreCase);

    private static void CopyNumber(JsonElement source, JsonObject target, string name)
    {
        if (source.TryGetProperty(name, out JsonElement element) &&
            element.ValueKind is JsonValueKind.Number)
        {
            target[name] = JsonNode.Parse(element.GetRawText());
        }
    }

    private bool IsAuthorized(HttpRequestData request)
    {
        if (request.Headers.TryGetValue("authorization", out string? authorization) &&
            authorization.StartsWith("Bearer ", StringComparison.OrdinalIgnoreCase))
        {
            return string.Equals(
                authorization[7..].Trim(),
                AuthToken,
                StringComparison.Ordinal);
        }

        return request.Headers.TryGetValue("x-api-key", out string? apiKey) &&
               string.Equals(apiKey.Trim(), AuthToken, StringComparison.Ordinal);
    }

    private void AddUpstreamAuthorization(HttpRequestMessage request)
    {
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", _configuration.UpstreamSecret);
        request.Headers.TryAddWithoutValidation("x-api-key", _configuration.UpstreamSecret);
    }

    private static async Task<HttpRequestData> ReadRequestAsync(
        NetworkStream stream,
        CancellationToken cancellationToken)
    {
        var buffer = new byte[8192];
        using var headerBytes = new MemoryStream();
        int headerEnd = -1;
        while (headerEnd < 0)
        {
            int read = await stream.ReadAsync(buffer.AsMemory(0, buffer.Length), cancellationToken).ConfigureAwait(false);
            if (read == 0)
            {
                throw new IOException("客户端连接已关闭。");
            }

            headerBytes.Write(buffer, 0, read);
            if (headerBytes.Length > MaxHeaderBytes)
            {
                throw new InvalidOperationException("HTTP 请求头过大。");
            }

            byte[] current = headerBytes.ToArray();
            headerEnd = IndexOfHeaderEnd(current);
        }

        byte[] all = headerBytes.ToArray();
        string headerText = Encoding.ASCII.GetString(all, 0, headerEnd);
        string[] lines = headerText.Split("\r\n", StringSplitOptions.None);
        string[] requestLine = lines[0].Split(' ', 3, StringSplitOptions.RemoveEmptyEntries);
        if (requestLine.Length < 2)
        {
            throw new InvalidOperationException("HTTP 请求行无效。");
        }

        var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        foreach (string line in lines.Skip(1))
        {
            int colon = line.IndexOf(':');
            if (colon > 0)
            {
                headers[line[..colon].Trim().ToLowerInvariant()] = line[(colon + 1)..].Trim();
            }
        }

        int bodyStart = headerEnd + 4;
        int contentLength = headers.TryGetValue("content-length", out string? lengthText) &&
                            int.TryParse(lengthText, out int parsed)
            ? parsed
            : 0;
        if (contentLength > MaxBodyBytes)
        {
            throw new InvalidOperationException("HTTP 请求体过大。");
        }

        byte[] body = new byte[contentLength];
        int copied = Math.Min(contentLength, all.Length - bodyStart);
        if (copied > 0)
        {
            Buffer.BlockCopy(all, bodyStart, body, 0, copied);
        }

        while (copied < contentLength)
        {
            int read = await stream.ReadAsync(body.AsMemory(copied, contentLength - copied), cancellationToken)
                .ConfigureAwait(false);
            if (read == 0)
            {
                throw new IOException("请求体尚未读取完成，客户端连接已关闭。");
            }
            copied += read;
        }

        return new HttpRequestData(requestLine[0], requestLine[1], headers, body);
    }

    private static async Task WriteResponseAsync(
        NetworkStream stream,
        HttpResponseData response,
        CancellationToken cancellationToken)
    {
        byte[] body = response.BodyWriter is null
            ? Encoding.UTF8.GetBytes(response.Body)
            : [];
        var builder = new StringBuilder();
        builder.Append("HTTP/1.1 ").Append(response.StatusCode).Append(' ').Append(GetReasonPhrase(response.StatusCode)).Append("\r\n");
        builder.Append("Content-Type: ").Append(response.ContentType).Append("\r\n");
        if (response.BodyWriter is null)
        {
            builder.Append("Content-Length: ").Append(body.Length).Append("\r\n");
        }
        builder.Append("Connection: close\r\n");
        foreach ((string name, string value) in response.Headers)
        {
            builder.Append(name).Append(": ").Append(value).Append("\r\n");
        }
        builder.Append("\r\n");

        byte[] header = Encoding.ASCII.GetBytes(builder.ToString());
        await stream.WriteAsync(header, cancellationToken).ConfigureAwait(false);
        if (response.BodyWriter is null)
        {
            await stream.WriteAsync(body, cancellationToken).ConfigureAwait(false);
        }
        else
        {
            await response.BodyWriter(stream, cancellationToken).ConfigureAwait(false);
        }
    }

    private static int IndexOfHeaderEnd(byte[] bytes)
    {
        for (int i = 0; i <= bytes.Length - 4; i++)
        {
            if (bytes[i] == '\r' && bytes[i + 1] == '\n' && bytes[i + 2] == '\r' && bytes[i + 3] == '\n')
            {
                return i;
            }
        }

        return -1;
    }

    private static HttpResponseData JsonResponse(int statusCode, JsonObject body) =>
        new(statusCode, "application/json; charset=utf-8", body.ToJsonString(JsonOptions));

    private static HttpResponseData SseResponse(string body) =>
        new(
            200,
            "text/event-stream; charset=utf-8",
            body,
            new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase)
            {
                ["Cache-Control"] = "no-cache",
            });

    private static HttpResponseData SseStreamingResponse(Func<NetworkStream, CancellationToken, Task> bodyWriter) =>
        new(
            200,
            "text/event-stream; charset=utf-8",
            string.Empty,
            new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase)
            {
                ["Cache-Control"] = "no-cache",
                ["X-Accel-Buffering"] = "no",
            },
            bodyWriter);

    private static JsonObject ErrorBody(string type, string message) => new()
    {
        ["type"] = "error",
        ["error"] = new JsonObject
        {
            ["type"] = type,
            ["message"] = message,
        },
    };

    private static int EstimateTokens(JsonElement root)
    {
        string text = root.GetRawText();
        if (root.TryGetProperty("messages", out JsonElement messages) && messages.ValueKind == JsonValueKind.Array)
        {
            text = string.Join("\n", messages.EnumerateArray().Select(message =>
                message.TryGetProperty("content", out JsonElement content)
                    ? ReadAnthropicContentAsText(content)
                    : string.Empty));
        }

        return Math.Max(1, text.Length / 4);
    }

    private static string ReadGatewayErrorMessage(string body)
    {
        if (string.IsNullOrWhiteSpace(body))
        {
            return "上游没有返回错误详情。";
        }

        try
        {
            using JsonDocument document = JsonDocument.Parse(body);
            JsonElement root = document.RootElement;
            if (root.TryGetProperty("error", out JsonElement error))
            {
                if (error.ValueKind == JsonValueKind.Object &&
                    error.TryGetProperty("message", out JsonElement nested))
                {
                    return Limit(nested.GetString());
                }
                if (error.ValueKind == JsonValueKind.String)
                {
                    return Limit(error.GetString());
                }
            }
            if (root.TryGetProperty("message", out JsonElement message))
            {
                return Limit(message.GetString());
            }
        }
        catch (JsonException)
        {
        }

        return Limit(body);
    }

    private static JsonObject CloneObject(JsonObject source) =>
        JsonNode.Parse(source.ToJsonString(JsonOptions)) as JsonObject ?? new JsonObject();

    private static string Limit(string? message)
    {
        string value = (message ?? string.Empty).Replace("\r", " ").Replace("\n", " ").Trim();
        return value.Length <= 500 ? value : value[..500];
    }

    private static string GetReasonPhrase(int statusCode) => statusCode switch
    {
        200 => "OK",
        400 => "Bad Request",
        401 => "Unauthorized",
        404 => "Not Found",
        405 => "Method Not Allowed",
        500 => "Internal Server Error",
        502 => "Bad Gateway",
        _ => "OK",
    };

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }

        _disposed = true;
        _shutdown.Cancel();
        try { _listener.Stop(); } catch (SocketException) { } catch (ObjectDisposedException) { }
        try { _acceptLoop?.Wait(TimeSpan.FromSeconds(1)); } catch (AggregateException) { }
        _shutdown.Dispose();
    }

    private sealed record BridgeConfiguration(
        string UpstreamBaseUrl,
        string UpstreamSecret,
        string OpusModel,
        string SonnetModel,
        string HaikuModel);

    private sealed class AnthropicStreamState
    {
        public int NextBlockIndex { get; set; }
        public int? OpenTextBlockIndex { get; set; }
        public bool HasText { get; set; }
        public bool HasToolUse { get; set; }
        public bool HasContent { get; set; }
        public bool IsClosed { get; set; }
        public JsonObject Usage { get; set; } = EmptyAnthropicUsage();
        public HashSet<string> EmittedToolCallIds { get; } = new(StringComparer.Ordinal);
    }

    private sealed record BridgeRequest(string Model, JsonObject Payload);

    private sealed record HttpRequestData(
        string Method,
        string Path,
        IReadOnlyDictionary<string, string> Headers,
        byte[] Body);

    private sealed record HttpResponseData(
        int StatusCode,
        string ContentType,
        string Body,
        IReadOnlyDictionary<string, string>? Headers = null,
        Func<NetworkStream, CancellationToken, Task>? BodyWriter = null)
    {
        public IReadOnlyDictionary<string, string> Headers { get; } =
            Headers ?? new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
    }

    private sealed class BridgeResponse
    {
        private BridgeResponse(bool success, int statusCode, JsonObject? root, string errorMessage)
        {
            Success = success;
            StatusCode = statusCode;
            Root = root ?? new JsonObject();
            ErrorMessage = errorMessage;
        }

        public bool Success { get; }
        public int StatusCode { get; }
        public JsonObject Root { get; }
        public string ErrorMessage { get; }

        public static BridgeResponse Ok(JsonObject root) => new(true, 200, root, string.Empty);

        public static BridgeResponse Failed(int statusCode, string errorMessage) =>
            new(false, statusCode is >= 400 and <= 599 ? statusCode : 502, null, errorMessage);
    }

    private sealed class BridgeStreamResponse
    {
        private BridgeStreamResponse(bool success, int statusCode, string body, string errorMessage)
        {
            Success = success;
            StatusCode = statusCode;
            Body = body;
            ErrorMessage = errorMessage;
        }

        public bool Success { get; }
        public int StatusCode { get; }
        public string Body { get; }
        public string ErrorMessage { get; }

        public static BridgeStreamResponse Ok(string body) => new(true, 200, body, string.Empty);

        public static BridgeStreamResponse Failed(int statusCode, string errorMessage) =>
            new(false, statusCode is >= 400 and <= 599 ? statusCode : 502, string.Empty, errorMessage);
    }
}
