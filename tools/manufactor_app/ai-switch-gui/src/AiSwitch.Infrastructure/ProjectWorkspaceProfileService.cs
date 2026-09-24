using LanAi.Workspace.Core;

namespace LanAi.Workspace.Infrastructure;

public sealed record ProjectProfileOperationResult(ProjectWorkspaceProfile Profile, IReadOnlyList<string> Warnings);

/// <summary>
/// Project profile capture/apply translated from CC Switch v3.17 profile
/// service: leaving a bound project first saves its live state, then the target
/// project's provider, MCP, prompt and skill selections are applied best effort.
/// </summary>
public sealed class ProjectWorkspaceProfileService
{
    private readonly IWorkspaceFeatureStore _store;
    private readonly IOfficialClientExtensionSynchronizer _synchronizer;
    private readonly IConnectionProfileEditor _connections;
    private readonly SemaphoreSlim _gate = new(1, 1);

    public ProjectWorkspaceProfileService(
        IWorkspaceFeatureStore store,
        IOfficialClientExtensionSynchronizer synchronizer,
        IConnectionProfileEditor connections)
    {
        _store = store ?? throw new ArgumentNullException(nameof(store));
        _synchronizer = synchronizer ?? throw new ArgumentNullException(nameof(synchronizer));
        _connections = connections ?? throw new ArgumentNullException(nameof(connections));
    }

    public async Task<ProjectWorkspaceProfile> CaptureAsync(string projectId, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(projectId);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            WorkspaceFeatureState state = await _store.LoadAsync(cancellationToken).ConfigureAwait(false);
            ConnectionProfileRouting routing = await _connections.GetRoutingAsync(cancellationToken).ConfigureAwait(false);
            ProjectWorkspaceProfile profile = Capture(projectId.Trim(), state, routing);
            WorkspaceFeatureState next = ReplaceProfile(state, profile) with { CurrentProjectProfileId = projectId.Trim() };
            await _store.SaveAsync(next, cancellationToken).ConfigureAwait(false);
            return profile;
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task<ProjectProfileOperationResult> ApplyAsync(string projectId, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(projectId);
        await _gate.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            string normalizedId = projectId.Trim();
            WorkspaceFeatureState state = await _store.LoadAsync(cancellationToken).ConfigureAwait(false);
            ConnectionProfileRouting liveRouting = await _connections.GetRoutingAsync(cancellationToken).ConfigureAwait(false);

            if (!string.IsNullOrWhiteSpace(state.CurrentProjectProfileId) &&
                !string.Equals(state.CurrentProjectProfileId, normalizedId, StringComparison.OrdinalIgnoreCase))
            {
                ProjectWorkspaceProfile autoSaved = Capture(state.CurrentProjectProfileId!, state, liveRouting);
                state = ReplaceProfile(state, autoSaved);
            }

            ProjectWorkspaceProfile profile = state.ProjectProfiles.FirstOrDefault(item =>
                string.Equals(item.ProjectId, normalizedId, StringComparison.OrdinalIgnoreCase))
                ?? throw new KeyNotFoundException("该项目还没有工作配置快照，请先保存当前配置。");

            var warnings = new List<string>();
            try
            {
                await _connections.SetRoutingAsync(
                    new ConnectionProfileRouting(
                        profile.CodexConnectionProfileId ?? liveRouting.CodexProfileId,
                        profile.ClaudeConnectionProfileId ?? liveRouting.ClaudeCodeProfileId,
                        profile.GeminiConnectionProfileId ?? liveRouting.GeminiCliProfileId),
                    cancellationToken).ConfigureAwait(false);
            }
            catch (Exception exception) when (exception is InvalidOperationException or KeyNotFoundException)
            {
                warnings.Add($"连接来源：{exception.Message}");
            }

            WorkspaceFeatureState projected = ProjectExtensions(state, profile, warnings) with
            {
                CurrentProjectProfileId = normalizedId,
            };
            await _synchronizer.SynchronizeAsync(state, projected, cancellationToken).ConfigureAwait(false);
            try
            {
                await _store.SaveAsync(projected, cancellationToken).ConfigureAwait(false);
            }
            catch
            {
                await _synchronizer.SynchronizeAsync(projected, state, cancellationToken).ConfigureAwait(false);
                try { await _connections.SetRoutingAsync(liveRouting, cancellationToken).ConfigureAwait(false); } catch { }
                throw;
            }

            return new ProjectProfileOperationResult(profile, warnings);
        }
        finally
        {
            _gate.Release();
        }
    }

    private static ProjectWorkspaceProfile Capture(
        string projectId,
        WorkspaceFeatureState state,
        ConnectionProfileRouting routing)
    {
        return new ProjectWorkspaceProfile
        {
            ProjectId = projectId,
            CodexConnectionProfileId = routing.CodexProfileId,
            ClaudeConnectionProfileId = routing.ClaudeCodeProfileId,
            GeminiConnectionProfileId = routing.GeminiCliProfileId,
            CodexPromptPresetId = FindPrompt(state, ManagedClientTargets.Codex),
            ClaudePromptPresetId = FindPrompt(state, ManagedClientTargets.Claude),
            GeminiPromptPresetId = FindPrompt(state, ManagedClientTargets.Gemini),
            McpServerIds = state.McpServers.Where(item => item.Targets != ManagedClientTargets.None).Select(item => item.Id).ToArray(),
            SkillIds = state.Skills.Where(item => item.Targets != ManagedClientTargets.None).Select(item => item.Id).ToArray(),
            McpTargets = state.McpServers.ToDictionary(item => item.Id, item => item.Targets, StringComparer.OrdinalIgnoreCase),
            SkillTargets = state.Skills.ToDictionary(item => item.Id, item => item.Targets, StringComparer.OrdinalIgnoreCase),
            UpdatedAt = DateTimeOffset.UtcNow,
        };
    }

    private static WorkspaceFeatureState ProjectExtensions(
        WorkspaceFeatureState state,
        ProjectWorkspaceProfile profile,
        ICollection<string> warnings)
    {
        var knownMcp = state.McpServers.Select(item => item.Id).ToHashSet(StringComparer.OrdinalIgnoreCase);
        foreach (string id in profile.McpTargets.Keys.Where(id => !knownMcp.Contains(id))) warnings.Add($"MCP {id} 已不存在");
        var knownSkills = state.Skills.Select(item => item.Id).ToHashSet(StringComparer.OrdinalIgnoreCase);
        foreach (string id in profile.SkillTargets.Keys.Where(id => !knownSkills.Contains(id))) warnings.Add($"Skill {id} 已不存在");

        PromptPresetDefinition[] prompts = state.PromptPresets
            .Select(item => item with { Targets = ManagedClientTargets.None })
            .ToArray();
        prompts = EnablePrompt(prompts, profile.CodexPromptPresetId, ManagedClientTargets.Codex, warnings);
        prompts = EnablePrompt(prompts, profile.ClaudePromptPresetId, ManagedClientTargets.Claude, warnings);
        prompts = EnablePrompt(prompts, profile.GeminiPromptPresetId, ManagedClientTargets.Gemini, warnings);

        return state with
        {
            McpServers = state.McpServers.Select(item => item with
            {
                Targets = profile.McpTargets.GetValueOrDefault(item.Id, ManagedClientTargets.None),
            }).ToArray(),
            Skills = state.Skills.Select(item => item with
            {
                Targets = profile.SkillTargets.GetValueOrDefault(item.Id, ManagedClientTargets.None),
            }).ToArray(),
            PromptPresets = prompts,
        };
    }

    private static PromptPresetDefinition[] EnablePrompt(
        PromptPresetDefinition[] prompts,
        string? id,
        ManagedClientTargets target,
        ICollection<string> warnings)
    {
        if (string.IsNullOrWhiteSpace(id)) return prompts;
        int index = Array.FindIndex(prompts, item => string.Equals(item.Id, id, StringComparison.OrdinalIgnoreCase));
        if (index < 0)
        {
            warnings.Add($"提示词 {id} 已不存在");
            return prompts;
        }

        prompts[index] = prompts[index] with { Targets = prompts[index].Targets | target };
        return prompts;
    }

    private static string? FindPrompt(WorkspaceFeatureState state, ManagedClientTargets target) =>
        state.PromptPresets.FirstOrDefault(item => (item.Targets & target) != ManagedClientTargets.None)?.Id;

    private static WorkspaceFeatureState ReplaceProfile(WorkspaceFeatureState state, ProjectWorkspaceProfile profile) => state with
    {
        ProjectProfiles = state.ProjectProfiles
            .Where(item => !string.Equals(item.ProjectId, profile.ProjectId, StringComparison.OrdinalIgnoreCase))
            .Append(profile)
            .ToArray(),
    };
}
