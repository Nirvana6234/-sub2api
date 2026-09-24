using LanAi.Workspace.Core;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace LanAi.Workspace.Terminal;

public sealed class CliTerminalCommandFactory
{
    private const string CodexProviderName = "lan_ai_workspace";
    private const string DefaultGatewayGeminiModel = "gemini-3-flash";

    private static readonly string[] GatewayGeminiModels =
    [
        "gemini-3-flash",
        "gemini-3-flash-agent",
        "gemini-3.1-flash-lite",
        "gemini-3.1-pro-low",
        "gemini-3.5-flash-extra-low",
        "gemini-3.5-flash-low",
        "gemini-pro-agent",
    ];

    private static readonly string[] BuiltInVisibleGeminiModelsToHide =
    [
        "auto",
        "gemini-3.1-flash-lite",
        "gemini-3.1-pro-preview",
        "gemini-3-pro-preview",
        "gemini-3-flash-preview",
        "gemini-3.5-flash",
        "gemini-2.5-pro",
        "gemini-2.5-flash",
        "gemini-2.5-flash-lite",
        "gemma-4-31b-it",
        "gemma-4-26b-a4b-it",
    ];

    private readonly IConnectionCredentialProvider _credentials;

    public CliTerminalCommandFactory(IConnectionCredentialProvider credentials)
    {
        _credentials = credentials;
    }

    public async Task<TerminalCommand> CreateAsync(
        CliLaunchRequest request,
        CliInstallation installation,
        ConnectionProfile? connection,
        CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(request);
        ArgumentNullException.ThrowIfNull(installation);

        if (!installation.IsInstalled || string.IsNullOrWhiteSpace(installation.ExecutablePath))
        {
            throw new InvalidOperationException($"尚未检测到 {request.Cli} CLI。 ");
        }

        if (installation.Kind != request.Cli)
        {
            throw new ArgumentException("CLI 安装信息与启动请求不匹配。", nameof(installation));
        }

        var arguments = BuildCliArguments(request, connection);
        var environment = new Dictionary<string, string?>(StringComparer.OrdinalIgnoreCase);

        if (connection is not null)
        {
            ApplyNonSecretConnectionEnvironment(request.Cli, connection, environment);
            var secret = await _credentials.GetSecretAsync(connection.Id, request.Cli, cancellationToken);
            ApplyConnectionEnvironment(request.Cli, secret, environment);
            ConfigureGatewayGeminiModelHome(request, connection, environment);
        }

        var (fileName, wrappedArguments) = WrapScriptHost(installation.ExecutablePath, arguments);
        return new TerminalCommand(
            fileName,
            wrappedArguments,
            request.WorkingDirectory,
            environment,
            $"{request.Cli} · {Path.GetFileName(request.WorkingDirectory)}");
    }

    private static IReadOnlyList<string> BuildCliArguments(
        CliLaunchRequest request,
        ConnectionProfile? connection)
    {
        var arguments = new List<string>();

        switch (request.Cli)
        {
            case CliKind.Codex:
                if (request.Mode != CliLaunchMode.New && !string.IsNullOrWhiteSpace(request.NativeSessionId))
                {
                    arguments.Add(request.Mode == CliLaunchMode.Fork ? "fork" : "resume");
                    arguments.Add(request.NativeSessionId);
                }

                if (!string.IsNullOrWhiteSpace(request.Model))
                {
                    arguments.Add("-m");
                    arguments.Add(request.Model);
                }

                string? codexBaseUrl = connection is null
                    ? null
                    : ResolveBaseUrl(connection, CliKind.Codex);
                if (codexBaseUrl is not null)
                {
                    arguments.AddRange(
                    [
                        "-c", $"model_provider=\"{CodexProviderName}\"",
                        "-c", $"model_providers.{CodexProviderName}.name=\"局域网 AI 工作台\"",
                        "-c", $"model_providers.{CodexProviderName}.base_url=\"{EscapeToml(codexBaseUrl)}\"",
                        "-c", $"model_providers.{CodexProviderName}.wire_api=\"responses\"",
                        "-c", $"model_providers.{CodexProviderName}.requires_openai_auth=true"
                    ]);
                }
                break;

            case CliKind.ClaudeCode:
                if (request.Mode != CliLaunchMode.New && !string.IsNullOrWhiteSpace(request.NativeSessionId))
                {
                    arguments.Add("--resume");
                    arguments.Add(request.NativeSessionId);
                    if (request.Mode == CliLaunchMode.Fork)
                    {
                        arguments.Add("--fork-session");
                    }
                }

                if (!string.IsNullOrWhiteSpace(request.Model))
                {
                    arguments.Add("--model");
                    arguments.Add(request.Model);
                }
                break;

            case CliKind.GeminiCli:
                if (request.Mode != CliLaunchMode.New && !string.IsNullOrWhiteSpace(request.NativeSessionId))
                {
                    arguments.Add("--resume");
                    arguments.Add(request.NativeSessionId);
                }

                if (!string.IsNullOrWhiteSpace(request.Model))
                {
                    arguments.Add("--model");
                    arguments.Add(request.Model);
                }
                break;

            case CliKind.GrokCli:
                if (!string.IsNullOrWhiteSpace(request.Model))
                {
                    arguments.Add("--model");
                    arguments.Add(request.Model);
                }
                break;

            default:
                throw new ArgumentOutOfRangeException(nameof(request.Cli));
        }

        arguments.AddRange(request.AdditionalArguments);
        return arguments;
    }

    private static void ApplyConnectionEnvironment(
        CliKind cli,
        string? secret,
        IDictionary<string, string?> environment)
    {
        if (string.IsNullOrWhiteSpace(secret))
        {
            return;
        }

        switch (cli)
        {
            case CliKind.Codex:
                environment["OPENAI_API_KEY"] = secret;
                break;
            case CliKind.ClaudeCode:
                environment["ANTHROPIC_AUTH_TOKEN"] = secret;
                environment["ANTHROPIC_API_KEY"] = secret;
                break;
            case CliKind.GeminiCli:
                environment["GEMINI_API_KEY"] = secret;
                environment["GOOGLE_API_KEY"] = secret;
                break;
            case CliKind.GrokCli:
                environment["XAI_API_KEY"] = secret;
                environment["OPENAI_API_KEY"] = secret;
                break;
        }
    }

    public static void ApplyNonSecretConnectionEnvironment(
        CliKind cli,
        ConnectionProfile connection,
        IDictionary<string, string?> environment)
    {
        var baseUrl = ResolveBaseUrl(connection, cli);
        if (baseUrl is null)
        {
            return;
        }

        switch (cli)
        {
            case CliKind.ClaudeCode:
                environment["ANTHROPIC_BASE_URL"] = baseUrl.TrimEnd('/');
                environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1";
                break;
            case CliKind.GeminiCli:
                environment["GOOGLE_GEMINI_BASE_URL"] = baseUrl.TrimEnd('/');
                break;
            case CliKind.GrokCli:
                environment["GROK_MODELS_BASE_URL"] = baseUrl.TrimEnd('/');
                environment["OPENAI_BASE_URL"] = baseUrl.TrimEnd('/');
                break;
        }
    }

    private static void ConfigureGatewayGeminiModelHome(
        CliLaunchRequest request,
        ConnectionProfile connection,
        IDictionary<string, string?> environment)
    {
        if (request.Cli != CliKind.GeminiCli ||
            !environment.ContainsKey("GOOGLE_GEMINI_BASE_URL"))
        {
            return;
        }

        string home = GetGatewayGeminiHome(connection);
        Directory.CreateDirectory(home);

        string settingsPath = Path.Combine(home, "settings.json");
        string selectedModel = ResolveGatewayGeminiSelectedModel(settingsPath, request.Model);
        File.WriteAllText(
            settingsPath,
            BuildGatewayGeminiSettingsJson(selectedModel),
            new UTF8Encoding(encoderShouldEmitUTF8Identifier: false));

        environment["GEMINI_CLI_HOME"] = home;
    }

    private static string GetGatewayGeminiHome(ConnectionProfile connection)
    {
        string seed = string.Join(
            "|",
            connection.Id,
            connection.ClientBaseUrls.TryGetValue(CliKind.GeminiCli, out string? geminiUrl)
                ? geminiUrl
                : connection.BaseUrl);
        byte[] hash = SHA256.HashData(Encoding.UTF8.GetBytes(seed));
        string id = Convert.ToHexString(hash)[..16].ToLowerInvariant();
        string root = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
        if (string.IsNullOrWhiteSpace(root))
        {
            root = Path.GetTempPath();
        }

        return Path.Combine(root, "LanAi.Workspace", "gemini-cli", "gateway-profiles", id);
    }

    private static string ResolveGatewayGeminiSelectedModel(string settingsPath, string? requestedModel)
    {
        if (IsGatewayGeminiModel(requestedModel))
        {
            return requestedModel!.Trim();
        }

        try
        {
            if (File.Exists(settingsPath))
            {
                using FileStream stream = File.OpenRead(settingsPath);
                using JsonDocument document = JsonDocument.Parse(stream);
                if (document.RootElement.TryGetProperty("model", out JsonElement modelElement) &&
                    modelElement.TryGetProperty("name", out JsonElement nameElement) &&
                    nameElement.ValueKind == JsonValueKind.String)
                {
                    string? existingModel = nameElement.GetString();
                    if (IsGatewayGeminiModel(existingModel))
                    {
                        return existingModel!.Trim();
                    }
                }
            }
        }
        catch (JsonException)
        {
            // A malformed generated settings file should not block launching the CLI.
        }
        catch (IOException)
        {
            // If the file is temporarily unavailable, fall back to the safe default model.
        }
        catch (UnauthorizedAccessException)
        {
            // Launch should remain possible even when the old settings file cannot be read.
        }

        return DefaultGatewayGeminiModel;
    }

    private static bool IsGatewayGeminiModel(string? model) =>
        !string.IsNullOrWhiteSpace(model) &&
        GatewayGeminiModels.Contains(model.Trim(), StringComparer.OrdinalIgnoreCase);

    private static string BuildGatewayGeminiSettingsJson(string selectedModel)
    {
        var modelDefinitions = new Dictionary<string, object>(StringComparer.Ordinal);
        foreach (string model in BuiltInVisibleGeminiModelsToHide)
        {
            modelDefinitions[model] = new
            {
                isVisible = false,
                isPreview = false,
                tier = model.Equals("auto", StringComparison.Ordinal) ? "auto" : "custom",
                features = new { },
            };
        }

        foreach (string model in GatewayGeminiModels)
        {
            modelDefinitions[model] = new
            {
                displayName = model,
                dialogDescription = "当前中转站密钥支持的模型。",
                tier = "custom",
                family = "gateway-gemini",
                isPreview = false,
                isVisible = true,
                features = new
                {
                    thinking = false,
                    multimodalToolUse = true,
                },
            };
        }

        var settings = new
        {
            security = new
            {
                auth = new
                {
                    selectedType = "gemini-api-key",
                },
            },
            model = new
            {
                name = selectedModel,
            },
            experimental = new
            {
                dynamicModelConfiguration = true,
            },
            modelConfigs = new
            {
                modelDefinitions,
            },
        };

        return JsonSerializer.Serialize(
            settings,
            new JsonSerializerOptions { WriteIndented = true });
    }

    private static string? ResolveBaseUrl(ConnectionProfile profile, CliKind cli)
    {
        string? candidate;
        if (profile.ClientBaseUrls.TryGetValue(cli, out string? clientUrl))
        {
            candidate = clientUrl;
        }
        else if (profile.ClientBaseUrls.Count == 0)
        {
            // A profile without per-client endpoints may intentionally use one
            // shared URL. Once any per-client endpoint exists, BaseUrl is only
            // a legacy/display summary and must never leak into another client.
            candidate = profile.BaseUrl;
        }
        else
        {
            return null;
        }

        if (string.IsNullOrWhiteSpace(candidate))
        {
            return null;
        }

        string normalized = ConnectionEndpointNormalizer.Normalize(cli, candidate);
        if (!Uri.TryCreate(normalized, UriKind.Absolute, out Uri? uri) ||
            (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps) ||
            string.IsNullOrWhiteSpace(uri.Host))
        {
            throw new InvalidOperationException(
                $"连接“{profile.Name}”为 {GetCliDisplayName(cli)} 配置的地址“{normalized}”无效。" +
                "请输入以 http:// 或 https:// 开头的绝对 URL。");
        }

        return normalized;
    }

    private static string GetCliDisplayName(CliKind cli) => cli switch
    {
        CliKind.Codex => "Codex",
        CliKind.ClaudeCode => "Claude Code",
        CliKind.GeminiCli => "Gemini CLI",
        CliKind.GrokCli => "Grok CLI",
        _ => cli.ToString(),
    };

    private static (string FileName, IReadOnlyList<string> Arguments) WrapScriptHost(
        string executablePath,
        IReadOnlyList<string> arguments)
    {
        var extension = Path.GetExtension(executablePath);
        if (extension.Equals(".ps1", StringComparison.OrdinalIgnoreCase))
        {
            return (FindWindowsPowerShell(),
            [
                "-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", executablePath,
                .. arguments
            ]);
        }

        if (extension.Equals(".cmd", StringComparison.OrdinalIgnoreCase) ||
            extension.Equals(".bat", StringComparison.OrdinalIgnoreCase))
        {
            var companionPowerShell = Path.ChangeExtension(executablePath, ".ps1");
            if (File.Exists(companionPowerShell))
            {
                return (FindWindowsPowerShell(),
                [
                    "-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", companionPowerShell,
                    .. arguments
                ]);
            }

            var command = "call " + QuoteForCmd(executablePath);
            if (arguments.Count > 0)
            {
                command += " " + string.Join(' ', arguments.Select(QuoteForCmd));
            }

            return (Environment.GetEnvironmentVariable("COMSPEC") ?? @"C:\Windows\System32\cmd.exe",
                ["/d", "/s", "/c", command]);
        }

        return (executablePath, arguments);
    }

    private static string FindWindowsPowerShell()
    {
        var path = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.Windows),
            "System32",
            "WindowsPowerShell",
            "v1.0",
            "powershell.exe");
        return File.Exists(path) ? path : "powershell.exe";
    }

    private static string QuoteForCmd(string value)
    {
        if (value.Length == 0)
        {
            return "\"\"";
        }

        if (!value.Any(character => char.IsWhiteSpace(character) || "&|<>^()\"".Contains(character)))
        {
            return value;
        }

        return "\"" + value.Replace("\"", "\"\"") + "\"";
    }

    private static string EscapeToml(string value) => value
        .Replace("\\", "\\\\", StringComparison.Ordinal)
        .Replace("\"", "\\\"", StringComparison.Ordinal);
}


