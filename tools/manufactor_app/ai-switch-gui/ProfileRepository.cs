namespace AiSwitchGui;

internal sealed class ProfileRepository
{
    public const int MaxBackupFolders = 2;

    private readonly ConfigPaths _paths;

    public ProfileRepository(ConfigPaths paths)
    {
        _paths = paths;
    }

    public void EnsureInitialized()
    {
        Directory.CreateDirectory(_paths.Root);
        try
        {
            Directory.CreateDirectory(_paths.BackupRoot);
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            Directory.CreateDirectory(_paths.FallbackBackupRoot);
        }

        if (!File.Exists(_paths.SettingsFile))
        {
            JsonFile.Write(_paths.SettingsFile, new AppSettings { ConfigRoot = _paths.Root });
        }

        if (!File.Exists(_paths.ProfilesFile))
        {
            var store = BuildDefaultStore();
            JsonFile.Write(_paths.ProfilesFile, store);
        }
    }

    public ProfileStore LoadProfiles() => LoadProfiles(persistNormalizedDocument: true);

    /// <summary>
    /// Reads the old profile document and applies the runtime defaults required
    /// by the switcher.  Callers that only need a transient runtime snapshot
    /// can opt out of the legacy whole-document rewrite so extension fields
    /// maintained by the WPF editor remain intact.
    /// </summary>
    public ProfileStore LoadProfiles(bool persistNormalizedDocument)
    {
        var store = JsonFile.ReadOrDefault(_paths.ProfilesFile, BuildDefaultStore);
        NormalizeStore(store);
        if (persistNormalizedDocument)
        {
            SaveProfiles(store);
        }

        return store;
    }

    public AppSettings LoadSettings()
    {
        var settings = JsonFile.ReadOrDefault(_paths.SettingsFile, () => new AppSettings { ConfigRoot = _paths.Root });
        settings.ConfigRoot = _paths.Root;
        settings.Stats ??= new StatsSettings();
        SaveSettings(settings);
        return settings;
    }

    public void SaveSettings(AppSettings settings)
    {
        settings.ConfigRoot = _paths.Root;
        JsonFile.Write(_paths.SettingsFile, settings);
    }

    public void SaveProfiles(ProfileStore store)
    {
        JsonFile.Write(_paths.ProfilesFile, store);
    }

    public bool ProfilesFileExists()
    {
        return File.Exists(_paths.ProfilesFile);
    }

    public BackupSnapshot? GetLatestBackup()
    {
        var latest = ExistingBackupRoots()
            .SelectMany(path => new DirectoryInfo(path).GetDirectories())
            .OrderByDescending(x => x.Name)
            .FirstOrDefault();

        return latest is null ? null : new BackupSnapshot { Folder = latest.FullName };
    }

    public void TrimOldBackups()
    {
        foreach (string backupRoot in ExistingBackupRoots())
        {
            TrimOldBackups(backupRoot);
        }
    }

    private static void TrimOldBackups(string backupRoot)
    {
        var directories = new DirectoryInfo(backupRoot)
            .GetDirectories()
            .OrderByDescending(x => x.Name)
            .ToList();

        foreach (var stale in directories.Skip(MaxBackupFolders))
        {
            stale.Delete(recursive: true);
        }
    }

    private IEnumerable<string> ExistingBackupRoots()
    {
        if (Directory.Exists(_paths.BackupRoot))
        {
            yield return _paths.BackupRoot;
        }
        if (!string.Equals(_paths.FallbackBackupRoot, _paths.BackupRoot, StringComparison.OrdinalIgnoreCase) &&
            Directory.Exists(_paths.FallbackBackupRoot))
        {
            yield return _paths.FallbackBackupRoot;
        }
    }

    private static ProfileStore BuildDefaultStore()
    {
        return new ProfileStore
        {
            Cloud = ProfileDefinition.CreateCloudDefaults(),
            CloudSources =
            [
                ProfileDefinition.CreateCloudDefaults()
            ],
            SelectedCloudSourceId = ProfileSourceIds.Cloud,
            Local = ProfileDefinition.CreateLocalDefaults(),
            Lan = ProfileDefinition.CreateLanDefaults(),
            LocalSources =
            [
                ProfileDefinition.CreateLocalDefaults(),
                ProfileDefinition.CreateLanDefaults()
            ],
            SelectedLocalSourceId = ProfileSourceIds.LocalMachine,
            ActiveConnectionProfileId = ProfileSourceIds.LocalMachine,
            Mixed = MixedProfileDefinition.CreateDefault()
        };
    }

    private static void NormalizeStore(ProfileStore store)
    {
        store.Cloud ??= ProfileDefinition.CreateCloudDefaults();
        store.CloudSources ??= [];
        store.Local ??= ProfileDefinition.CreateLocalDefaults();
        store.Lan ??= ProfileDefinition.CreateLanDefaults();
        store.LocalSources ??= [];
        store.Mixed ??= MixedProfileDefinition.CreateDefault();

        if (string.IsNullOrWhiteSpace(store.Cloud.Name))
        {
            store.Cloud.Name = "云端";
        }

        if (string.IsNullOrWhiteSpace(store.Local.Name) ||
            string.Equals(store.Local.Name, "本地中转", StringComparison.OrdinalIgnoreCase))
        {
            store.Local.Name = "本机中转";
        }

        if (string.IsNullOrWhiteSpace(store.Lan.Name))
        {
            store.Lan.Name = "局域网中转";
        }

        store.Cloud.Codex ??= new ClientProfile();
        store.Cloud.Claude ??= new ClientProfile();
        store.Cloud.Gemini ??= new ClientProfile();
        store.Cloud.Grok ??= new ClientProfile();
        store.Local.Codex ??= new ClientProfile();
        store.Local.Claude ??= new ClientProfile();
        store.Local.Gemini ??= ProfileDefinition.CreateLocalDefaults().Gemini;
        store.Local.Grok ??= ProfileDefinition.CreateLocalDefaults().Grok;
        FillMissingBaseUrls(store.Local, ProfileDefinition.CreateLocalDefaults());
        store.Lan.Codex ??= ProfileDefinition.CreateLanDefaults().Codex;
        store.Lan.Claude ??= ProfileDefinition.CreateLanDefaults().Claude;
        store.Lan.Gemini ??= ProfileDefinition.CreateLanDefaults().Gemini;
        store.Lan.Grok ??= ProfileDefinition.CreateLanDefaults().Grok;
        NormalizeLanDashboardUrl(store.Lan);

        if (store.CloudSources.Count == 0)
        {
            store.CloudSources.Add(CloneProfile(store.Cloud, ProfileSourceIds.Cloud, "云端"));
        }

        EnsureCloudSource(store, ProfileSourceIds.Cloud, () => CloneProfile(store.Cloud, ProfileSourceIds.Cloud, "云端"));

        foreach (var source in store.CloudSources)
        {
            if (string.IsNullOrWhiteSpace(source.Id))
            {
                source.Id = Guid.NewGuid().ToString("N");
            }

            if (string.IsNullOrWhiteSpace(source.Name))
            {
                source.Name = "云端";
            }

            if (LooksLikeUrl(source.Name))
            {
                source.Name = string.Equals(source.Id, ProfileSourceIds.Cloud, StringComparison.OrdinalIgnoreCase)
                    ? "云端"
                    : "云端来源";
            }

            source.Codex ??= new ClientProfile();
            source.Claude ??= new ClientProfile();
            source.Gemini ??= new ClientProfile();
            source.Grok ??= new ClientProfile();
        }

        if (string.IsNullOrWhiteSpace(store.SelectedCloudSourceId) ||
            !store.CloudSources.Any(x => string.Equals(x.Id, store.SelectedCloudSourceId, StringComparison.OrdinalIgnoreCase)))
        {
            store.SelectedCloudSourceId = ProfileSourceIds.Cloud;
        }

        store.Cloud = store.CloudSources.FirstOrDefault(x =>
            string.Equals(x.Id, store.SelectedCloudSourceId, StringComparison.OrdinalIgnoreCase)) ?? store.CloudSources[0];

        if (store.LocalSources.Count == 0)
        {
            store.LocalSources.Add(CloneProfile(store.Local, ProfileSourceIds.LocalMachine, "本机中转"));
            store.LocalSources.Add(CloneProfile(store.Lan, ProfileSourceIds.LanDefault, "局域网中转"));
        }

        EnsureLocalSource(store, ProfileSourceIds.LocalMachine, () => CloneProfile(store.Local, ProfileSourceIds.LocalMachine, "本机中转"));
        EnsureLocalSource(store, ProfileSourceIds.LanDefault, () => CloneProfile(store.Lan, ProfileSourceIds.LanDefault, "局域网中转"));

        // Local modes are fixed system entries.  Retire any legacy custom
        // local sources while retaining the two supported configurations.
        store.LocalSources = store.LocalSources
            .Where(x => string.Equals(x.Id, ProfileSourceIds.LocalMachine, StringComparison.OrdinalIgnoreCase) ||
                        string.Equals(x.Id, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase))
            .ToList();

        foreach (var source in store.LocalSources)
        {
            if (string.IsNullOrWhiteSpace(source.Id))
            {
                source.Id = Guid.NewGuid().ToString("N");
            }

            if (string.IsNullOrWhiteSpace(source.Name))
            {
                source.Name = "本地中转";
            }

            source.Codex ??= new ClientProfile();
            source.Claude ??= new ClientProfile();
            source.Gemini ??= ProfileDefinition.CreateLocalDefaults().Gemini;
            source.Grok ??= string.Equals(source.Id, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase)
                ? ProfileDefinition.CreateLanDefaults().Grok
                : ProfileDefinition.CreateLocalDefaults().Grok;
            FillMissingBaseUrls(
                source,
                string.Equals(source.Id, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase)
                    ? ProfileDefinition.CreateLanDefaults()
                    : ProfileDefinition.CreateLocalDefaults());
            source.Name = string.Equals(source.Id, ProfileSourceIds.LocalMachine, StringComparison.OrdinalIgnoreCase)
                ? "本机中转"
                : "局域网中转";
            if (string.Equals(source.Id, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase))
            {
                NormalizeLanDashboardUrl(source);
            }
        }

        if (string.IsNullOrWhiteSpace(store.SelectedLocalSourceId) ||
            !store.LocalSources.Any(x => string.Equals(x.Id, store.SelectedLocalSourceId, StringComparison.OrdinalIgnoreCase)))
        {
            store.SelectedLocalSourceId = ProfileSourceIds.LocalMachine;
        }

        var selected = store.LocalSources.FirstOrDefault(x =>
            string.Equals(x.Id, store.SelectedLocalSourceId, StringComparison.OrdinalIgnoreCase)) ?? store.LocalSources[0];
        store.Local = selected;
        store.Lan = store.LocalSources.FirstOrDefault(x =>
            string.Equals(x.Id, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase)) ?? store.Lan;
        NormalizeLanDashboardUrl(store.Lan);

        // Older WinForms documents predate the WPF activity marker.  Supply a
        // deterministic local default only when it is absent or invalid; an
        // explicitly selected cloud source remains intact.
        if (string.IsNullOrWhiteSpace(store.ActiveConnectionProfileId) ||
            !IsKnownSource(store, store.ActiveConnectionProfileId))
        {
            store.ActiveConnectionProfileId = store.SelectedLocalSourceId;
        }

        if (string.IsNullOrWhiteSpace(store.Mixed.CodexSourceId))
        {
            store.Mixed.CodexSourceId = store.Mixed.CodexSource == ClientSourceMode.Cloud
                ? ProfileSourceIds.Cloud
                : store.SelectedLocalSourceId;
        }

        if (string.IsNullOrWhiteSpace(store.Mixed.ClaudeSourceId))
        {
            store.Mixed.ClaudeSourceId = store.Mixed.ClaudeSource == ClientSourceMode.Cloud
                ? ProfileSourceIds.Cloud
                : store.SelectedLocalSourceId;
        }

        if (string.IsNullOrWhiteSpace(store.Mixed.GeminiSourceId))
        {
            store.Mixed.GeminiSourceId = store.Mixed.GeminiSource == ClientSourceMode.Cloud
                ? ProfileSourceIds.Cloud
                : store.SelectedLocalSourceId;
        }

        if (string.IsNullOrWhiteSpace(store.Mixed.GrokSourceId))
        {
            store.Mixed.GrokSourceId = store.Mixed.GrokSource == ClientSourceMode.Cloud
                ? ProfileSourceIds.Cloud
                : store.SelectedLocalSourceId;
        }

        if (!IsKnownSource(store, store.Mixed.CodexSourceId))
        {
            store.Mixed.CodexSourceId = ProfileSourceIds.Cloud;
        }

        if (!IsKnownSource(store, store.Mixed.ClaudeSourceId))
        {
            store.Mixed.ClaudeSourceId = store.SelectedLocalSourceId;
        }

        if (!IsKnownSource(store, store.Mixed.GeminiSourceId))
        {
            store.Mixed.GeminiSourceId = store.SelectedLocalSourceId;
        }

        if (!IsKnownSource(store, store.Mixed.GrokSourceId))
        {
            store.Mixed.GrokSourceId = store.SelectedLocalSourceId;
        }

        store.BackupSourceIds = (store.BackupSourceIds ?? [])
            .Where(id => !string.IsNullOrWhiteSpace(id))
            .Select(id => id.Trim())
            .Where(id => store.CloudSources.Any(source =>
                string.Equals(source.Id, id, StringComparison.OrdinalIgnoreCase)))
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .ToList();
        store.BackupUpstreamEnabled ??= store.BackupSourceIds.Count > 0;
    }

    private static void EnsureLocalSource(ProfileStore store, string id, Func<ProfileDefinition> factory)
    {
        if (store.LocalSources.Any(x => string.Equals(x.Id, id, StringComparison.OrdinalIgnoreCase)))
        {
            return;
        }

        store.LocalSources.Add(factory());
    }

    private static void FillMissingBaseUrls(ProfileDefinition profile, ProfileDefinition defaults)
    {
        profile.Codex.BaseUrl = MissingFallback(profile.Codex.BaseUrl, defaults.Codex.BaseUrl);
        profile.Claude.BaseUrl = MissingFallback(profile.Claude.BaseUrl, defaults.Claude.BaseUrl);
        profile.Gemini.BaseUrl = MissingFallback(profile.Gemini.BaseUrl, defaults.Gemini.BaseUrl);
        profile.Grok.BaseUrl = MissingFallback(profile.Grok.BaseUrl, defaults.Grok.BaseUrl);
    }

    private static string MissingFallback(string? value, string fallback)
        => string.IsNullOrWhiteSpace(value) ? fallback : value;

    private static void EnsureCloudSource(ProfileStore store, string id, Func<ProfileDefinition> factory)
    {
        if (store.CloudSources.Any(x => string.Equals(x.Id, id, StringComparison.OrdinalIgnoreCase)))
        {
            return;
        }

        store.CloudSources.Add(factory());
    }

    private static bool IsKnownSource(ProfileStore store, string id)
    {
        return store.CloudSources.Any(x => string.Equals(x.Id, id, StringComparison.OrdinalIgnoreCase)) ||
               store.LocalSources.Any(x => string.Equals(x.Id, id, StringComparison.OrdinalIgnoreCase));
    }

    private static ProfileDefinition CloneProfile(ProfileDefinition source, string id, string fallbackName)
    {
        return new ProfileDefinition
        {
            Id = id,
            Name = string.IsNullOrWhiteSpace(source.Name) ? fallbackName : source.Name,
            Notes = source.Notes,
            DashboardUrl = source.DashboardUrl,
            Codex = new ClientProfile
            {
                BaseUrl = source.Codex?.BaseUrl ?? string.Empty,
                Secret = source.Codex?.Secret ?? string.Empty
            },
            Claude = new ClientProfile
            {
                BaseUrl = source.Claude?.BaseUrl ?? string.Empty,
                Secret = source.Claude?.Secret ?? string.Empty
            },
            Gemini = new ClientProfile
            {
                BaseUrl = source.Gemini?.BaseUrl ?? string.Empty,
                Secret = source.Gemini?.Secret ?? string.Empty
            },
            Grok = new ClientProfile
            {
                BaseUrl = source.Grok?.BaseUrl ?? string.Empty,
                Secret = source.Grok?.Secret ?? string.Empty
            }
        };
    }

    private static void NormalizeLanDashboardUrl(ProfileDefinition profile)
    {
        if (!string.IsNullOrWhiteSpace(profile.DashboardUrl))
        {
            return;
        }

        foreach (ClientProfile? client in new[] { profile.Codex, profile.Claude, profile.Gemini, profile.Grok })
        {
            if (TryCreateNativeDashboardUrl(client?.BaseUrl, out string dashboardUrl))
            {
                profile.DashboardUrl = dashboardUrl;
                return;
            }
        }
    }

    private static bool TryCreateNativeDashboardUrl(string? apiUrl, out string dashboardUrl)
    {
        dashboardUrl = string.Empty;
        if (string.IsNullOrWhiteSpace(apiUrl) ||
            !Uri.TryCreate(apiUrl.Trim(), UriKind.Absolute, out Uri? apiUri) ||
            apiUri.Scheme is not ("http" or "https") ||
            string.IsNullOrWhiteSpace(apiUri.Host) ||
            !string.IsNullOrWhiteSpace(apiUri.UserInfo) ||
            apiUri.Port != 8080)
        {
            return false;
        }

        dashboardUrl = new UriBuilder(apiUri.Scheme, apiUri.Host, 3000)
        {
            Path = "/dashboard",
        }.Uri.AbsoluteUri;
        return true;
    }

    private static bool LooksLikeUrl(string value)
    {
        return Uri.TryCreate(value, UriKind.Absolute, out var uri) &&
               (uri.Scheme == Uri.UriSchemeHttp || uri.Scheme == Uri.UriSchemeHttps);
    }
}


