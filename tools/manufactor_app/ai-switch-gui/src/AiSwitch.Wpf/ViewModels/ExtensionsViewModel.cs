using System.Collections.ObjectModel;
using System.IO.Compression;
using System.Security.Cryptography;
using System.Text;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using LanAi.Workspace.Core;
using LanAi.Workspace.Infrastructure;
using Microsoft.Win32;

namespace LanAi.Workspace.Wpf.ViewModels;

public partial class ExtensionsViewModel : PageViewModel, IDisposable
{
    private readonly IWorkspaceFeatureStore _store;
    private readonly IOfficialClientExtensionSynchronizer _synchronizer;
    private readonly AppDataPaths _paths;
    private readonly OfficialMcpImportService _mcpImporter;
    private readonly SemaphoreSlim _mutationGate = new(1, 1);
    private WorkspaceFeatureState _state = new();
    private bool _disposed;

    public ExtensionsViewModel(
        IWorkspaceFeatureStore store,
        IOfficialClientExtensionSynchronizer synchronizer,
        AppDataPaths paths)
        : base("扩展中心", "统一管理三个官方客户端的 MCP、提示词和 Skills。")
    {
        _store = store ?? throw new ArgumentNullException(nameof(store));
        _synchronizer = synchronizer ?? throw new ArgumentNullException(nameof(synchronizer));
        _paths = paths ?? throw new ArgumentNullException(nameof(paths));
        _mcpImporter = new OfficialMcpImportService(paths);
        McpServers = [];
        PromptPresets = [];
        Skills = [];
        McpTransportOptions = Enum.GetNames<McpTransportKind>();
    }

    public ObservableCollection<McpServerItemViewModel> McpServers { get; }

    public ObservableCollection<PromptPresetItemViewModel> PromptPresets { get; }

    public ObservableCollection<SkillItemViewModel> Skills { get; }

    public IReadOnlyList<string> McpTransportOptions { get; }

    [ObservableProperty]
    private bool isBusy;

    [ObservableProperty]
    private string status = "正在读取扩展配置…";

    [ObservableProperty]
    private bool isMcpEditorOpen;

    [ObservableProperty]
    private bool isPromptEditorOpen;

    [ObservableProperty]
    private bool isSkillEditorOpen;

    [ObservableProperty]
    private string mcpId = string.Empty;

    [ObservableProperty]
    private string mcpName = string.Empty;

    [ObservableProperty]
    private string mcpDescription = string.Empty;

    [ObservableProperty]
    private string selectedMcpTransport = nameof(McpTransportKind.Stdio);

    [ObservableProperty]
    private string mcpCommandOrUrl = string.Empty;

    [ObservableProperty]
    private string mcpArguments = string.Empty;

    [ObservableProperty]
    private string mcpEnvironment = string.Empty;

    [ObservableProperty]
    private string mcpHeaders = string.Empty;

    [ObservableProperty]
    private bool mcpCodex;

    [ObservableProperty]
    private bool mcpClaude;

    [ObservableProperty]
    private bool mcpGemini;

    [ObservableProperty]
    private string? editingMcpOriginalId;

    [ObservableProperty]
    private string promptId = string.Empty;

    [ObservableProperty]
    private string promptName = string.Empty;

    [ObservableProperty]
    private string promptMarkdown = string.Empty;

    [ObservableProperty]
    private bool promptCodex;

    [ObservableProperty]
    private bool promptClaude;

    [ObservableProperty]
    private bool promptGemini;

    [ObservableProperty]
    private string? editingPromptOriginalId;

    [ObservableProperty]
    private SkillItemViewModel? editingSkill;

    [ObservableProperty]
    private bool skillCodex;

    [ObservableProperty]
    private bool skillClaude;

    [ObservableProperty]
    private bool skillGemini;

    public async Task InitializeAsync(CancellationToken cancellationToken = default)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        IsBusy = true;
        try
        {
            _state = await _store.LoadAsync(cancellationToken).ConfigureAwait(true);
            WorkspaceFeatureState imported = await ImportInitialPromptFilesAsync(_state, cancellationToken).ConfigureAwait(true);
            if (!ReferenceEquals(imported, _state))
            {
                _state = imported;
                await _store.SaveAsync(_state, cancellationToken).ConfigureAwait(true);
            }
            RebuildCollections();
            Status = $"MCP {McpServers.Count} · 提示词 {PromptPresets.Count} · Skills {Skills.Count}";
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidDataException)
        {
            Status = $"读取扩展配置失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsBusy = false;
        }
    }

    [RelayCommand]
    private void NewMcp()
    {
        EditingMcpOriginalId = null;
        McpId = string.Empty;
        McpName = string.Empty;
        McpDescription = string.Empty;
        SelectedMcpTransport = nameof(McpTransportKind.Stdio);
        McpCommandOrUrl = string.Empty;
        McpArguments = string.Empty;
        McpEnvironment = string.Empty;
        McpHeaders = string.Empty;
        McpCodex = McpClaude = McpGemini = true;
        IsMcpEditorOpen = true;
    }

    [RelayCommand]
    private async Task ImportMcpFromClientsAsync()
    {
        if (IsBusy) return;
        IsBusy = true;
        try
        {
            McpImportResult result = await _mcpImporter.ImportAllAsync(_state).ConfigureAwait(true);
            _state = result.State;
            await _store.SaveAsync(_state).ConfigureAwait(true);
            RebuildCollections();
            Status = result.Warnings.Count == 0
                ? $"已从官方客户端导入 {result.ImportedCount} 个新 MCP。"
                : $"已导入 {result.ImportedCount} 个新 MCP；{result.Warnings.Count} 个敏感或损坏字段已跳过。";
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidDataException or InvalidOperationException)
        {
            Status = $"导入 MCP 失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsBusy = false;
        }
    }

    [RelayCommand]
    private void EditMcp(McpServerItemViewModel? item)
    {
        if (item is null) return;
        McpServerDefinition definition = item.Definition;
        EditingMcpOriginalId = definition.Id;
        McpId = definition.Id;
        McpName = definition.Name;
        McpDescription = definition.Description ?? string.Empty;
        SelectedMcpTransport = definition.Transport.ToString();
        McpCommandOrUrl = definition.Transport == McpTransportKind.Stdio
            ? definition.Command ?? string.Empty
            : definition.Url ?? string.Empty;
        McpArguments = string.Join(Environment.NewLine, definition.Arguments);
        McpEnvironment = FormatMap(definition.Environment);
        McpHeaders = FormatMap(definition.Headers);
        McpCodex = definition.Targets.HasFlag(ManagedClientTargets.Codex);
        McpClaude = definition.Targets.HasFlag(ManagedClientTargets.Claude);
        McpGemini = definition.Targets.HasFlag(ManagedClientTargets.Gemini);
        IsMcpEditorOpen = true;
    }

    [RelayCommand]
    private void CancelMcpEdit() => IsMcpEditorOpen = false;

    [RelayCommand]
    private async Task SaveMcpAsync()
    {
        if (!Enum.TryParse(SelectedMcpTransport, true, out McpTransportKind transport))
        {
            Status = "请选择 MCP 传输类型。";
            return;
        }

        string id = McpId.Trim();
        string name = McpName.Trim();
        if (string.IsNullOrWhiteSpace(id) || string.IsNullOrWhiteSpace(name))
        {
            Status = "请填写 MCP ID 和名称。";
            return;
        }

        var definition = new McpServerDefinition
        {
            Id = id,
            Name = name,
            Description = NullIfWhiteSpace(McpDescription),
            Transport = transport,
            Command = transport == McpTransportKind.Stdio ? NullIfWhiteSpace(McpCommandOrUrl) : null,
            Url = transport == McpTransportKind.Stdio ? null : NullIfWhiteSpace(McpCommandOrUrl),
            Arguments = SplitLines(McpArguments),
            Environment = ParseMap(McpEnvironment),
            Headers = ParseMap(McpHeaders),
            Targets = CreateTargets(McpCodex, McpClaude, McpGemini),
        };
        List<McpServerDefinition> items = _state.McpServers.ToList();
        if (!string.IsNullOrWhiteSpace(EditingMcpOriginalId))
        {
            items.RemoveAll(item => string.Equals(item.Id, EditingMcpOriginalId, StringComparison.OrdinalIgnoreCase));
        }

        items.RemoveAll(item => string.Equals(item.Id, id, StringComparison.OrdinalIgnoreCase));
        items.Add(definition);
        await SaveStateAsync(_state with { McpServers = items }).ConfigureAwait(true);
        IsMcpEditorOpen = false;
    }

    [RelayCommand]
    private async Task DeleteMcpAsync(McpServerItemViewModel? item)
    {
        if (item is null || !ConfirmDelete($"删除 MCP“{item.Name}”？它会从三个客户端的受管配置中移除。")) return;
        await SaveStateAsync(_state with
        {
            McpServers = _state.McpServers
                .Where(server => !string.Equals(server.Id, item.Id, StringComparison.OrdinalIgnoreCase))
                .ToArray(),
        }).ConfigureAwait(true);
    }

    [RelayCommand]
    private void NewPrompt()
    {
        EditingPromptOriginalId = null;
        PromptId = string.Empty;
        PromptName = string.Empty;
        PromptMarkdown = string.Empty;
        PromptCodex = PromptClaude = PromptGemini = true;
        IsPromptEditorOpen = true;
    }

    [RelayCommand]
    private void EditPrompt(PromptPresetItemViewModel? item)
    {
        if (item is null) return;
        PromptPresetDefinition definition = item.Definition;
        EditingPromptOriginalId = definition.Id;
        PromptId = definition.Id;
        PromptName = definition.Name;
        PromptMarkdown = definition.Markdown;
        PromptCodex = definition.Targets.HasFlag(ManagedClientTargets.Codex);
        PromptClaude = definition.Targets.HasFlag(ManagedClientTargets.Claude);
        PromptGemini = definition.Targets.HasFlag(ManagedClientTargets.Gemini);
        IsPromptEditorOpen = true;
    }

    [RelayCommand]
    private void CancelPromptEdit() => IsPromptEditorOpen = false;

    [RelayCommand]
    private async Task SavePromptAsync()
    {
        string id = PromptId.Trim();
        string name = PromptName.Trim();
        if (string.IsNullOrWhiteSpace(id) || string.IsNullOrWhiteSpace(name) || string.IsNullOrWhiteSpace(PromptMarkdown))
        {
            Status = "请填写提示词 ID、名称和内容。";
            return;
        }

        ManagedClientTargets targets = CreateTargets(PromptCodex, PromptClaude, PromptGemini);
        var definition = new PromptPresetDefinition
        {
            Id = id,
            Name = name,
            Markdown = PromptMarkdown.Trim(),
            Targets = targets,
        };
        WorkspaceFeatureState captured = await CaptureLivePromptContentAsync(_state, targets).ConfigureAwait(true);
        List<PromptPresetDefinition> items = captured.PromptPresets
            .Select(item => (item.Targets & targets) != ManagedClientTargets.None
                ? item with { Targets = item.Targets & ~targets }
                : item)
            .Where(item => item.Targets != ManagedClientTargets.None ||
                           !string.Equals(item.Id, EditingPromptOriginalId, StringComparison.OrdinalIgnoreCase))
            .ToList();
        if (!string.IsNullOrWhiteSpace(EditingPromptOriginalId))
        {
            items.RemoveAll(item => string.Equals(item.Id, EditingPromptOriginalId, StringComparison.OrdinalIgnoreCase));
        }

        items.RemoveAll(item => string.Equals(item.Id, id, StringComparison.OrdinalIgnoreCase));
        items.Add(definition);
        await SaveStateAsync(captured with { PromptPresets = items }).ConfigureAwait(true);
        IsPromptEditorOpen = false;
    }

    [RelayCommand]
    private async Task DeletePromptAsync(PromptPresetItemViewModel? item)
    {
        if (item is null) return;
        if (item.Definition.Targets != ManagedClientTargets.None)
        {
            Status = "已启用的提示词不能删除，请先编辑并取消所有客户端。";
            return;
        }

        if (!ConfirmDelete($"删除提示词“{item.Name}”？")) return;
        await SaveStateAsync(_state with
        {
            PromptPresets = _state.PromptPresets
                .Where(prompt => !string.Equals(prompt.Id, item.Id, StringComparison.OrdinalIgnoreCase))
                .ToArray(),
        }).ConfigureAwait(true);
    }

    [RelayCommand]
    private async Task ImportSkillAsync()
    {
        var dialog = new OpenFolderDialog
        {
            Title = "选择包含 SKILL.md 的技能目录",
            Multiselect = false,
        };
        if (dialog.ShowDialog() != true) return;
        string source = Path.GetFullPath(dialog.FolderName);
        if (!File.Exists(Path.Combine(source, "SKILL.md")))
        {
            Status = "所选目录没有 SKILL.md。";
            return;
        }

        string name = Path.GetFileName(source.TrimEnd(Path.DirectorySeparatorChar));
        string id = CreateStableId(name);
        string storageName = id;
        string destination = SafeChildPath(_paths.ManagedSkillsDirectory, storageName);
        ManagedSkillDefinition? existing = _state.Skills.FirstOrDefault(skill =>
            string.Equals(skill.Id, id, StringComparison.OrdinalIgnoreCase));
        if (existing is not null && !ConfirmDelete($"技能“{name}”已存在，是否用所选目录替换？")) return;

        await _mutationGate.WaitAsync().ConfigureAwait(true);
        IsBusy = true;
        try
        {
            Directory.CreateDirectory(_paths.ManagedSkillsDirectory);
            if (Directory.Exists(destination))
            {
                CreateSkillBackup(destination, id);
                Directory.Delete(destination, recursive: true);
            }

            CopyDirectory(source, destination);
            var definition = new ManagedSkillDefinition
            {
                Id = id,
                Name = name,
                Description = ReadSkillDescription(Path.Combine(destination, "SKILL.md")),
                StorageDirectoryName = storageName,
                SourceLabel = "本地导入",
                ContentSha256 = ComputeDirectoryHash(destination),
                Targets = ManagedClientTargets.All,
            };
            List<ManagedSkillDefinition> skills = _state.Skills
                .Where(skill => !string.Equals(skill.Id, id, StringComparison.OrdinalIgnoreCase))
                .Append(definition)
                .ToList();
            await SaveStateCoreAsync(_state with { Skills = skills }).ConfigureAwait(true);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidOperationException)
        {
            Status = $"导入技能失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsBusy = false;
            _mutationGate.Release();
        }
    }

    [RelayCommand]
    private void EditSkill(SkillItemViewModel? item)
    {
        if (item is null) return;
        EditingSkill = item;
        SkillCodex = item.Definition.Targets.HasFlag(ManagedClientTargets.Codex);
        SkillClaude = item.Definition.Targets.HasFlag(ManagedClientTargets.Claude);
        SkillGemini = item.Definition.Targets.HasFlag(ManagedClientTargets.Gemini);
        IsSkillEditorOpen = true;
    }

    [RelayCommand]
    private void CancelSkillEdit() => IsSkillEditorOpen = false;

    [RelayCommand]
    private async Task SaveSkillAsync()
    {
        if (EditingSkill is null) return;
        string id = EditingSkill.Id;
        ManagedClientTargets targets = CreateTargets(SkillCodex, SkillClaude, SkillGemini);
        ManagedSkillDefinition[] skills = _state.Skills
            .Select(skill => string.Equals(skill.Id, id, StringComparison.OrdinalIgnoreCase)
                ? skill with { Targets = targets }
                : skill)
            .ToArray();
        await SaveStateAsync(_state with { Skills = skills }).ConfigureAwait(true);
        IsSkillEditorOpen = false;
    }

    [RelayCommand]
    private async Task DeleteSkillAsync(SkillItemViewModel? item)
    {
        if (item is null || !ConfirmDelete($"卸载技能“{item.Name}”？删除前会保留 ZIP 备份。")) return;
        ManagedSkillDefinition definition = item.Definition;
        string source = SafeChildPath(_paths.ManagedSkillsDirectory, definition.StorageDirectoryName);
        if (Directory.Exists(source))
        {
            CreateSkillBackup(source, definition.Id);
        }

        await SaveStateAsync(_state with
        {
            Skills = _state.Skills
                .Where(skill => !string.Equals(skill.Id, definition.Id, StringComparison.OrdinalIgnoreCase))
                .ToArray(),
        }).ConfigureAwait(true);
        if (Directory.Exists(source)) Directory.Delete(source, recursive: true);
    }

    [RelayCommand]
    private async Task RestoreSkillBackupAsync()
    {
        var dialog = new OpenFileDialog
        {
            Title = "选择 Skill ZIP 备份",
            Filter = "Skill 备份|skill-*.zip|ZIP 文件|*.zip",
            InitialDirectory = _paths.BackupsDirectory,
        };
        if (dialog.ShowDialog() != true) return;

        await _mutationGate.WaitAsync().ConfigureAwait(true);
        IsBusy = true;
        string temporary = Path.Combine(_paths.AppDataRoot, $"skill-restore-{Guid.NewGuid():N}.tmp");
        try
        {
            Directory.CreateDirectory(temporary);
            using (ZipArchive archive = ZipFile.OpenRead(dialog.FileName))
            {
                string root = Path.GetFullPath(temporary).TrimEnd(Path.DirectorySeparatorChar) + Path.DirectorySeparatorChar;
                foreach (ZipArchiveEntry entry in archive.Entries)
                {
                    string destinationPath = Path.GetFullPath(Path.Combine(temporary, entry.FullName));
                    if (!destinationPath.StartsWith(root, StringComparison.OrdinalIgnoreCase))
                        throw new InvalidDataException("Skill 备份包含越界路径。");
                    if (string.IsNullOrEmpty(entry.Name)) Directory.CreateDirectory(destinationPath);
                    else
                    {
                        Directory.CreateDirectory(Path.GetDirectoryName(destinationPath)!);
                        entry.ExtractToFile(destinationPath, overwrite: true);
                    }
                }
            }

            string skillFile = Path.Combine(temporary, "SKILL.md");
            if (!File.Exists(skillFile)) throw new InvalidDataException("Skill 备份中没有 SKILL.md。");
            string name = ReadSkillName(skillFile) ?? Path.GetFileNameWithoutExtension(dialog.FileName);
            string id = CreateStableId(name);
            string destination = SafeChildPath(_paths.ManagedSkillsDirectory, id);
            if (Directory.Exists(destination))
            {
                CreateSkillBackup(destination, id);
                Directory.Delete(destination, recursive: true);
            }

            CopyDirectory(temporary, destination);
            var restored = new ManagedSkillDefinition
            {
                Id = id,
                Name = name,
                Description = ReadSkillDescription(Path.Combine(destination, "SKILL.md")),
                StorageDirectoryName = id,
                SourceLabel = "备份恢复",
                ContentSha256 = ComputeDirectoryHash(destination),
                Targets = ManagedClientTargets.All,
            };
            await SaveStateCoreAsync(_state with
            {
                Skills = _state.Skills.Where(item => !string.Equals(item.Id, id, StringComparison.OrdinalIgnoreCase)).Append(restored).ToArray(),
            }).ConfigureAwait(true);
            Status = $"已恢复 Skill：{name}";
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidDataException or InvalidOperationException)
        {
            Status = $"恢复 Skill 失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            if (Directory.Exists(temporary)) Directory.Delete(temporary, recursive: true);
            IsBusy = false;
            _mutationGate.Release();
        }
    }

    private async Task SaveStateAsync(WorkspaceFeatureState next)
    {
        await _mutationGate.WaitAsync().ConfigureAwait(true);
        IsBusy = true;
        try
        {
            await SaveStateCoreAsync(next).ConfigureAwait(true);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException or InvalidDataException or InvalidOperationException)
        {
            Status = $"保存失败：{Sanitize(exception.Message)}";
        }
        finally
        {
            IsBusy = false;
            _mutationGate.Release();
        }
    }

    private async Task SaveStateCoreAsync(WorkspaceFeatureState next)
    {
        WorkspaceFeatureState previous = _state;
        try
        {
            await _synchronizer.SynchronizeAsync(previous, next).ConfigureAwait(true);
        }
        catch
        {
            try { await _synchronizer.SynchronizeAsync(next, previous).ConfigureAwait(true); } catch { }
            throw;
        }
        try
        {
            await _store.SaveAsync(next).ConfigureAwait(true);
        }
        catch
        {
            await _synchronizer.SynchronizeAsync(next, previous).ConfigureAwait(true);
            throw;
        }

        _state = await _store.LoadAsync().ConfigureAwait(true);
        RebuildCollections();
        Status = $"已同步 · MCP {McpServers.Count} · 提示词 {PromptPresets.Count} · Skills {Skills.Count}";
    }

    private void RebuildCollections()
    {
        McpServers.Clear();
        foreach (McpServerDefinition definition in _state.McpServers) McpServers.Add(new McpServerItemViewModel(definition));
        PromptPresets.Clear();
        foreach (PromptPresetDefinition definition in _state.PromptPresets) PromptPresets.Add(new PromptPresetItemViewModel(definition));
        Skills.Clear();
        foreach (ManagedSkillDefinition definition in _state.Skills) Skills.Add(new SkillItemViewModel(definition));
    }

    private static ManagedClientTargets CreateTargets(bool codex, bool claude, bool gemini)
    {
        ManagedClientTargets targets = ManagedClientTargets.None;
        if (codex) targets |= ManagedClientTargets.Codex;
        if (claude) targets |= ManagedClientTargets.Claude;
        if (gemini) targets |= ManagedClientTargets.Gemini;
        return targets;
    }

    private static IReadOnlyList<string> SplitLines(string value) => value
        .Split(['\r', '\n'], StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);

    private static IReadOnlyDictionary<string, string> ParseMap(string value)
    {
        var result = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        foreach (string line in SplitLines(value))
        {
            int separator = line.IndexOf('=');
            if (separator <= 0) throw new InvalidDataException($"键值行缺少等号：{line}");
            result[line[..separator].Trim()] = line[(separator + 1)..].Trim();
        }

        return result;
    }

    private static string FormatMap(IReadOnlyDictionary<string, string> values) =>
        string.Join(Environment.NewLine, values.Select(pair => $"{pair.Key}={pair.Value}"));

    private static string CreateStableId(string value)
    {
        string slug = new(value.ToLowerInvariant().Select(character =>
            char.IsLetterOrDigit(character) ? character : '-').ToArray());
        slug = string.Join('-', slug.Split('-', StringSplitOptions.RemoveEmptyEntries));
        return string.IsNullOrWhiteSpace(slug) ? Guid.NewGuid().ToString("N") : slug;
    }

    private static string? ReadSkillDescription(string skillFile)
    {
        foreach (string line in File.ReadLines(skillFile).Take(40))
        {
            if (line.StartsWith("description:", StringComparison.OrdinalIgnoreCase))
            {
                return line["description:".Length..].Trim().Trim('"', '\'');
            }
        }

        return null;
    }

    private static string? ReadSkillName(string skillFile)
    {
        foreach (string line in File.ReadLines(skillFile).Take(40))
        {
            if (line.StartsWith("name:", StringComparison.OrdinalIgnoreCase))
                return NullIfWhiteSpace(line["name:".Length..].Trim().Trim('"', '\''));
        }

        return null;
    }

    private string CreateSkillBackup(string source, string id)
    {
        Directory.CreateDirectory(_paths.BackupsDirectory);
        string backup = Path.Combine(_paths.BackupsDirectory, $"skill-{id}-{DateTime.UtcNow:yyyyMMdd-HHmmssfff}.zip");
        ZipFile.CreateFromDirectory(source, backup, CompressionLevel.Optimal, includeBaseDirectory: false);
        foreach (FileInfo stale in new DirectoryInfo(_paths.BackupsDirectory)
                     .EnumerateFiles($"skill-{id}-*.zip")
                     .OrderByDescending(file => file.CreationTimeUtc)
                     .Skip(20))
        {
            try { stale.Delete(); } catch { }
        }

        return backup;
    }

    private static string ComputeDirectoryHash(string directory)
    {
        using var hash = IncrementalHash.CreateHash(HashAlgorithmName.SHA256);
        foreach (string file in Directory.EnumerateFiles(directory, "*", SearchOption.AllDirectories)
                     .OrderBy(path => path, StringComparer.OrdinalIgnoreCase))
        {
            string relative = Path.GetRelativePath(directory, file).Replace('\\', '/');
            hash.AppendData(Encoding.UTF8.GetBytes(relative));
            using FileStream stream = File.OpenRead(file);
            byte[] buffer = new byte[16 * 1024];
            int read;
            while ((read = stream.Read(buffer, 0, buffer.Length)) > 0) hash.AppendData(buffer, 0, read);
        }

        return Convert.ToHexString(hash.GetHashAndReset());
    }

    private static string SafeChildPath(string parent, string child)
    {
        string parentFull = Path.GetFullPath(parent).TrimEnd(Path.DirectorySeparatorChar) + Path.DirectorySeparatorChar;
        string childFull = Path.GetFullPath(Path.Combine(parentFull, child));
        if (!childFull.StartsWith(parentFull, StringComparison.OrdinalIgnoreCase))
            throw new InvalidOperationException("技能路径超出允许范围。 ");
        return childFull;
    }

    private static void CopyDirectory(string source, string destination)
    {
        Directory.CreateDirectory(destination);
        foreach (string file in Directory.EnumerateFiles(source))
            File.Copy(file, Path.Combine(destination, Path.GetFileName(file)), overwrite: true);
        foreach (string directory in Directory.EnumerateDirectories(source))
            CopyDirectory(directory, Path.Combine(destination, Path.GetFileName(directory)));
    }

    private static bool ConfirmDelete(string message) => System.Windows.MessageBox.Show(
        message,
        "共飞AI工作台",
        System.Windows.MessageBoxButton.YesNo,
        System.Windows.MessageBoxImage.Warning) == System.Windows.MessageBoxResult.Yes;

    private static string? NullIfWhiteSpace(string? value) => string.IsNullOrWhiteSpace(value) ? null : value.Trim();

    private static string Sanitize(string value) => value.Replace("\r", " ", StringComparison.Ordinal).Replace("\n", " ", StringComparison.Ordinal).Trim();

    private async Task<WorkspaceFeatureState> ImportInitialPromptFilesAsync(
        WorkspaceFeatureState state,
        CancellationToken cancellationToken)
    {
        var prompts = state.PromptPresets.ToList();
        bool changed = false;
        foreach ((ManagedClientTargets target, string path, string label) in PromptFiles())
        {
            if (prompts.Any(prompt => (prompt.Targets & target) != ManagedClientTargets.None) || !File.Exists(path))
            {
                continue;
            }

            string content = await File.ReadAllTextAsync(path, cancellationToken).ConfigureAwait(true);
            if (string.IsNullOrWhiteSpace(content)) continue;
            PromptPresetDefinition? same = prompts.FirstOrDefault(prompt =>
                string.Equals(prompt.Markdown.Trim(), content.Trim(), StringComparison.Ordinal));
            if (same is not null)
            {
                prompts.Remove(same);
                prompts.Add(same with { Targets = same.Targets | target });
            }
            else
            {
                string id = $"auto-imported-{target.ToString().ToLowerInvariant()}-{DateTimeOffset.UtcNow.ToUnixTimeSeconds()}";
                prompts.Add(new PromptPresetDefinition
                {
                    Id = id,
                    Name = $"{label} 原始提示词",
                    Markdown = content.TrimEnd(),
                    Targets = target,
                });
            }

            changed = true;
        }

        return changed ? state with { PromptPresets = prompts } : state;
    }

    private async Task<WorkspaceFeatureState> CaptureLivePromptContentAsync(
        WorkspaceFeatureState state,
        ManagedClientTargets switchingTargets)
    {
        var prompts = state.PromptPresets.ToList();
        bool changed = false;
        foreach ((ManagedClientTargets target, string path, string label) in PromptFiles())
        {
            if ((switchingTargets & target) == ManagedClientTargets.None || !File.Exists(path)) continue;
            string live = await File.ReadAllTextAsync(path).ConfigureAwait(true);
            if (string.IsNullOrWhiteSpace(live)) continue;
            int enabledIndex = prompts.FindIndex(prompt => (prompt.Targets & target) != ManagedClientTargets.None);
            if (enabledIndex >= 0)
            {
                PromptPresetDefinition enabled = prompts[enabledIndex];
                if (!string.Equals(enabled.Markdown.Trim(), live.Trim(), StringComparison.Ordinal))
                {
                    prompts[enabledIndex] = enabled with { Markdown = live.TrimEnd() };
                    changed = true;
                }
            }
            else if (!prompts.Any(prompt => string.Equals(prompt.Markdown.Trim(), live.Trim(), StringComparison.Ordinal)))
            {
                prompts.Add(new PromptPresetDefinition
                {
                    Id = $"backup-{target.ToString().ToLowerInvariant()}-{DateTimeOffset.UtcNow.ToUnixTimeSeconds()}",
                    Name = $"{label} 原始提示词备份",
                    Markdown = live.TrimEnd(),
                    Targets = ManagedClientTargets.None,
                });
                changed = true;
            }
        }

        return changed ? state with { PromptPresets = prompts } : state;
    }

    private IEnumerable<(ManagedClientTargets Target, string Path, string Label)> PromptFiles()
    {
        yield return (ManagedClientTargets.Codex, _paths.CodexPromptPath, "Codex");
        yield return (ManagedClientTargets.Claude, _paths.ClaudePromptPath, "Claude");
        yield return (ManagedClientTargets.Gemini, _paths.GeminiPromptPath, "Gemini");
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;
        _mutationGate.Dispose();
    }
}

public sealed class McpServerItemViewModel(McpServerDefinition definition)
{
    public McpServerDefinition Definition { get; } = definition;
    public string Id => Definition.Id;
    public string Name => Definition.Name;
    public string Transport => Definition.Transport.ToString();
    public string TargetSummary => TargetDisplay(Definition.Targets);
    internal static string TargetDisplay(ManagedClientTargets targets) => string.Join(" · ", new[]
    {
        targets.HasFlag(ManagedClientTargets.Codex) ? "Codex" : null,
        targets.HasFlag(ManagedClientTargets.Claude) ? "Claude" : null,
        targets.HasFlag(ManagedClientTargets.Gemini) ? "Gemini" : null,
    }.Where(value => value is not null)) is { Length: > 0 } value ? value : "未启用";
}

public sealed class PromptPresetItemViewModel(PromptPresetDefinition definition)
{
    public PromptPresetDefinition Definition { get; } = definition;
    public string Id => Definition.Id;
    public string Name => Definition.Name;
    public string Preview => Definition.Markdown.Replace("\r", " ").Replace("\n", " ").Trim();
    public string TargetSummary => McpServerItemViewModel.TargetDisplay(Definition.Targets);
}

public sealed class SkillItemViewModel(ManagedSkillDefinition definition)
{
    public ManagedSkillDefinition Definition { get; } = definition;
    public string Id => Definition.Id;
    public string Name => Definition.Name;
    public string Description => Definition.Description ?? "本地技能";
    public string TargetSummary => McpServerItemViewModel.TargetDisplay(Definition.Targets);
    public string HashPreview => string.IsNullOrWhiteSpace(Definition.ContentSha256) ? "未计算" : Definition.ContentSha256[..Math.Min(12, Definition.ContentSha256.Length)];
}
