using System.Text.Encodings.Web;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace AiSwitchGui;

internal sealed class CpaSub2ApiConverter
{
    public const string OutputDirectoryName = "sub2api-converted";
    public const string OutputFileName = "sub2api_accounts_import_ui_file.json";

    private static readonly JsonSerializerOptions WriteOptions = new()
    {
        Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
        WriteIndented = true
    };

    /// <summary>
    /// Converts a CPA export into the documented Sub2API data-import format.
    /// When a local backend is unavailable, callers may explicitly choose a
    /// separate fallback output directory.  Existing callers can omit it and
    /// retain the historical source/sub2api-converted location.
    /// </summary>
    public CpaConvertResult ConvertDirectory(string sourceDirectory, string? outputDirectory = null)
    {
        if (string.IsNullOrWhiteSpace(sourceDirectory) || !Directory.Exists(sourceDirectory))
        {
            throw new DirectoryNotFoundException("请选择有效的 CPA JSON 文件夹。");
        }

        string resolvedOutputDirectory = ResolveOutputDirectory(sourceDirectory, outputDirectory);
        Directory.CreateDirectory(resolvedOutputDirectory);

        var inputFiles = Directory
            .EnumerateFiles(sourceDirectory, "*.json", SearchOption.TopDirectoryOnly)
            .Where(path => !IsGeneratedFile(path))
            .OrderBy(path => path, StringComparer.OrdinalIgnoreCase)
            .ToList();

        if (inputFiles.Count == 0)
        {
            throw new InvalidOperationException("所选文件夹内没有可转换的 JSON 文件。");
        }

        return ConvertInputFiles(
            inputFiles,
            sourceDirectory,
            ResolveOutputDirectory(sourceDirectory, outputDirectory));
    }

    /// <summary>
    /// Converts exactly the CPA JSON files the user selected.  The WPF onboarding
    /// page uses this for a single CPA export so unrelated JSON files sitting in
    /// the same folder are never included by surprise.  A Sub2API data export is
    /// deliberately handled by the direct-import path and does not need this
    /// conversion step.
    /// </summary>
    public CpaConvertResult ConvertFiles(
        IEnumerable<string> sourceFiles,
        string? outputDirectory = null)
    {
        ArgumentNullException.ThrowIfNull(sourceFiles);

        var inputFiles = sourceFiles
            .Where(path => !string.IsNullOrWhiteSpace(path))
            .Select(path => Path.GetFullPath(path.Trim()))
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .OrderBy(path => path, StringComparer.OrdinalIgnoreCase)
            .ToList();
        if (inputFiles.Count == 0)
        {
            throw new ArgumentException("请选择至少一个 CPA JSON 文件。", nameof(sourceFiles));
        }

        foreach (string inputFile in inputFiles)
        {
            if (!File.Exists(inputFile))
            {
                throw new FileNotFoundException("所选 CPA JSON 文件不存在。", inputFile);
            }

            if (!Path.GetExtension(inputFile).Equals(".json", StringComparison.OrdinalIgnoreCase))
            {
                throw new ArgumentException("仅支持 JSON 账号文件。", nameof(sourceFiles));
            }
        }

        string sourceDirectory = Path.GetDirectoryName(inputFiles[0])
            ?? throw new ArgumentException("无法确定 CPA JSON 文件所在目录。", nameof(sourceFiles));
        return ConvertInputFiles(
            inputFiles,
            sourceDirectory,
            ResolveOutputDirectory(sourceDirectory, outputDirectory));
    }

    private static CpaConvertResult ConvertInputFiles(
        IReadOnlyList<string> inputFiles,
        string sourceDirectory,
        string resolvedOutputDirectory)
    {
        ArgumentNullException.ThrowIfNull(inputFiles);
        if (inputFiles.Count == 0)
        {
            throw new InvalidOperationException("没有可转换的 CPA JSON 文件。");
        }

        var proxies = new JsonArray();
        var proxyKeys = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        var accounts = new JsonArray();
        var warnings = new List<string>();
        var index = 0;

        foreach (var file in inputFiles)
        {
            var root = ReadJsonRoot(file, warnings);
            if (root is null)
            {
                continue;
            }

            if (TryAppendSub2ApiPayload(root, file, accounts, proxies, proxyKeys, warnings, ref index))
            {
                continue;
            }

            var entries = ReadCandidateEntries(root);
            foreach (var entry in entries)
            {
                var account = BuildAccount(entry, file, ++index, warnings);
                if (account is not null)
                {
                    accounts.Add(account);
                }
            }
        }

        if (accounts.Count == 0)
        {
            throw new InvalidOperationException("没有识别到可导入的账号。请确认 JSON 是 CPA 账号格式，或 Sub2API 导出的 data/accounts 格式。");
        }

        var payload = new JsonObject
        {
            ["type"] = "sub2api-data",
            ["version"] = 1,
            ["exported_at"] = DateTimeOffset.UtcNow.ToString("O"),
            ["proxies"] = proxies,
            ["accounts"] = accounts
        };

        var outputPath = Path.Combine(resolvedOutputDirectory, OutputFileName);
        File.WriteAllText(outputPath, payload.ToJsonString(WriteOptions));

        return new CpaConvertResult(
            sourceDirectory,
            outputPath,
            inputFiles.Count,
            accounts.Count,
            warnings);
    }

    private static string ResolveOutputDirectory(string sourceDirectory, string? outputDirectory)
    {
        if (string.IsNullOrWhiteSpace(outputDirectory))
        {
            return Path.Combine(sourceDirectory, OutputDirectoryName);
        }

        string normalized = Path.GetFullPath(outputDirectory.Trim());
        if (string.IsNullOrWhiteSpace(normalized))
        {
            throw new ArgumentException("请选择有效的导出目录。", nameof(outputDirectory));
        }

        return normalized;
    }

    private static bool IsGeneratedFile(string path)
    {
        var fileName = Path.GetFileName(path);
        return fileName.Equals(OutputFileName, StringComparison.OrdinalIgnoreCase) ||
               fileName.StartsWith("sub2api_", StringComparison.OrdinalIgnoreCase) ||
               fileName.Contains("summary", StringComparison.OrdinalIgnoreCase);
    }

    private static JsonNode? ReadJsonRoot(string file, List<string> warnings)
    {
        try
        {
            using var document = JsonDocument.Parse(File.ReadAllText(file));
            return JsonNode.Parse(document.RootElement.GetRawText());
        }
        catch (Exception ex) when (ex is JsonException or IOException or UnauthorizedAccessException)
        {
            warnings.Add($"{Path.GetFileName(file)} 读取失败：{ex.Message}");
            return null;
        }
    }

    private static List<JsonObject> ReadCandidateEntries(JsonNode root)
    {
        var entries = new List<JsonObject>();
        CollectCandidateEntries(root, entries);
        return entries;
    }

    private static bool TryAppendSub2ApiPayload(
        JsonNode root,
        string file,
        JsonArray accounts,
        JsonArray proxies,
        HashSet<string> proxyKeys,
        List<string> warnings,
        ref int index)
    {
        if (!TryResolveSub2ApiPayload(root, out var payload))
        {
            return false;
        }

        var addedAccounts = 0;
        var addedProxies = 0;

        if (payload.TryGetPropertyValue("proxies", out var proxiesNode) && proxiesNode is JsonArray proxyArray)
        {
            foreach (var proxyNode in proxyArray)
            {
                if (proxyNode is not JsonObject proxy)
                {
                    continue;
                }

                var key = BuildProxyMergeKey(proxy);
                if (!proxyKeys.Add(key))
                {
                    continue;
                }

                proxies.Add(proxy.DeepClone());
                addedProxies++;
            }
        }

        if (payload.TryGetPropertyValue("accounts", out var accountsNode) && accountsNode is JsonArray accountArray)
        {
            foreach (var accountNode in accountArray)
            {
                if (accountNode is not JsonObject account)
                {
                    continue;
                }

                var cloned = account.DeepClone().AsObject();
                if (!cloned.TryGetPropertyValue("name", out var nameNode) ||
                    nameNode is not JsonValue nameValue ||
                    !TryGetString(nameValue, out var name) ||
                    string.IsNullOrWhiteSpace(name))
                {
                    cloned["name"] = $"Merged Sub2API {index + 1:D3}";
                }

                accounts.Add(cloned);
                index++;
                addedAccounts++;
            }
        }

        if (addedAccounts == 0 && addedProxies == 0)
        {
            warnings.Add($"{Path.GetFileName(file)} 看起来是 Sub2API 数据，但没有可合并的 accounts/proxies。");
        }

        return true;
    }

    private static bool TryResolveSub2ApiPayload(JsonNode root, out JsonObject payload)
    {
        payload = null!;
        var candidate = root as JsonObject;
        if (candidate is null)
        {
            return false;
        }

        if (candidate.TryGetPropertyValue("data", out var dataNode) && dataNode is JsonObject dataObj)
        {
            candidate = dataObj;
        }

        var type = FindString(candidate, ["type"]);
        var hasAccounts = candidate.TryGetPropertyValue("accounts", out var accountsNode) && accountsNode is JsonArray;
        var hasProxies = candidate.TryGetPropertyValue("proxies", out var proxiesNode) && proxiesNode is JsonArray;
        var looksLikeSub2Api = string.Equals(type, "sub2api-data", StringComparison.OrdinalIgnoreCase) ||
                               string.Equals(type, "sub2api-bundle", StringComparison.OrdinalIgnoreCase) ||
                               (hasAccounts && hasProxies) ||
                               (hasAccounts && !LooksLikeAccountObject(candidate));

        if (!looksLikeSub2Api)
        {
            return false;
        }

        payload = candidate;
        return true;
    }

    private static string BuildProxyMergeKey(JsonObject proxy)
    {
        var proxyKey = FindString(proxy, ["proxy_key"]);
        if (!string.IsNullOrWhiteSpace(proxyKey))
        {
            return proxyKey;
        }

        return string.Join("|",
            FindString(proxy, ["protocol"]) ?? string.Empty,
            FindString(proxy, ["host"]) ?? string.Empty,
            FindString(proxy, ["port"]) ?? string.Empty,
            FindString(proxy, ["username"]) ?? string.Empty,
            FindString(proxy, ["password"]) ?? string.Empty);
    }

    private static void CollectCandidateEntries(JsonNode? node, List<JsonObject> entries)
    {
        switch (node)
        {
            case JsonArray array:
                foreach (var item in array)
                {
                    CollectCandidateEntries(item, entries);
                }
                break;

            case JsonObject obj:
                if (LooksLikeAccountObject(obj))
                {
                    entries.Add(obj);
                    return;
                }

                foreach (var propertyName in new[] { "accounts", "items", "data", "contents", "sessions" })
                {
                    if (!obj.TryGetPropertyValue(propertyName, out var child) || child is null)
                    {
                        continue;
                    }

                    if (child is JsonArray childArray)
                    {
                        foreach (var item in childArray)
                        {
                            if (item is JsonValue value && TryGetString(value, out var text) && LooksLikeJson(text))
                            {
                                CollectCandidateEntries(JsonNode.Parse(text), entries);
                                continue;
                            }

                            CollectCandidateEntries(item, entries);
                        }
                    }
                    else
                    {
                        CollectCandidateEntries(child, entries);
                    }
                }
                break;
        }
    }

    private static bool LooksLikeAccountObject(JsonObject obj)
    {
        return !string.IsNullOrWhiteSpace(FindString(obj, ["access_token"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["accessToken"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["token"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["tokens", "access_token"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["tokens", "accessToken"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["refresh_token"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["refreshToken"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["tokens", "refresh_token"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["tokens", "refreshToken"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["id_token"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["idToken"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["tokens", "id_token"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["tokens", "idToken"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["credentials", "access_token"])) ||
               !string.IsNullOrWhiteSpace(FindString(obj, ["credentials", "accessToken"]));
    }

    private static JsonObject? BuildAccount(JsonObject source, string file, int index, List<string> warnings)
    {
        var accessToken = FirstNonEmpty(
            FindString(source, ["access_token"]),
            FindString(source, ["accessToken"]),
            FindString(source, ["token"]),
            FindString(source, ["tokens", "access_token"]),
            FindString(source, ["tokens", "accessToken"]),
            FindString(source, ["credentials", "access_token"]),
            FindString(source, ["credentials", "accessToken"]));

        var refreshToken = FirstNonEmpty(
            FindString(source, ["refresh_token"]),
            FindString(source, ["refreshToken"]),
            FindString(source, ["tokens", "refresh_token"]),
            FindString(source, ["tokens", "refreshToken"]),
            FindString(source, ["credentials", "refresh_token"]),
            FindString(source, ["credentials", "refreshToken"]));

        var idToken = FirstNonEmpty(
            FindString(source, ["id_token"]),
            FindString(source, ["idToken"]),
            FindString(source, ["tokens", "id_token"]),
            FindString(source, ["tokens", "idToken"]),
            FindString(source, ["credentials", "id_token"]),
            FindString(source, ["credentials", "idToken"]));

        if (string.IsNullOrWhiteSpace(accessToken) &&
            string.IsNullOrWhiteSpace(refreshToken) &&
            string.IsNullOrWhiteSpace(idToken))
        {
            warnings.Add($"{Path.GetFileName(file)} 第 {index} 条缺少 token，已跳过。");
            return null;
        }

        var email = FirstNonEmpty(
            FindString(source, ["email"]),
            FindString(source, ["user", "email"]),
            FindString(source, ["profile", "email"]),
            FindString(source, ["credentials", "email"]));

        var accountId = FirstNonEmpty(
            FindString(source, ["account_id"]),
            FindString(source, ["accountId"]),
            FindString(source, ["chatgpt_account_id"]),
            FindString(source, ["chatgptAccountId"]),
            FindString(source, ["account", "id"]),
            FindString(source, ["credentials", "chatgpt_account_id"]),
            FindString(source, ["credentials", "account_id"]));

        var expiresText = FirstNonEmpty(
            FindString(source, ["expired"]),
            FindString(source, ["expires_at"]),
            FindString(source, ["expiresAt"]),
            FindString(source, ["token_expires_at"]),
            FindString(source, ["tokenExpiresAt"]),
            FindString(source, ["credentials", "expires_at"]));

        var lastRefresh = FirstNonEmpty(
            FindString(source, ["last_refresh"]),
            FindString(source, ["lastRefresh"]),
            FindString(source, ["credentials", "last_refresh"]));

        var credentials = new JsonObject();
        AddIfPresent(credentials, "access_token", accessToken);
        AddIfPresent(credentials, "refresh_token", refreshToken);
        AddIfPresent(credentials, "id_token", idToken);
        AddIfPresent(credentials, "email", email);
        AddIfPresent(credentials, "chatgpt_account_id", accountId);
        AddIfPresent(credentials, "account_id", accountId);
        AddIfPresent(credentials, "last_refresh", lastRefresh);
        AddIfPresent(credentials, "expires_at", expiresText);

        var account = new JsonObject
        {
            ["name"] = BuildAccountName(index, email),
            ["notes"] = $"Converted from CPA file: {Path.GetFileName(file)}",
            ["platform"] = "openai",
            ["type"] = "oauth",
            ["credentials"] = credentials,
            ["extra"] = new JsonObject
            {
                ["import_source"] = "cpa_json",
                ["original_file"] = Path.GetFileName(file),
                ["original_type"] = FirstNonEmpty(FindString(source, ["type"]), "cpa")
            },
            ["concurrency"] = 3,
            ["priority"] = 50,
            ["rate_multiplier"] = 1.0,
            ["auto_pause_on_expired"] = true
        };

        if (TryResolveUnixTime(source, expiresText, out var expiresAt))
        {
            account["expires_at"] = expiresAt;
        }

        return account;
    }

    private static string BuildAccountName(int index, string? email)
    {
        var suffix = string.IsNullOrWhiteSpace(email) ? $"account-{index:D3}" : email.Trim();
        return $"CPA Codex {index:D3} {suffix}";
    }

    private static bool TryResolveUnixTime(JsonObject source, string? value, out long unixTime)
    {
        unixTime = 0;

        foreach (var path in new[] { new[] { "expires_at" }, ["expiresAt"], ["expired"] })
        {
            if (TryFindNumber(source, path, out unixTime))
            {
                return unixTime > 0;
            }
        }

        if (string.IsNullOrWhiteSpace(value))
        {
            return false;
        }

        if (long.TryParse(value, out unixTime))
        {
            if (unixTime > 10_000_000_000)
            {
                unixTime /= 1000;
            }
            return unixTime > 0;
        }

        if (DateTimeOffset.TryParse(value, out var parsed))
        {
            unixTime = parsed.ToUnixTimeSeconds();
            return true;
        }

        return false;
    }

    private static string? FindString(JsonObject obj, params string[][] paths)
    {
        foreach (var path in paths)
        {
            var current = (JsonNode?)obj;
            foreach (var segment in path)
            {
                if (current is not JsonObject currentObj ||
                    !TryGetPropertyCaseInsensitive(currentObj, segment, out current))
                {
                    current = null;
                    break;
                }
            }

            if (current is JsonValue value && TryGetString(value, out var text) && !string.IsNullOrWhiteSpace(text))
            {
                return text.Trim();
            }
        }

        return null;
    }

    private static bool TryFindNumber(JsonObject obj, string[] path, out long value)
    {
        value = 0;
        var current = (JsonNode?)obj;
        foreach (var segment in path)
        {
            if (current is not JsonObject currentObj ||
                !TryGetPropertyCaseInsensitive(currentObj, segment, out current))
            {
                return false;
            }
        }

        if (current is null)
        {
            return false;
        }

        if (current.GetValueKind() == JsonValueKind.Number && current.GetValue<long>() is var number)
        {
            value = number;
            return true;
        }

        if (current is JsonValue jsonValue && TryGetString(jsonValue, out var text) && long.TryParse(text, out value))
        {
            return true;
        }

        return false;
    }

    private static bool TryGetPropertyCaseInsensitive(JsonObject obj, string name, out JsonNode? value)
    {
        if (obj.TryGetPropertyValue(name, out value))
        {
            return true;
        }

        foreach (var property in obj)
        {
            if (property.Key.Equals(name, StringComparison.OrdinalIgnoreCase))
            {
                value = property.Value;
                return true;
            }
        }

        value = null;
        return false;
    }

    private static bool TryGetString(JsonValue value, out string text)
    {
        text = string.Empty;
        try
        {
            if (value.GetValueKind() == JsonValueKind.String)
            {
                text = value.GetValue<string>() ?? string.Empty;
                return true;
            }

            text = value.ToJsonString().Trim('"');
            return !string.IsNullOrWhiteSpace(text);
        }
        catch
        {
            return false;
        }
    }

    private static void AddIfPresent(JsonObject obj, string key, string? value)
    {
        if (!string.IsNullOrWhiteSpace(value))
        {
            obj[key] = value.Trim();
        }
    }

    private static string? FirstNonEmpty(params string?[] values)
    {
        return values.FirstOrDefault(value => !string.IsNullOrWhiteSpace(value));
    }

    private static bool LooksLikeJson(string value)
    {
        var trimmed = value.TrimStart();
        return trimmed.StartsWith('{') || trimmed.StartsWith('[');
    }
}

internal sealed record CpaConvertResult(
    string SourceDirectory,
    string OutputPath,
    int InputFileCount,
    int AccountCount,
    IReadOnlyList<string> Warnings);
