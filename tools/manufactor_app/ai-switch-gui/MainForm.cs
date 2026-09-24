using System.Diagnostics;
using System.Drawing.Drawing2D;
using System.Net;

namespace AiSwitchGui;

internal sealed class MainForm : Form
{
    private static readonly Color AppBg = Color.FromArgb(240, 244, 248);
    private static readonly Color Surface = Color.White;
    private static readonly Color SurfaceAlt = Color.FromArgb(248, 250, 252);
    private static readonly Color Border = Color.FromArgb(226, 232, 240);
    private static readonly Color Primary = Color.FromArgb(37, 99, 235);
    private static readonly Color PrimarySoft = Color.FromArgb(239, 246, 255);
    private static readonly Color Success = Color.FromArgb(16, 185, 129);
    private static readonly Color SuccessSoft = Color.FromArgb(240, 253, 250);
    private static readonly Color Warning = Color.FromArgb(245, 158, 11);
    private static readonly Color WarningSoft = Color.FromArgb(254, 243, 199);
    private static readonly Color Danger = Color.FromArgb(239, 68, 68);
    private static readonly Color DangerSoft = Color.FromArgb(254, 242, 242);
    private static readonly Color TextMain = Color.FromArgb(15, 23, 42);
    private static readonly Color TextMuted = Color.FromArgb(100, 116, 139);

    private static readonly Color SidebarBg = Color.FromArgb(15, 23, 42);
    private static readonly Color SidebarButtonBg = Color.Transparent;
    private static readonly Color SidebarButtonActiveBg = Color.FromArgb(30, 41, 59);
    private static readonly Color SidebarButtonHoverBg = Color.FromArgb(24, 37, 57);
    private static readonly Color SidebarText = Color.FromArgb(148, 163, 184);
    private static readonly Color SidebarTextActive = Color.White;
    private static readonly string BrandLogoPath = Path.Combine(AppContext.BaseDirectory, "Assets", "airplane-app-icon.png");
    private static readonly string AppIconPath = Path.Combine(AppContext.BaseDirectory, "Assets", "airplane-app-icon.ico");

    private readonly ConfigPaths _paths;
    private readonly ProfileRepository _repository;
    private readonly SwitchService _switchService;
    private readonly LocalGatewayService _localGatewayService = new();
    private readonly CpaSub2ApiConverter _cpaSub2ApiConverter = new();
    private readonly SessionConfigSnapshot _sessionSnapshot;
    private readonly NotifyIcon _trayIcon = new();
    private readonly EventWaitHandle _activateEvent = new(false, EventResetMode.AutoReset, Program.ActivateEventName);
    private readonly CancellationTokenSource _activateListenerCts = new();

    private readonly TabControl _mainTabs = new();
    private readonly Label _headerTitle = new();
    private readonly List<Button> _sidebarButtons = [];
    private readonly TabControl _profileTabs = new();
    private readonly ComboBox _cloudSourceBox = new();
    private readonly ComboBox _localSourceBox = new();
    private readonly ComboBox _mixedCodexSourceBox = new();
    private readonly ComboBox _mixedClaudeSourceBox = new();
    private readonly ComboBox _mixedGeminiSourceBox = new();
    private readonly ComboBox _mixedGrokSourceBox = new();
    private readonly DataGridView _cloudSourcesGrid = new();
    private readonly DataGridView _localSourcesGrid = new();

    private readonly TextBox _cloudNotesBox = new();
    private readonly TextBox _cloudCodexBaseBox = new();
    private readonly TextBox _cloudCodexKeyBox = new();
    private readonly TextBox _cloudClaudeBaseBox = new();
    private readonly TextBox _cloudClaudeKeyBox = new();
    private readonly TextBox _cloudGeminiBaseBox = new();
    private readonly TextBox _cloudGeminiKeyBox = new();
    private readonly TextBox _cloudGrokBaseBox = new();
    private readonly TextBox _cloudGrokKeyBox = new();
    private readonly TextBox _cloudSourceNameBox = new();

    private readonly TextBox _localNotesBox = new();
    private readonly TextBox _localCodexBaseBox = new();
    private readonly TextBox _localCodexKeyBox = new();
    private readonly TextBox _localClaudeBaseBox = new();
    private readonly TextBox _localClaudeKeyBox = new();
    private readonly TextBox _localGeminiBaseBox = new();
    private readonly TextBox _localGeminiKeyBox = new();
    private readonly TextBox _localGrokBaseBox = new();
    private readonly TextBox _localGrokKeyBox = new();
    private readonly TextBox _localSourceNameBox = new();
    private readonly Label _localSourceNameValue = new();

    private readonly Label _modeBadge = new();
    private readonly Label _healthBadge = new();
    private readonly Label _currentTargetValue = new();
    private readonly Label _codexBaseValue = new();
    private readonly Label _claudeBaseValue = new();
    private readonly Label _geminiBaseValue = new();
    private readonly Label _backupValue = new();
    private readonly Label _codexValidationValue = new();
    private readonly Label _claudeValidationValue = new();
    private readonly Label _geminiValidationValue = new();
    private readonly Label _pendingModeValue = new();
    private readonly Label _pendingDetailValue = new();
    private readonly Label _localGatewayStatusValue = new();
    private readonly Label _localGatewayWebValue = new();
    private readonly Label _localGatewayComposeValue = new();
    private readonly TextBox _localGatewayServicesBox = new();
    private readonly TextBox _statusBox = new();
    private readonly CheckBox _closeToTrayCheckBox = new();

    // 流量统计（面向用户：登录 Sub2API 看本账号消耗）
    private StatsService _statsService = null!;
    private readonly TextBox _statsGatewayBox = new();
    private readonly TextBox _statsEmailBox = new();
    private readonly TextBox _statsPasswordBox = new();
    private readonly Button _statsRefreshButton = new();
    private readonly Label _statsHintValue = new();
    private readonly Label _statsTotalRequestsValue = new();
    private readonly Label _statsTotalTokensValue = new();
    private readonly Label _statsTotalCostValue = new();
    private readonly Label _statsTodayRequestsValue = new();
    private readonly Label _statsTodayTokensValue = new();
    private readonly Label _statsTodayCostValue = new();
    private readonly Label _statsCacheReadValue = new();
    private readonly Label _statsAvgDurationValue = new();
    private readonly DataGridView _statsModelsGrid = new();
    private readonly DataGridView _statsTrendGrid = new();

    private readonly Button _applySelectedButton = new();
    private readonly Button _saveButton = new();
    private readonly Button _testSelectedButton = new();
    private readonly Button _openSelectedSiteButton = new();
    private readonly Button _importToCloudButton = new();
    private readonly Button _addCloudSourceButton = new();
    private readonly Button _deleteCloudSourceButton = new();
    private readonly Button _importToLocalButton = new();
    private readonly Button _addLocalSourceButton = new();
    private readonly Button _deleteLocalSourceButton = new();
    private readonly Button _localGatewayStartButton = new();
    private readonly Button _localGatewayRestartButton = new();
    private readonly Button _localGatewayStopButton = new();
    private readonly Button _localGatewayRefreshButton = new();
    private readonly Button _localGatewayOpenAdminButton = new();
    private readonly Button _localGatewayOpenLanAdminButton = new();
    private readonly Button _convertCpaAccountsButton = new();

    private readonly Font _uiFont = new("Microsoft YaHei UI", 9F, FontStyle.Regular, GraphicsUnit.Point);
    private readonly Font _titleFont = new("Microsoft YaHei UI", 18F, FontStyle.Bold, GraphicsUnit.Point);
    private readonly Font _sectionFont = new("Microsoft YaHei UI", 11F, FontStyle.Bold, GraphicsUnit.Point);
    private readonly Font _strongFont = new("Microsoft YaHei UI", 9.5F, FontStyle.Bold, GraphicsUnit.Point);
    private readonly Font _monoFont = new("Consolas", 9F, FontStyle.Regular, GraphicsUnit.Point);
    private readonly List<(ColumnStyle Style, float Width)> _scaledColumnStyles = [];

    private ProfileStore _currentStore = new();
    private AppSettings _settings = new();
    private bool _forceExit;
    private bool _localGatewayIsRunning;
    private bool _localGatewayDockerInstalled;
    private bool _localGatewayWebReachable;
    private bool _localGatewayComposeReady;
    private LocalGatewayStatus? _lastLocalGatewayStatus;
    private bool _loadingCloudSourceFields;
    private bool _loadingLocalSourceFields;
    private bool _loadingSourceGrids;

    public MainForm()
    {
        var root = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            "ai-switch-gui");

        _paths = new ConfigPaths(root);
        _repository = new ProfileRepository(_paths);
        _switchService = new SwitchService(_paths, _repository);
        _sessionSnapshot = _switchService.CreateSessionSnapshot();

        _repository.EnsureInitialized();
        _settings = _repository.LoadSettings();
        if (_settings.CloseToTrayOnClose)
        {
            _settings.CloseToTrayOnClose = false;
            _repository.SaveSettings(_settings);
        }

        _statsService = new StatsService(_settings.Stats);
        _currentStore = _repository.LoadProfiles();

        InitializeWindow();
        InitializeTrayIcon();
        BuildLayout();
        LoadProfilesIntoForm(_currentStore);

        var status = RefreshLiveStatus();
        AppendLine("程序已启动。");
        AppendLine($"启动识别结果: {status.ActiveTarget} - {status.Summary}");
        AppendLine("已记录打开前的 Codex / Claude Code 主配置，退出时会自动恢复。");
        ApplyStartupLocalGatewayStatus();
        StartActivateListener();
    }

    private void InitializeWindow()
    {
        Text = "本地中转管理工具";
        Icon = LoadApplicationIcon();
        StartPosition = FormStartPosition.CenterScreen;
        BackColor = AppBg;
        Font = _uiFont;
        AutoScaleMode = AutoScaleMode.Dpi;
        ShowInTaskbar = true;
        MinimizeBox = true;
        MaximizeBox = true;
        ApplyDpiSizes(initial: true);
    }

    private void InitializeTrayIcon()
    {
        _trayIcon.Text = "本地中转管理工具";
        _trayIcon.Icon = Icon ?? LoadApplicationIcon() ?? SystemIcons.Application;
        _trayIcon.Visible = true;

        var menu = new ContextMenuStrip();
        menu.Items.Add("打开本地中转管理工具", null, (_, _) => RestoreFromTray());
        menu.Items.Add("打开 Sub2API 后台", null, (_, _) => OpenUrl(_localGatewayService.WebUrl, "Sub2API 后台"));
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add("退出", null, (_, _) => ExitForReal());
        _trayIcon.ContextMenuStrip = menu;
        _trayIcon.DoubleClick += (_, _) => RestoreFromTray();
    }

    protected override void OnDpiChangedAfterParent(EventArgs e)
    {
        base.OnDpiChangedAfterParent(e);
        ApplyDpiSizes(initial: false);
    }

    protected override void OnResize(EventArgs e)
    {
        base.OnResize(e);
        if (WindowState == FormWindowState.Minimized)
        {
            HideToTray("已收起到任务栏小图标。右键小图标可退出并恢复打开前配置。");
        }
    }

    protected override void OnFormClosing(FormClosingEventArgs e)
    {
        if (!_forceExit && _settings.CloseToTrayOnClose && e.CloseReason == CloseReason.UserClosing)
        {
            e.Cancel = true;
            HideToTray("已收起到任务栏小图标。要彻底退出，请右键小图标选择“退出”。");
            return;
        }

        try
        {
            _switchService.RestoreSessionSnapshot(_sessionSnapshot);
        }
        catch (Exception ex)
        {
            MessageBox.Show(
                $"退出时恢复打开前配置失败：{ex.Message}",
                "本地中转管理工具",
                MessageBoxButtons.OK,
                MessageBoxIcon.Warning);
        }

        base.OnFormClosing(e);
    }

    protected override void Dispose(bool disposing)
    {
        if (disposing)
        {
            _activateListenerCts.Cancel();
            _activateListenerCts.Dispose();
            _activateEvent.Dispose();
            _trayIcon.Visible = false;
            _trayIcon.Dispose();
        }

        base.Dispose(disposing);
    }

    private void StartActivateListener()
    {
        Task.Run(() =>
        {
            while (!_activateListenerCts.IsCancellationRequested)
            {
                try
                {
                    var signaled = _activateEvent.WaitOne(TimeSpan.FromMilliseconds(500));
                    if (signaled && !_activateListenerCts.IsCancellationRequested && IsHandleCreated)
                    {
                        BeginInvoke(RestoreFromTray);
                    }
                }
                catch (ObjectDisposedException)
                {
                    return;
                }
                catch
                {
                    // Keep the UI alive even if the activation listener sees a transient handle issue.
                }
            }
        }, _activateListenerCts.Token);
    }

    private int ScaleValue(int value)
    {
        return (int)Math.Round(value * DeviceDpi / 96F);
    }

    private Size ScaleSize(Size size)
    {
        return new Size(ScaleValue(size.Width), ScaleValue(size.Height));
    }

    private Size ClampSize(Size size)
    {
        var bounds = Screen.FromControl(this).WorkingArea;
        var maxWidth = Math.Max(ScaleValue(640), bounds.Width - ScaleValue(24));
        var maxHeight = Math.Max(ScaleValue(480), bounds.Height - ScaleValue(24));
        return new Size(Math.Min(size.Width, maxWidth), Math.Min(size.Height, maxHeight));
    }

    private ColumnStyle CreateScaledAbsoluteColumn(float width)
    {
        var style = new ColumnStyle(SizeType.Absolute, ScaleValue((int)width));
        _scaledColumnStyles.Add((style, width));
        return style;
    }

    private void ApplyDpiSizes(bool initial)
    {
        MinimumSize = ClampSize(ScaleSize(new Size(1080, 700)));
        if (initial)
        {
            Size = ClampSize(ScaleSize(new Size(1440, 920)));
        }

        foreach (var (style, width) in _scaledColumnStyles)
        {
            style.Width = ScaleValue((int)width);
        }
    }

    private void BuildLayout()
    {
        var mainLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 2,
            RowCount = 1,
            BackColor = AppBg,
            Margin = Padding.Empty,
            Padding = Padding.Empty
        };
        mainLayout.ColumnStyles.Add(CreateScaledAbsoluteColumn(220F)); // Left Sidebar
        mainLayout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F)); // Main Area

        // Create Sidebar
        mainLayout.Controls.Add(BuildSidebar(), 0, 0);

        // Create Main workspace
        var workspaceLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 3,
            BackColor = AppBg,
            Padding = new Padding(ScaleValue(20), ScaleValue(16), ScaleValue(20), ScaleValue(12))
        };
        workspaceLayout.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(60))); // Top Header Bar
        workspaceLayout.RowStyles.Add(new RowStyle(SizeType.Percent, 100F)); // Tabs Content
        workspaceLayout.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(28))); // Footer

        workspaceLayout.Controls.Add(BuildHeaderBar(), 0, 0);
        workspaceLayout.Controls.Add(BuildWorkbench(), 0, 1);
        workspaceLayout.Controls.Add(BuildFooter(), 0, 2);

        mainLayout.Controls.Add(workspaceLayout, 1, 0);
        Controls.Add(mainLayout);
        
        // Select first tab by default
        SwitchTab(0);
    }

    private Control BuildSidebar()
    {
        var sidebar = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 8,
            BackColor = SidebarBg,
            Padding = new Padding(0, ScaleValue(24), 0, ScaleValue(24))
        };
        sidebar.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        sidebar.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(80)));
        sidebar.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(30)));
        sidebar.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(50)));
        sidebar.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(50)));
        sidebar.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(50)));
        sidebar.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(50)));
        sidebar.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        var brandPanel = BuildSidebarBrandMark();
        brandPanel.Dock = DockStyle.Fill;

        var btnConfig = CreateSidebarButton("配置与切换", 0);
        var btnGateway = CreateSidebarButton("本地中转站", 1);
        var btnSavings = CreateSidebarButton("流量统计", 2);
        var btnLogs = CreateSidebarButton("运行日志", 3);

        _sidebarButtons.Clear();
        _sidebarButtons.Add(btnConfig);
        _sidebarButtons.Add(btnGateway);
        _sidebarButtons.Add(btnSavings);
        _sidebarButtons.Add(btnLogs);

        sidebar.Controls.Add(brandPanel, 0, 0);
        sidebar.Controls.Add(new Panel { Dock = DockStyle.Fill, BackColor = Color.Transparent }, 0, 1);
        sidebar.Controls.Add(btnConfig, 0, 2);
        sidebar.Controls.Add(btnGateway, 0, 3);
        sidebar.Controls.Add(btnSavings, 0, 4);
        sidebar.Controls.Add(btnLogs, 0, 5);

        return sidebar;
    }

    private Control BuildSidebarBrandMark()
    {
        var host = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            Height = ScaleValue(80),
            ColumnCount = 2,
            RowCount = 1,
            BackColor = Color.Transparent,
            Padding = new Padding(ScaleValue(16), ScaleValue(8), ScaleValue(16), ScaleValue(8)),
            Margin = Padding.Empty
        };
        host.ColumnStyles.Add(CreateScaledAbsoluteColumn(54F));
        host.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));

        var picture = new PictureBox
        {
            Size = ScaleSize(new Size(44, 44)),
            SizeMode = PictureBoxSizeMode.Zoom,
            BackColor = Color.Transparent,
            Margin = new Padding(0, ScaleValue(4), ScaleValue(8), 0)
        };

        if (File.Exists(BrandLogoPath))
        {
            picture.Image = Image.FromFile(BrandLogoPath);
        }
        else if (File.Exists(AppIconPath))
        {
            picture.Image = Icon.ExtractAssociatedIcon(AppIconPath)?.ToBitmap();
        }

        var textHost = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 1,
            RowCount = 2,
            BackColor = Color.Transparent,
            Margin = Padding.Empty
        };
        textHost.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        textHost.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        
        textHost.Controls.Add(new Label
        {
            Text = "归年",
            AutoSize = true,
            ForeColor = Color.White,
            Font = new Font("Microsoft YaHei UI", 12F, FontStyle.Bold, GraphicsUnit.Point),
            Margin = Padding.Empty
        }, 0, 0);
        
        textHost.Controls.Add(new Label
        {
            Text = "中转切换工具",
            AutoSize = true,
            ForeColor = Color.FromArgb(148, 163, 184), // slate-400
            Font = new Font("Microsoft YaHei UI", 8.5F, FontStyle.Regular, GraphicsUnit.Point),
            Margin = new Padding(0, ScaleValue(2), 0, 0)
        }, 0, 1);

        host.Controls.Add(picture, 0, 0);
        host.Controls.Add(textHost, 1, 0);
        return host;
    }

    private Button CreateSidebarButton(string text, int tabIndex)
    {
        var button = new Button
        {
            Text = text,
            Dock = DockStyle.Fill,
            FlatStyle = FlatStyle.Flat,
            BackColor = SidebarButtonBg,
            ForeColor = SidebarText,
            Font = _strongFont,
            TextAlign = ContentAlignment.MiddleLeft,
            Padding = new Padding(ScaleValue(16), 0, 0, 0),
            Margin = new Padding(ScaleValue(10), 0, ScaleValue(10), ScaleValue(6))
        };
        button.FlatAppearance.BorderSize = 0;
        button.FlatAppearance.MouseOverBackColor = SidebarButtonHoverBg;
        button.FlatAppearance.MouseDownBackColor = SidebarButtonActiveBg;
        
        button.Click += (s, e) =>
        {
            SwitchTab(tabIndex);
        };
        
        return button;
    }

    private void SwitchTab(int index)
    {
        if (index < 0 || index >= _mainTabs.TabPages.Count) return;
        _mainTabs.SelectedIndex = index;
        
        for (int i = 0; i < _sidebarButtons.Count; i++)
        {
            var btn = _sidebarButtons[i];
            if (i == index)
            {
                btn.BackColor = SidebarButtonActiveBg;
                btn.ForeColor = SidebarTextActive;
            }
            else
            {
                btn.BackColor = SidebarButtonBg;
                btn.ForeColor = SidebarText;
            }
        }
        
        _headerTitle.Text = index switch
        {
            0 => "配置与切换",
            1 => "本地中转站",
            2 => "流量统计",
            3 => "运行日志",
            _ => "管理工具"
        };

        if (index == 2)
        {
            UpdateStatsGatewayDisplay();
        }
    }

    private Control BuildHeaderBar()
    {
        var header = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 2,
            RowCount = 1,
            BackColor = Color.Transparent,
            Margin = Padding.Empty,
            Padding = new Padding(0, 0, 0, ScaleValue(10))
        };
        header.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        header.ColumnStyles.Add(CreateScaledAbsoluteColumn(480F)); // badges width

        _headerTitle.Text = "配置与切换";
        _headerTitle.Font = new Font("Microsoft YaHei UI", 16F, FontStyle.Bold, GraphicsUnit.Point);
        _headerTitle.ForeColor = TextMain;
        _headerTitle.TextAlign = ContentAlignment.MiddleLeft;
        _headerTitle.Dock = DockStyle.Fill;
        _headerTitle.Margin = Padding.Empty;

        var badgeFlow = new FlowLayoutPanel
        {
            Dock = DockStyle.Fill,
            FlowDirection = FlowDirection.RightToLeft,
            WrapContents = false,
            BackColor = Color.Transparent,
            Margin = Padding.Empty
        };

        ConfigureBadge(_modeBadge);
        ConfigureBadge(_healthBadge);
        
        _modeBadge.Margin = new Padding(ScaleValue(8), ScaleValue(4), 0, 0);
        _healthBadge.Margin = new Padding(ScaleValue(8), ScaleValue(4), 0, 0);

        badgeFlow.Controls.Add(_healthBadge);
        badgeFlow.Controls.Add(_modeBadge);

        header.Controls.Add(_headerTitle, 0, 0);
        header.Controls.Add(badgeFlow, 1, 0);
        return header;
    }

    private static Icon? LoadApplicationIcon()
    {
        try
        {
            var executablePath = Environment.ProcessPath;
            if (!string.IsNullOrWhiteSpace(executablePath) && File.Exists(executablePath))
            {
                var executableIcon = Icon.ExtractAssociatedIcon(executablePath);
                if (executableIcon is not null)
                {
                    return executableIcon;
                }
            }

            return File.Exists(AppIconPath) ? new Icon(AppIconPath) : null;
        }
        catch
        {
            return null;
        }
    }

    private Control BuildWorkbench()
    {
        _mainTabs.Dock = DockStyle.Fill;
        _mainTabs.SizeMode = TabSizeMode.Fixed;
        _mainTabs.ItemSize = new Size(0, 1); // Hide tab headers!
        _mainTabs.Padding = Point.Empty;
        _mainTabs.Margin = Padding.Empty;
        _mainTabs.BackColor = AppBg;

        _mainTabs.TabPages.Clear();
        _mainTabs.TabPages.Add(BuildSwitchTabPage());
        _mainTabs.TabPages.Add(WrapInTabPage("本地中转站", BuildLocalGatewayCard()));
        _mainTabs.TabPages.Add(WrapInTabPage("流量统计", BuildTrafficStatsCard()));
        _mainTabs.TabPages.Add(WrapInTabPage("运行日志", BuildLogCard()));
        
        return _mainTabs;
    }

    private TabPage WrapInTabPage(string title, Control content)
    {
        var page = new TabPage(title)
        {
            BackColor = AppBg,
            Padding = new Padding(0) // Remove padding inside main tabs since cards have their own padding
        };
        content.Dock = DockStyle.Fill;
        page.Controls.Add(content);
        return page;
    }

    private TabPage BuildSwitchTabPage()
    {
        var page = new TabPage("配置与切换")
        {
            BackColor = AppBg,
            Padding = new Padding(0)
        };

        var grid = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 2,
            RowCount = 1,
            BackColor = AppBg
        };
        grid.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        grid.ColumnStyles.Add(CreateScaledAbsoluteColumn(400F));
        grid.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        var profiles = BuildProfilesCard();
        profiles.Margin = new Padding(0, 0, ScaleValue(14), 0);
        grid.Controls.Add(profiles, 0, 0);

        var rightColumn = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 2,
            BackColor = AppBg
        };
        rightColumn.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        rightColumn.RowStyles.Add(new RowStyle(SizeType.Percent, 55F));
        rightColumn.RowStyles.Add(new RowStyle(SizeType.Percent, 45F));

        var status = BuildStatusCard();
        status.Margin = new Padding(0, 0, 0, ScaleValue(14));
        rightColumn.Controls.Add(status, 0, 0);
        rightColumn.Controls.Add(BuildActionsCard(), 0, 1);

        grid.Controls.Add(rightColumn, 1, 0);
        page.Controls.Add(grid);
        return page;
    }

    private Control BuildStatusCard()
    {
        var card = CreateCardPanel();
        card.Dock = DockStyle.Fill;
        card.Padding = new Padding(ScaleValue(18), ScaleValue(14), ScaleValue(18), ScaleValue(14));

        var layout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 2,
            BackColor = Surface
        };
        layout.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        layout.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        layout.Controls.Add(CreateSectionHeader("当前状态", "始终显示主配置现在真正生效的目标和验证结果。"), 0, 0);

        var metricsHost = new Panel
        {
            Dock = DockStyle.Fill,
            AutoScroll = true,
            BackColor = Surface,
            Margin = new Padding(0, ScaleValue(10), 0, 0)
        };

        var metrics = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 1,
            RowCount = 7,
            BackColor = Surface
        };
        metrics.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        for (var i = 0; i < 8; i++)
        {
            metrics.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        }

        metrics.Controls.Add(CreateMetricTile("生效目标", _currentTargetValue), 0, 0);
        metrics.Controls.Add(CreateMetricTile("最近备份", _backupValue), 0, 1);
        metrics.Controls.Add(CreateMetricTile("Codex 地址", _codexBaseValue), 0, 2);
        metrics.Controls.Add(CreateMetricTile("Claude 地址", _claudeBaseValue), 0, 3);
        metrics.Controls.Add(CreateMetricTile("Gemini 地址", _geminiBaseValue), 0, 4);
        metrics.Controls.Add(CreateMetricTile("Codex 验证", _codexValidationValue), 0, 5);
        metrics.Controls.Add(CreateMetricTile("Claude 验证", _claudeValidationValue), 0, 6);
        metrics.Controls.Add(CreateMetricTile("Gemini 验证", _geminiValidationValue), 0, 7);

        metricsHost.Controls.Add(metrics);
        layout.Controls.Add(metricsHost, 0, 1);
        card.Controls.Add(layout);
        return card;
    }

    private Control BuildProfilesCard()
    {
        var card = CreateCardPanel();
        card.Dock = DockStyle.Fill;
        card.Padding = new Padding(ScaleValue(18), ScaleValue(14), ScaleValue(18), ScaleValue(18));

        var layout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 2,
            BackColor = Surface
        };
        layout.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        layout.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        layout.Controls.Add(CreateSectionHeader("配置来源", "维护云端、本机、局域网中转来源，并选择混合组合。"), 0, 0);
        layout.Controls.Add(BuildProfilesTabs(), 0, 1);

        card.Controls.Add(layout);
        return card;
    }

    private Control BuildProfilesTabs()
    {
        _profileTabs.Dock = DockStyle.Fill;
        _profileTabs.SizeMode = TabSizeMode.Fixed;
        _profileTabs.ItemSize = ScaleSize(new Size(120, 34));
        _profileTabs.Padding = new Point(ScaleValue(12), ScaleValue(6));
        _profileTabs.Font = _uiFont;
        _profileTabs.SelectedIndexChanged += (_, _) => UpdatePendingActionSummary();

        _profileTabs.TabPages.Clear();
        _profileTabs.TabPages.Add(BuildCloudSourcesPage());
        _profileTabs.TabPages.Add(BuildLocalSourcesPage());
        _profileTabs.TabPages.Add(BuildMixedPage());
        _profileTabs.SelectedIndex = 1;
        return _profileTabs;
    }

    private TabPage BuildCloudSourcesPage()
    {
        var page = new TabPage("云端")
        {
            BackColor = Surface,
            Padding = new Padding(ScaleValue(10))
        };

        ConfigureComboBox(_cloudSourceBox);
        ConfigureTextBox(_cloudSourceNameBox);
        _cloudSourceNameBox.PlaceholderText = "例如：备用远程来源";
        _cloudSourceNameBox.TextChanged += (_, _) => PersistCurrentCloudSourceFields();
        _cloudSourceBox.SelectedIndexChanged += (_, _) => LoadSelectedCloudSourceIntoFields();

        var mainSplit = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 2,
            RowCount = 1,
            BackColor = Surface
        };
        mainSplit.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 34F)); // Left: source selector
        mainSplit.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 66F)); // Right: inputs detail
        mainSplit.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        // --- Left Side (Grid + Add/Delete Buttons) ---
        var leftLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 3,
            BackColor = Surface
        };
        leftLayout.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(72))); // Add/Delete button bar
        leftLayout.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));
        leftLayout.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(30))); // Info Label

        ConfigureSourceGrid(_cloudSourcesGrid);
        
        _cloudSourcesGrid.SelectionChanged += (_, _) =>
        {
            if (_loadingSourceGrids || _cloudSourcesGrid.CurrentRow is null || _cloudSourcesGrid.CurrentRow.IsNewRow) return;
            var id = Convert.ToString(_cloudSourcesGrid.CurrentRow.Cells["Id"].Value);
            if (string.IsNullOrWhiteSpace(id)) return;
            SelectSourceOption(_cloudSourceBox, id);
        };

        var gridPanel = new Panel
        {
            Dock = DockStyle.Fill,
            Padding = new Padding(0, 0, ScaleValue(8), 0)
        };
        _cloudSourcesGrid.Dock = DockStyle.Fill;
        gridPanel.Controls.Add(_cloudSourcesGrid);
        leftLayout.Controls.Add(gridPanel, 0, 1);

        ConfigurePrimaryButton(_addCloudSourceButton, "+ 新增云端", Primary, (_, _) => AddCloudSource());
        ConfigureSecondaryButton(_deleteCloudSourceButton, "删除", (_, _) => DeleteSelectedCloudSource());
        var toolbar = CreateSourceToolbar("云端来源", _addCloudSourceButton, _deleteCloudSourceButton);
        leftLayout.Controls.Add(toolbar, 0, 0);

        var helpLabel = new Label
        {
            Text = "左侧选择来源，右侧编辑完整配置。",
            ForeColor = TextMuted,
            Font = new Font("Microsoft YaHei UI", 8F),
            Dock = DockStyle.Fill,
            TextAlign = ContentAlignment.MiddleLeft
        };
        leftLayout.Controls.Add(helpLabel, 0, 2);

        // --- Right Side (Detailed Config Fields) ---
        var rightScroll = new Panel
        {
            Dock = DockStyle.Fill,
            AutoScroll = true,
            BackColor = Surface,
            Padding = new Padding(ScaleValue(8), 0, 0, 0)
        };

        var rightLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            ColumnCount = 1,
            RowCount = 4,
            BackColor = Surface,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink
        };
        for (var i = 0; i < 5; i++)
        {
            rightLayout.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        }

        var nameHost = CreateSoftSection("基本信息");
        var nameLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 2,
            RowCount = 1,
            BackColor = nameHost.BackColor
        };
        nameLayout.ColumnStyles.Add(CreateScaledAbsoluteColumn(80));
        nameLayout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        nameLayout.Controls.Add(CreateInputLabel("来源名称"), 0, 0);
        nameLayout.Controls.Add(_cloudSourceNameBox, 1, 0);
        _cloudSourceNameBox.Margin = new Padding(0, ScaleValue(4), 0, ScaleValue(4));
        nameHost.Controls.Add(nameLayout);
        rightLayout.Controls.Add(nameHost, 0, 0);

        ConfigureSecondaryButton(_importToCloudButton, "将当前主配置导入到当前组", (_, _) => ImportCurrentIntoProfile(TargetMode.Cloud));
        _importToCloudButton.Margin = new Padding(0, ScaleValue(4), 0, ScaleValue(10));
        _importToCloudButton.Height = ScaleValue(30);
        rightLayout.Controls.Add(_importToCloudButton, 0, 1);

        rightLayout.Controls.Add(BuildClientSection("Codex", _cloudCodexBaseBox, _cloudCodexKeyBox, "http://127.0.0.1:8080/v1", "sk-..."), 0, 2);
        rightLayout.Controls.Add(BuildClientSection("Claude Code", _cloudClaudeBaseBox, _cloudClaudeKeyBox, "http://127.0.0.1:8080", "sk-..."), 0, 3);
        rightLayout.Controls.Add(BuildClientSection("Gemini CLI", _cloudGeminiBaseBox, _cloudGeminiKeyBox, "https://code-plan.site", "sk-..."), 0, 4);
        rightLayout.Controls.Add(BuildClientSection("Grok CLI", _cloudGrokBaseBox, _cloudGrokKeyBox, "https://api.x.ai/v1", "xai-..."), 0, 5);

        ConfigureTextBox(_cloudNotesBox, multiline: true);
        _cloudNotesBox.Dock = DockStyle.Top;
        _cloudNotesBox.Height = ScaleValue(60);
        _cloudNotesBox.PlaceholderText = "用途、供应商、维护说明";
        _cloudNotesBox.TextChanged += (_, _) => PersistCurrentCloudSourceFields();
        _cloudCodexBaseBox.TextChanged += (_, _) => PersistCurrentCloudSourceFields();
        _cloudCodexKeyBox.TextChanged += (_, _) => PersistCurrentCloudSourceFields();
        _cloudClaudeBaseBox.TextChanged += (_, _) => PersistCurrentCloudSourceFields();
        _cloudClaudeKeyBox.TextChanged += (_, _) => PersistCurrentCloudSourceFields();
        _cloudGeminiBaseBox.TextChanged += (_, _) => PersistCurrentCloudSourceFields();
        _cloudGeminiKeyBox.TextChanged += (_, _) => PersistCurrentCloudSourceFields();
        _cloudGrokBaseBox.TextChanged += (_, _) => PersistCurrentCloudSourceFields();
        _cloudGrokKeyBox.TextChanged += (_, _) => PersistCurrentCloudSourceFields();

        var notesHost = CreateSoftSection("备注说明");
        notesHost.Controls.Add(_cloudNotesBox);
        rightLayout.Controls.Add(notesHost, 0, 6);

        rightScroll.Controls.Add(rightLayout);

        mainSplit.Controls.Add(leftLayout, 0, 0);
        mainSplit.Controls.Add(rightScroll, 1, 0);
        
        page.Controls.Add(mainSplit);
        return page;
    }

    private TabPage BuildLocalSourcesPage()
    {
        var page = new TabPage("本地中转")
        {
            BackColor = Surface,
            Padding = new Padding(ScaleValue(10))
        };

        ConfigureComboBox(_localSourceBox);
        ConfigureTextBox(_localSourceNameBox);
        _localSourceNameBox.ReadOnly = true;
        _localSourceNameBox.TabStop = false;
        _localSourceNameBox.BackColor = SurfaceAlt;
        _localSourceBox.SelectedIndexChanged += (_, _) => LoadSelectedLocalSourceIntoFields();

        var mainSplit = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 2,
            RowCount = 1,
            BackColor = Surface
        };
        mainSplit.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 34F)); // Left: source selector
        mainSplit.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 66F)); // Right: inputs detail
        mainSplit.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        // --- Left Side (the two fixed local modes) ---
        var leftLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 3,
            BackColor = Surface
        };
        leftLayout.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(52)));
        leftLayout.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));
        leftLayout.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(30))); // Info Label

        ConfigureSourceGrid(_localSourcesGrid);
        
        _localSourcesGrid.SelectionChanged += (_, _) =>
        {
            if (_loadingSourceGrids || _localSourcesGrid.CurrentRow is null || _localSourcesGrid.CurrentRow.IsNewRow) return;
            var id = Convert.ToString(_localSourcesGrid.CurrentRow.Cells["Id"].Value);
            if (string.IsNullOrWhiteSpace(id)) return;
            SelectSourceOption(_localSourceBox, id);
        };

        var gridPanel = new Panel
        {
            Dock = DockStyle.Fill,
            Padding = new Padding(0, 0, ScaleValue(8), 0)
        };
        _localSourcesGrid.Dock = DockStyle.Fill;
        gridPanel.Controls.Add(_localSourcesGrid);
        leftLayout.Controls.Add(gridPanel, 0, 1);

        leftLayout.Controls.Add(CreateSectionHeader("固定来源", "仅保留本机中转与局域网中转"), 0, 0);

        var helpLabel = new Label
        {
            Text = "固定名称不可更改；仅编辑地址、密钥和备注。",
            ForeColor = TextMuted,
            Font = new Font("Microsoft YaHei UI", 8F),
            Dock = DockStyle.Fill,
            TextAlign = ContentAlignment.MiddleLeft
        };
        leftLayout.Controls.Add(helpLabel, 0, 2);

        // --- Right Side (Detailed Config Fields) ---
        var rightScroll = new Panel
        {
            Dock = DockStyle.Fill,
            AutoScroll = true,
            BackColor = Surface,
            Padding = new Padding(ScaleValue(8), 0, 0, 0)
        };

        var rightLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            ColumnCount = 1,
            RowCount = 6,
            BackColor = Surface,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink
        };
        for (var i = 0; i < 5; i++)
        {
            rightLayout.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        }

        var nameHost = CreateSoftSection("基本信息");
        var nameLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 2,
            RowCount = 1,
            BackColor = nameHost.BackColor
        };
        nameLayout.ColumnStyles.Add(CreateScaledAbsoluteColumn(80));
        nameLayout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        nameLayout.Controls.Add(CreateInputLabel("来源名称"), 0, 0);
        _localSourceNameValue.Dock = DockStyle.Fill;
        _localSourceNameValue.AutoSize = false;
        _localSourceNameValue.TextAlign = ContentAlignment.MiddleLeft;
        _localSourceNameValue.ForeColor = TextMain;
        _localSourceNameValue.Font = _strongFont;
        _localSourceNameValue.Margin = new Padding(0, ScaleValue(4), 0, ScaleValue(4));
        nameLayout.Controls.Add(_localSourceNameValue, 1, 0);
        nameHost.Controls.Add(nameLayout);
        rightLayout.Controls.Add(nameHost, 0, 0);

        ConfigureSecondaryButton(_importToLocalButton, "将当前主配置导入到当前来源", (_, _) => ImportCurrentIntoProfile(TargetMode.Local));
        _importToLocalButton.Margin = new Padding(0, ScaleValue(4), 0, ScaleValue(10));
        _importToLocalButton.Height = ScaleValue(30);
        rightLayout.Controls.Add(_importToLocalButton, 0, 1);

        rightLayout.Controls.Add(BuildClientSection("Codex", _localCodexBaseBox, _localCodexKeyBox, "http://127.0.0.1:8080/v1", "sk-..."), 0, 2);
        rightLayout.Controls.Add(BuildClientSection("Claude Code", _localClaudeBaseBox, _localClaudeKeyBox, "http://127.0.0.1:8080", "sk-..."), 0, 3);
        rightLayout.Controls.Add(BuildClientSection("Gemini CLI", _localGeminiBaseBox, _localGeminiKeyBox, "http://127.0.0.1:8080", "sk-..."), 0, 4);
        rightLayout.Controls.Add(BuildClientSection("Grok CLI", _localGrokBaseBox, _localGrokKeyBox, "http://127.0.0.1:8080/v1", "xai-..."), 0, 5);

        ConfigureTextBox(_localNotesBox, multiline: true);
        _localNotesBox.Dock = DockStyle.Top;
        _localNotesBox.Height = ScaleValue(60);
        _localNotesBox.PlaceholderText = "用途、机器 IP、维护说明";
        _localNotesBox.TextChanged += (_, _) => PersistCurrentLocalSourceFields();
        _localCodexBaseBox.TextChanged += (_, _) => PersistCurrentLocalSourceFields();
        _localCodexKeyBox.TextChanged += (_, _) => PersistCurrentLocalSourceFields();
        _localClaudeBaseBox.TextChanged += (_, _) => PersistCurrentLocalSourceFields();
        _localClaudeKeyBox.TextChanged += (_, _) => PersistCurrentLocalSourceFields();
        _localGeminiBaseBox.TextChanged += (_, _) => PersistCurrentLocalSourceFields();
        _localGeminiKeyBox.TextChanged += (_, _) => PersistCurrentLocalSourceFields();
        _localGrokBaseBox.TextChanged += (_, _) => PersistCurrentLocalSourceFields();
        _localGrokKeyBox.TextChanged += (_, _) => PersistCurrentLocalSourceFields();

        var notesHost = CreateSoftSection("备注说明");
        notesHost.Controls.Add(_localNotesBox);
        rightLayout.Controls.Add(notesHost, 0, 6);

        rightScroll.Controls.Add(rightLayout);

        mainSplit.Controls.Add(leftLayout, 0, 0);
        mainSplit.Controls.Add(rightScroll, 1, 0);
        
        page.Controls.Add(mainSplit);
        return page;
    }

    private void ConfigureSourceGrid(DataGridView grid)
    {
        if (grid.Columns.Count > 0)
        {
            return;
        }

        grid.AllowUserToAddRows = false; // We use the new buttons now!
        grid.AllowUserToDeleteRows = false;
        grid.AutoGenerateColumns = false;
        grid.AutoSizeColumnsMode = DataGridViewAutoSizeColumnsMode.Fill;
        grid.AutoSizeRowsMode = DataGridViewAutoSizeRowsMode.None;
        grid.BackgroundColor = Color.White;
        grid.BorderStyle = BorderStyle.None;
        grid.GridColor = Color.FromArgb(226, 232, 240);
        grid.RowHeadersVisible = false;
        grid.ScrollBars = ScrollBars.Vertical;
        grid.Font = _uiFont;
        grid.EnableHeadersVisualStyles = false;
        grid.ReadOnly = true;
        grid.MultiSelect = false;
        grid.SelectionMode = DataGridViewSelectionMode.FullRowSelect;
        grid.ColumnHeadersDefaultCellStyle.BackColor = Color.FromArgb(241, 245, 249);
        grid.ColumnHeadersDefaultCellStyle.ForeColor = TextMain;
        grid.ColumnHeadersDefaultCellStyle.Font = _strongFont;
        grid.ColumnHeadersBorderStyle = DataGridViewHeaderBorderStyle.Single;
        grid.AlternatingRowsDefaultCellStyle.BackColor = Color.FromArgb(248, 250, 252);
        grid.DefaultCellStyle.SelectionBackColor = PrimarySoft;
        grid.DefaultCellStyle.SelectionForeColor = TextMain;
        grid.DefaultCellStyle.WrapMode = DataGridViewTriState.False;
        grid.DefaultCellStyle.Padding = new Padding(ScaleValue(4), ScaleValue(5), ScaleValue(4), ScaleValue(5));
        grid.RowTemplate.Height = ScaleValue(40);
        grid.ColumnHeadersHeightSizeMode = DataGridViewColumnHeadersHeightSizeMode.AutoSize;
        grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "Id", HeaderText = "Id", Visible = false });
        grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "Name", HeaderText = "来源", FillWeight = 34F, MinimumWidth = ScaleValue(92) });
        grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "CodexBase", HeaderText = "配置摘要", FillWeight = 66F, MinimumWidth = ScaleValue(150) });
        grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "CodexKey", HeaderText = "Codex Key", Visible = false });
        grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "ClaudeBase", HeaderText = "Claude 地址", Visible = false });
        grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "ClaudeKey", HeaderText = "Claude Key", Visible = false });
        grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "GeminiBase", HeaderText = "Gemini 地址", Visible = false });
        grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "GeminiKey", HeaderText = "Gemini Key", Visible = false });
        grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "GrokBase", HeaderText = "Grok 地址", Visible = false });
        grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "GrokKey", HeaderText = "Grok Key", Visible = false });
        grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "Notes", HeaderText = "备注", Visible = false });
    }

    private Control CreateSourceToolbar(string title, Button addButton, Button deleteButton)
    {
        var host = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 2,
            BackColor = Surface,
            Padding = new Padding(0, 0, ScaleValue(8), ScaleValue(6))
        };
        host.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        host.RowStyles.Add(new RowStyle(SizeType.Absolute, ScaleValue(24)));
        host.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        host.Controls.Add(new Label
        {
            Text = title,
            Dock = DockStyle.Fill,
            ForeColor = TextMain,
            Font = _strongFont,
            TextAlign = ContentAlignment.MiddleLeft,
            Margin = Padding.Empty
        }, 0, 0);

        var actions = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 2,
            RowCount = 1,
            BackColor = Surface,
            Margin = Padding.Empty
        };
        actions.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 62F));
        actions.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 38F));
        actions.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        addButton.Dock = DockStyle.Fill;
        deleteButton.Dock = DockStyle.Fill;
        addButton.Margin = new Padding(0, 0, ScaleValue(6), 0);
        deleteButton.Margin = Padding.Empty;
        actions.Controls.Add(addButton, 0, 0);
        actions.Controls.Add(deleteButton, 1, 0);
        host.Controls.Add(actions, 0, 1);
        return host;
    }

    private TabPage BuildMixedPage()
    {
        var page = new TabPage("混合模式")
        {
            BackColor = Surface,
            Padding = new Padding(ScaleValue(14))
        };

        ConfigureComboBox(_mixedCodexSourceBox);
        ConfigureComboBox(_mixedClaudeSourceBox);
        ConfigureComboBox(_mixedGeminiSourceBox);
        ConfigureComboBox(_mixedGrokSourceBox);
        _mixedCodexSourceBox.Items.Clear();
        _mixedClaudeSourceBox.Items.Clear();
        _mixedGeminiSourceBox.Items.Clear();
        _mixedGrokSourceBox.Items.Clear();

        var pageScroll = new Panel
        {
            Dock = DockStyle.Fill,
            AutoScroll = true,
            BackColor = Surface
        };

        var root = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            ColumnCount = 1,
            RowCount = 4,
            BackColor = Surface,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink
        };
        for (var i = 0; i < 4; i++)
        {
            root.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        }

        root.Controls.Add(new Label
        {
            Text = "混合模式不会维护独立地址和 Key，而是从云端 / 本机 / 局域网 profile 里选择不同的来源组合。",
            AutoSize = true,
            MaximumSize = ScaleSize(new Size(620, 0)),
            ForeColor = TextMuted,
            Font = _uiFont,
            Margin = new Padding(ScaleValue(4), 0, 0, ScaleValue(12))
        }, 0, 0);

        var sourceHost = CreateSoftSection("来源指定");
        var sourceLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 2,
            RowCount = 4,
            BackColor = sourceHost.BackColor
        };
        sourceLayout.ColumnStyles.Add(CreateScaledAbsoluteColumn(110));
        sourceLayout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));

        sourceLayout.Controls.Add(CreateInputLabel("Codex 来源"), 0, 0);
        sourceLayout.Controls.Add(_mixedCodexSourceBox, 1, 0);
        _mixedCodexSourceBox.Margin = new Padding(0, ScaleValue(8), 0, ScaleValue(10));
        _mixedCodexSourceBox.Dock = DockStyle.Left;

        sourceLayout.Controls.Add(CreateInputLabel("Claude 来源"), 0, 1);
        sourceLayout.Controls.Add(_mixedClaudeSourceBox, 1, 1);
        _mixedClaudeSourceBox.Margin = new Padding(0, ScaleValue(8), 0, ScaleValue(10));
        _mixedClaudeSourceBox.Dock = DockStyle.Left;

        sourceLayout.Controls.Add(CreateInputLabel("Gemini 来源"), 0, 2);
        sourceLayout.Controls.Add(_mixedGeminiSourceBox, 1, 2);
        _mixedGeminiSourceBox.Margin = new Padding(0, ScaleValue(8), 0, ScaleValue(10));
        _mixedGeminiSourceBox.Dock = DockStyle.Left;

        sourceLayout.Controls.Add(CreateInputLabel("Grok 来源"), 0, 3);
        sourceLayout.Controls.Add(_mixedGrokSourceBox, 1, 3);
        _mixedGrokSourceBox.Margin = new Padding(0, ScaleValue(8), 0, ScaleValue(10));
        _mixedGrokSourceBox.Dock = DockStyle.Left;

        sourceHost.Controls.Add(sourceLayout);
        root.Controls.Add(sourceHost, 0, 1);

        var previewHost = CreateSoftSection("组合预览");
        var previewLabel = new Label
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            ForeColor = TextMain,
            Font = _strongFont,
            Padding = new Padding(0, ScaleValue(6), 0, ScaleValue(6))
        };
        previewHost.Controls.Add(previewLabel);
        root.Controls.Add(previewHost, 0, 2);

        _mixedCodexSourceBox.SelectedIndexChanged += (_, _) => previewLabel.Text = GetMixedSummaryText();
        _mixedClaudeSourceBox.SelectedIndexChanged += (_, _) => previewLabel.Text = GetMixedSummaryText();
        _mixedGeminiSourceBox.SelectedIndexChanged += (_, _) => previewLabel.Text = GetMixedSummaryText();
        _mixedGrokSourceBox.SelectedIndexChanged += (_, _) => previewLabel.Text = GetMixedSummaryText();
        previewLabel.Text = GetMixedSummaryText();

        pageScroll.Controls.Add(root);
        page.Controls.Add(pageScroll);
        return page;
    }

    private Control BuildClientSection(string title, TextBox baseBox, TextBox keyBox, string basePlaceholder, string keyPlaceholder)
    {
        ConfigureTextBox(baseBox);
        baseBox.PlaceholderText = basePlaceholder;

        ConfigureSecretBox(keyBox);
        keyBox.PlaceholderText = keyPlaceholder;

        var host = CreateSoftSection(title);
        var layout = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 3,
            RowCount = 2,
            BackColor = host.BackColor
        };
        layout.ColumnStyles.Add(CreateScaledAbsoluteColumn(92));
        layout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        layout.ColumnStyles.Add(CreateScaledAbsoluteColumn(88));

        AddField(layout, "地址", baseBox, null, 0);
        AddField(layout, "Key", keyBox, CreateToggleButton(keyBox), 1);

        host.Controls.Add(layout);
        return host;
    }

    private Control BuildActionsCard()
    {
        var card = CreateCardPanel();
        card.Dock = DockStyle.Fill;
        card.Padding = new Padding(ScaleValue(18), ScaleValue(14), ScaleValue(18), ScaleValue(18));

        var layout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 2,
            BackColor = Surface
        };
        layout.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        layout.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        layout.Controls.Add(CreateSectionHeader("操作与验证", "主按钮只做一件事：应用当前标签页对应的模式。"), 0, 0);

        var scrollHost = new Panel
        {
            Dock = DockStyle.Fill,
            AutoScroll = true,
            BackColor = Surface,
            Margin = new Padding(0, ScaleValue(10), 0, 0)
        };

        var stack = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 1,
            RowCount = 4,
            BackColor = Surface
        };
        stack.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        stack.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        stack.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        stack.RowStyles.Add(new RowStyle(SizeType.AutoSize));

        _pendingModeValue.AutoSize = true;
        _pendingModeValue.MaximumSize = ScaleSize(new Size(280, 0));
        _pendingModeValue.ForeColor = TextMain;
        _pendingModeValue.Font = _strongFont;
        _pendingModeValue.Margin = new Padding(0, ScaleValue(4), 0, ScaleValue(2));

        _pendingDetailValue.AutoSize = true;
        _pendingDetailValue.MaximumSize = ScaleSize(new Size(280, 0));
        _pendingDetailValue.ForeColor = TextMuted;
        _pendingDetailValue.Font = _uiFont;
        _pendingDetailValue.Margin = Padding.Empty;

        var pendingHost = CreateSoftSection("当前将执行");
        var pendingFlow = new FlowLayoutPanel
        {
            Dock = DockStyle.Top,
            FlowDirection = FlowDirection.TopDown,
            WrapContents = false,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            BackColor = pendingHost.BackColor,
            Margin = Padding.Empty
        };
        pendingFlow.Controls.Add(_pendingModeValue);
        pendingFlow.Controls.Add(_pendingDetailValue);
        pendingHost.Controls.Add(pendingFlow);
        stack.Controls.Add(pendingHost, 0, 1);

        var settingsHost = CreateSoftSection("设置");
        _closeToTrayCheckBox.Text = "点击关闭按钮时收起到任务栏小图标";
        _closeToTrayCheckBox.Checked = _settings.CloseToTrayOnClose;
        _closeToTrayCheckBox.AutoSize = true;
        _closeToTrayCheckBox.Dock = DockStyle.Top;
        _closeToTrayCheckBox.ForeColor = TextMain;
        _closeToTrayCheckBox.Font = _uiFont;
        _closeToTrayCheckBox.Margin = new Padding(0, ScaleValue(4), 0, ScaleValue(4));
        _closeToTrayCheckBox.CheckedChanged += (_, _) =>
        {
            _settings.CloseToTrayOnClose = _closeToTrayCheckBox.Checked;
            _repository.SaveSettings(_settings);
            AppendLine(_settings.CloseToTrayOnClose
                ? "设置已保存：关闭按钮会收起到任务栏小图标。"
                : "设置已保存：关闭按钮会直接退出并恢复配置。");
        };
        settingsHost.Controls.Add(_closeToTrayCheckBox);
        stack.Controls.Add(settingsHost, 0, 2);

        var toolsHost = CreateSoftSection("主要操作");
        var actions = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 2,
            RowCount = 3,
            BackColor = toolsHost.BackColor
        };
        actions.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 50F));
        actions.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 50F));
        actions.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        actions.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        actions.RowStyles.Add(new RowStyle(SizeType.AutoSize));

        ConfigurePrimaryButton(_applySelectedButton, "应用当前模式", Primary, async (_, _) => await RunActionAsync(() => SwitchAsync(GetSelectedTargetMode())));
        ConfigureSecondaryButton(_testSelectedButton, "测试当前模式", async (_, _) => await RunActionAsync(() => TestSelectedAsync(GetSelectedTargetMode())));
        ConfigureSecondaryButton(_saveButton, "保存配置", async (_, _) => await RunActionAsync(SaveProfilesOnlyAsync));
        ConfigureSecondaryButton(_openSelectedSiteButton, "打开当前站点", (_, _) => OpenProfileSite(GetSelectedTargetMode()));

        actions.Controls.Add(_applySelectedButton, 0, 0);
        actions.SetColumnSpan(_applySelectedButton, 2);
        actions.Controls.Add(_testSelectedButton, 0, 1);
        actions.Controls.Add(_saveButton, 1, 1);
        actions.Controls.Add(_openSelectedSiteButton, 0, 2);
        actions.SetColumnSpan(_openSelectedSiteButton, 2);

        toolsHost.Controls.Add(actions);
        stack.Controls.Add(toolsHost, 0, 0);

        scrollHost.Controls.Add(stack);
        layout.Controls.Add(scrollHost, 0, 1);
        card.Controls.Add(layout);
        return card;
    }

    private Control BuildLocalGatewayCard()
    {
        var mainLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 2,
            RowCount = 1,
            BackColor = AppBg,
            Padding = Padding.Empty,
            Margin = Padding.Empty
        };
        mainLayout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 55F)); // Left Column: Services list (Terminal)
        mainLayout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 45F)); // Right Column: Control & metrics

        // --- Left Side: Services List Terminal Card ---
        var terminalCard = CreateCardPanel();
        terminalCard.Dock = DockStyle.Fill;
        terminalCard.Padding = new Padding(ScaleValue(18), ScaleValue(14), ScaleValue(18), ScaleValue(14));
        terminalCard.Margin = new Padding(0, 0, ScaleValue(14), 0);

        var terminalLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 2,
            BackColor = Surface
        };
        terminalLayout.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        terminalLayout.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        terminalLayout.Controls.Add(CreateSectionHeader("服务运行状态", "显示当前原生服务或 Docker 容器及其端口。"), 0, 0);

        _localGatewayServicesBox.Dock = DockStyle.Fill;
        _localGatewayServicesBox.Multiline = true;
        _localGatewayServicesBox.ScrollBars = ScrollBars.Vertical;
        _localGatewayServicesBox.ReadOnly = true;
        _localGatewayServicesBox.BorderStyle = BorderStyle.None;
        _localGatewayServicesBox.BackColor = Color.FromArgb(15, 23, 42); // slate-900 (Dark theme terminal)
        _localGatewayServicesBox.ForeColor = Color.FromArgb(74, 222, 128); // green-400
        _localGatewayServicesBox.Font = _monoFont;

        var servicesHost = new Panel
        {
            Dock = DockStyle.Fill,
            Padding = new Padding(ScaleValue(10)),
            BackColor = Color.FromArgb(15, 23, 42), // Dark theme
            Margin = new Padding(0, ScaleValue(10), 0, 0)
        };
        servicesHost.Paint += DrawBorder;
        servicesHost.Controls.Add(_localGatewayServicesBox);
        terminalLayout.Controls.Add(servicesHost, 0, 1);
        terminalCard.Controls.Add(terminalLayout);

        // --- Right Side: Control & Metrics Card ---
        var controlCard = CreateCardPanel();
        controlCard.Dock = DockStyle.Fill;
        controlCard.Padding = new Padding(ScaleValue(18), ScaleValue(14), ScaleValue(18), ScaleValue(14));
        controlCard.Margin = Padding.Empty;

        var controlLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 3,
            BackColor = Surface
        };
        controlLayout.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        controlLayout.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        controlLayout.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        controlLayout.Controls.Add(CreateSectionHeader("本地中转控制台", "一键启动、停止或管理本地 Sub2API。"), 0, 0);

        var metrics = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 1,
            RowCount = 3,
            BackColor = Surface,
            Margin = new Padding(0, ScaleValue(10), 0, ScaleValue(14))
        };
        metrics.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        metrics.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        metrics.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        metrics.Controls.Add(CreateCompactMetric("服务状态", _localGatewayStatusValue), 0, 0);
        metrics.Controls.Add(CreateCompactMetric("管理后台", _localGatewayWebValue), 0, 1);
        metrics.Controls.Add(CreateCompactMetric("启动配置", _localGatewayComposeValue), 0, 2);
        controlLayout.Controls.Add(metrics, 0, 1);

        var actionsScroll = new Panel
        {
            Dock = DockStyle.Fill,
            AutoScroll = true,
            BackColor = Surface
        };

        var actions = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 2,
            RowCount = 5,
            BackColor = Surface
        };
        actions.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 50F));
        actions.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 50F));
        for (var i = 0; i < actions.RowCount; i++)
        {
            actions.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        }

        ConfigurePrimaryButton(_localGatewayStartButton, "启动并打开", Success, async (_, _) => await RunActionAsync(ToggleLocalGatewayAsync));
        _localGatewayStartButton.Height = ScaleValue(36);
        _localGatewayStartButton.Margin = new Padding(0, 0, 0, ScaleValue(8));
        
        ConfigureSecondaryButton(_localGatewayRefreshButton, "刷新状态", async (_, _) => await RunActionAsync(RefreshLocalGatewayStatusAsync));
        ConfigureSecondaryButton(_localGatewayOpenAdminButton, "打开后台", (_, _) => OpenLocalGatewayUrl("/dashboard", "Sub2API 后台"));
        ConfigureSecondaryButton(_localGatewayOpenLanAdminButton, "打开已配置局域网后台", async (_, _) => await RunActionAsync(() => OpenLanGatewayUrlAsync("/dashboard", "局域网中转后台")));
        ConfigureSecondaryButton(_convertCpaAccountsButton, "账号转/合并 Sub2API", (_, _) => ConvertCpaAccounts());
        ConfigureSecondaryButton(_localGatewayRestartButton, "重启服务", async (_, _) => await RunActionAsync(() => RunLocalGatewayCommandAsync("重启", _localGatewayService.RestartAsync)));
        ConfigureSecondaryButton(_localGatewayStopButton, "停止服务", async (_, _) => await RunActionAsync(() => RunLocalGatewayCommandAsync("停止", _localGatewayService.StopAsync)));

        foreach (var button in new[]
                 {
                     _localGatewayRefreshButton,
                     _localGatewayOpenAdminButton,
                     _localGatewayOpenLanAdminButton,
                     _convertCpaAccountsButton,
                     _localGatewayRestartButton,
                     _localGatewayStopButton
                 })
        {
            button.Height = ScaleValue(32);
            button.Margin = new Padding(0, 0, ScaleValue(8), ScaleValue(6));
        }

        actions.Controls.Add(_localGatewayStartButton, 0, 0);
        actions.SetColumnSpan(_localGatewayStartButton, 2);
        actions.Controls.Add(_localGatewayRefreshButton, 0, 1);
        actions.Controls.Add(_localGatewayOpenAdminButton, 1, 1);
        actions.Controls.Add(_localGatewayOpenLanAdminButton, 0, 2);
        actions.SetColumnSpan(_localGatewayOpenLanAdminButton, 2);
        actions.Controls.Add(_convertCpaAccountsButton, 0, 3);
        actions.SetColumnSpan(_convertCpaAccountsButton, 2);
        actions.Controls.Add(_localGatewayRestartButton, 0, 4);
        actions.Controls.Add(_localGatewayStopButton, 1, 4);
        
        actionsScroll.Controls.Add(actions);
        controlLayout.Controls.Add(actionsScroll, 0, 2);
        controlCard.Controls.Add(controlLayout);

        mainLayout.Controls.Add(terminalCard, 0, 0);
        mainLayout.Controls.Add(controlCard, 1, 0);

        _localGatewayStatusValue.Text = "等待刷新";
        _localGatewayWebValue.Text = _localGatewayService.WebUrl;
        _localGatewayComposeValue.Text = _localGatewayService.UsesNativeControl
            ? "原生 Windows 模式"
            : _localGatewayService.ComposeFile is null ? "未找到" : "Docker Compose";
        _localGatewayServicesBox.Text = "点击“刷新状态”查看服务。";

        return mainLayout;
    }

    private Control BuildLogCard()
    {
        var card = CreateCardPanel();
        card.Dock = DockStyle.Fill;
        card.Padding = new Padding(ScaleValue(18), ScaleValue(14), ScaleValue(18), ScaleValue(18));

        var layout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 2,
            BackColor = Surface
        };
        layout.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        layout.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        // Create Section Header with a Clear Logs button
        var headerLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            ColumnCount = 2,
            RowCount = 1,
            AutoSize = true,
            BackColor = Surface
        };
        headerLayout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        headerLayout.ColumnStyles.Add(CreateScaledAbsoluteColumn(100));

        headerLayout.Controls.Add(CreateSectionHeader("运行日志", "保存、切换、验证、恢复等系统操作均在此实时记录。"), 0, 0);

        var clearBtn = new Button();
        ConfigureSecondaryButton(clearBtn, "清空日志", (s, e) => _statusBox.Clear());
        clearBtn.Height = ScaleValue(28);
        clearBtn.Margin = new Padding(0, ScaleValue(4), 0, 0);
        headerLayout.Controls.Add(clearBtn, 1, 0);

        layout.Controls.Add(headerLayout, 0, 0);

        _statusBox.Dock = DockStyle.Fill;
        _statusBox.Multiline = true;
        _statusBox.ScrollBars = ScrollBars.Vertical;
        _statusBox.ReadOnly = true;
        _statusBox.BorderStyle = BorderStyle.None;
        _statusBox.BackColor = Color.FromArgb(15, 23, 42); // slate-900 (Dark terminal)
        _statusBox.ForeColor = Color.FromArgb(241, 245, 249); // slate-100
        _statusBox.Font = _monoFont;

        var logHost = new Panel
        {
            Dock = DockStyle.Fill,
            Padding = new Padding(ScaleValue(10)),
            BackColor = Color.FromArgb(15, 23, 42),
            Margin = new Padding(0, ScaleValue(12), 0, 0)
        };
        logHost.Paint += DrawBorder;
        logHost.Controls.Add(_statusBox);
        layout.Controls.Add(logHost, 0, 1);

        card.Controls.Add(layout);
        return card;
    }

    private Control BuildTrafficStatsCard()
    {
        var card = CreateCardPanel();
        card.Dock = DockStyle.Fill;
        card.Padding = new Padding(ScaleValue(18), ScaleValue(14), ScaleValue(18), ScaleValue(18));

        var layout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 1,
            RowCount = 2,
            BackColor = Surface
        };
        layout.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        layout.RowStyles.Add(new RowStyle(SizeType.Percent, 100F));

        layout.Controls.Add(CreateSectionHeader("流量统计", "登录中转账号，查看本账号的真实用量与消费。"), 0, 0);

        var scrollHost = new Panel
        {
            Dock = DockStyle.Fill,
            AutoScroll = true,
            BackColor = Surface,
            Margin = new Padding(0, ScaleValue(10), 0, 0)
        };

        var stack = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 1,
            RowCount = 4,
            BackColor = Surface
        };
        for (var i = 0; i < 4; i++)
        {
            stack.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        }

        // ── 账号登录区 ──
        ConfigureTextBox(_statsGatewayBox);
        _statsGatewayBox.ReadOnly = true;
        _statsGatewayBox.BackColor = SurfaceAlt;
        _statsGatewayBox.Text = _settings.Stats.GatewayBaseUrl;
        ConfigureTextBox(_statsEmailBox);
        _statsEmailBox.Text = _settings.Stats.Email;
        _statsEmailBox.PlaceholderText = "账号邮箱";
        ConfigureSecretBox(_statsPasswordBox);
        _statsPasswordBox.Text = _settings.Stats.Password;
        _statsPasswordBox.PlaceholderText = "密码";

        var accountHost = CreateSoftSection("账号");
        var accountLayout = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 3,
            RowCount = 3,
            BackColor = accountHost.BackColor
        };
        accountLayout.ColumnStyles.Add(CreateScaledAbsoluteColumn(72));
        accountLayout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        accountLayout.ColumnStyles.Add(CreateScaledAbsoluteColumn(1));
        AddField(accountLayout, "网关(自动)", _statsGatewayBox, null, 0);
        AddField(accountLayout, "邮箱", _statsEmailBox, null, 1);
        AddField(accountLayout, "密码", _statsPasswordBox, null, 2);
        accountHost.Controls.Add(accountLayout);
        stack.Controls.Add(accountHost, 0, 0);

        ConfigurePrimaryButton(_statsRefreshButton, "登录并刷新统计", Primary, async (_, _) => await RunActionAsync(RefreshTrafficStatsAsync));
        _statsRefreshButton.Margin = new Padding(0, 0, 0, ScaleValue(6));
        stack.Controls.Add(_statsRefreshButton, 0, 1);

        _statsHintValue.AutoSize = true;
        _statsHintValue.MaximumSize = ScaleSize(new Size(900, 0));
        _statsHintValue.ForeColor = TextMuted;
        _statsHintValue.Font = _uiFont;
        _statsHintValue.Margin = new Padding(ScaleValue(2), 0, 0, ScaleValue(10));
        _statsHintValue.Text = string.IsNullOrWhiteSpace(_settings.Stats.Email)
            ? "请填写账号后点击“登录并刷新统计”。"
            : "点击“登录并刷新统计”获取最新用量。";
        stack.Controls.Add(_statsHintValue, 0, 2);

        // ── 总览 + 明细 + 趋势 ──
        var content = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 1,
            RowCount = 3,
            BackColor = Surface
        };
        for (var i = 0; i < 3; i++)
        {
            content.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        }

        var overviewHost = CreateSoftSection("总览");
        var metrics = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 2,
            RowCount = 4,
            BackColor = overviewHost.BackColor
        };
        metrics.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 50F));
        metrics.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 50F));
        for (var i = 0; i < 4; i++)
        {
            metrics.RowStyles.Add(new RowStyle(SizeType.AutoSize));
        }
        metrics.Controls.Add(CreateMetricTile("总请求", _statsTotalRequestsValue), 0, 0);
        metrics.Controls.Add(CreateMetricTile("今日请求", _statsTodayRequestsValue), 1, 0);
        metrics.Controls.Add(CreateMetricTile("总 Token", _statsTotalTokensValue), 0, 1);
        metrics.Controls.Add(CreateMetricTile("今日 Token", _statsTodayTokensValue), 1, 1);
        metrics.Controls.Add(CreateMetricTile("总消费", _statsTotalCostValue), 0, 2);
        metrics.Controls.Add(CreateMetricTile("今日消费", _statsTodayCostValue), 1, 2);
        metrics.Controls.Add(CreateMetricTile("缓存读取", _statsCacheReadValue), 0, 3);
        metrics.Controls.Add(CreateMetricTile("平均耗时", _statsAvgDurationValue), 1, 3);
        overviewHost.Controls.Add(metrics);
        content.Controls.Add(overviewHost, 0, 0);

        ConfigureStatsGrid(_statsModelsGrid,
            ("Model", "模型", 150),
            ("Requests", "请求", 80),
            ("InputTokens", "输入", 110),
            ("OutputTokens", "输出", 100),
            ("CacheReadTokens", "缓存读取", 120),
            ("TotalTokens", "总Token", 120),
            ("Cost", "消费$", 0));
        var modelsHost = CreateSoftSection("按模型明细");
        _statsModelsGrid.Dock = DockStyle.Top;
        _statsModelsGrid.Height = ScaleValue(160);
        modelsHost.Controls.Add(_statsModelsGrid);
        content.Controls.Add(modelsHost, 0, 1);

        ConfigureStatsGrid(_statsTrendGrid,
            ("Date", "日期", 110),
            ("Requests", "请求", 90),
            ("TotalTokens", "总Token", 130),
            ("Cost", "消费$", 100),
            ("ActualCost", "实际$", 0));
        var trendHost = CreateSoftSection("用量趋势（按日）");
        _statsTrendGrid.Dock = DockStyle.Top;
        _statsTrendGrid.Height = ScaleValue(200);
        trendHost.Controls.Add(_statsTrendGrid);
        content.Controls.Add(trendHost, 0, 2);

        stack.Controls.Add(content, 0, 3);

        scrollHost.Controls.Add(stack);
        layout.Controls.Add(scrollHost, 0, 1);
        card.Controls.Add(layout);
        return card;
    }

    private void ConfigureStatsGrid(DataGridView grid, params (string Name, string Header, int Width)[] columns)
    {
        if (grid.Columns.Count > 0)
        {
            return;
        }

        grid.AllowUserToAddRows = false;
        grid.AllowUserToDeleteRows = false;
        grid.ReadOnly = true;
        grid.AutoGenerateColumns = false;
        grid.BackgroundColor = Color.White;
        grid.BorderStyle = BorderStyle.None;
        grid.GridColor = Color.FromArgb(224, 231, 241);
        grid.RowHeadersVisible = false;
        grid.Font = _uiFont;
        grid.EnableHeadersVisualStyles = false;
        grid.ColumnHeadersDefaultCellStyle.BackColor = Color.FromArgb(232, 239, 249);
        grid.ColumnHeadersDefaultCellStyle.ForeColor = TextMain;
        grid.ColumnHeadersDefaultCellStyle.Font = _strongFont;
        grid.AlternatingRowsDefaultCellStyle.BackColor = Color.FromArgb(248, 251, 255);
        grid.DefaultCellStyle.SelectionBackColor = PrimarySoft;
        grid.DefaultCellStyle.SelectionForeColor = TextMain;
        grid.ColumnHeadersHeightSizeMode = DataGridViewColumnHeadersHeightSizeMode.AutoSize;

        foreach (var (name, header, width) in columns)
        {
            var col = new DataGridViewTextBoxColumn { Name = name, HeaderText = header };
            if (width > 0)
            {
                col.Width = ScaleValue(width);
            }
            else
            {
                col.AutoSizeMode = DataGridViewAutoSizeColumnMode.Fill;
            }
            grid.Columns.Add(col);
        }
    }

    // 网关地址自动取自当前实际生效的中转配置，无需手动指定。
    private string ResolveGatewayBaseUrl()
    {
        try
        {
            var current = _switchService.ReadCurrentClientConfig();
            var candidate = !string.IsNullOrWhiteSpace(current.Codex?.BaseUrl)
                ? current.Codex!.BaseUrl
                : current.Claude?.BaseUrl;
            var origin = ToOriginUrl(candidate);
            if (!string.IsNullOrWhiteSpace(origin))
            {
                return origin;
            }
        }
        catch
        {
            // 读取失败时回退到上次保存的网关。
        }

        return _settings.Stats.GatewayBaseUrl;
    }

    private static string ToOriginUrl(string? url)
    {
        if (string.IsNullOrWhiteSpace(url))
        {
            return string.Empty;
        }

        var trimmed = url.Trim();
        if (!trimmed.Contains("://"))
        {
            trimmed = "http://" + trimmed;
        }

        if (Uri.TryCreate(trimmed, UriKind.Absolute, out var uri))
        {
            var portPart = uri.IsDefaultPort ? string.Empty : $":{uri.Port}";
            return $"{uri.Scheme}://{uri.Host}{portPart}";
        }

        return string.Empty;
    }

    private void UpdateStatsGatewayDisplay()
    {
        var gateway = ResolveGatewayBaseUrl();
        _settings.Stats.GatewayBaseUrl = gateway;
        _statsGatewayBox.Text = gateway;
    }

    private async Task RefreshTrafficStatsAsync()
    {
        UpdateStatsGatewayDisplay();
        _settings.Stats.Email = _statsEmailBox.Text.Trim();
        _settings.Stats.Password = _statsPasswordBox.Text;
        _repository.SaveSettings(_settings);
        _statsService.UpdateSettings(_settings.Stats);

        _statsHintValue.ForeColor = TextMuted;
        _statsHintValue.Text = "正在登录并读取统计...";
        AppendLine($"开始读取流量统计：{_settings.Stats.Email} @ {_settings.Stats.GatewayBaseUrl}");

        try
        {
            var overview = await _statsService.GetOverviewAsync(CancellationToken.None);
            var models = await _statsService.GetModelsAsync(CancellationToken.None);
            var trend = await _statsService.GetTrendAsync(CancellationToken.None);

            _statsTotalRequestsValue.Text = FormatCount(overview.TotalRequests);
            _statsTodayRequestsValue.Text = FormatCount(overview.TodayRequests);
            _statsTotalTokensValue.Text = FormatCount(overview.TotalTokens);
            _statsTodayTokensValue.Text = FormatCount(overview.TodayTokens);
            _statsTotalCostValue.Text = $"${overview.TotalCost:N2}";
            _statsTodayCostValue.Text = $"${overview.TodayCost:N2}";
            _statsCacheReadValue.Text = FormatCount(overview.TotalCacheReadTokens);
            _statsAvgDurationValue.Text = $"{overview.AverageDurationMs / 1000.0:N1}s";

            _statsModelsGrid.Rows.Clear();
            foreach (var m in models)
            {
                _statsModelsGrid.Rows.Add(
                    m.Model,
                    FormatCount(m.Requests),
                    FormatCount(m.InputTokens),
                    FormatCount(m.OutputTokens),
                    FormatCount(m.CacheReadTokens),
                    FormatCount(m.TotalTokens),
                    $"${m.Cost:N2}");
            }

            _statsTrendGrid.Rows.Clear();
            foreach (var t in trend)
            {
                _statsTrendGrid.Rows.Add(
                    t.Date,
                    FormatCount(t.Requests),
                    FormatCount(t.TotalTokens),
                    $"${t.Cost:N2}",
                    $"${t.ActualCost:N2}");
            }

            _statsHintValue.ForeColor = Success;
            _statsHintValue.Text = $"已更新：{_settings.Stats.Email} · {overview.TotalApiKeys} 个 API Key · 总消费 ${overview.TotalCost:N2}（实际 ${overview.TotalActualCost:N2}）。";
            AppendLine($"流量统计已更新：总请求 {overview.TotalRequests:N0}，总 Token {overview.TotalTokens:N0}，总消费 ${overview.TotalCost:N2}。");
        }
        catch (Exception ex)
        {
            _statsHintValue.ForeColor = Danger;
            _statsHintValue.Text = $"读取失败：{ex.Message}";
            AppendLine($"流量统计读取失败：{ex.Message}");
        }
    }

    private static string FormatCount(long value)
    {
        if (value >= 100_000_000) return $"{value / 100_000_000.0:N2}亿";
        if (value >= 10_000) return $"{value / 10_000.0:N2}万";
        return value.ToString("N0");
    }

    private Control BuildFooter()
    {
        var userProfile = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        return new Label
        {
            Dock = DockStyle.Fill,
            Text = $@"提示：主配置写入 {Path.Combine(userProfile, ".codex")} 和 {Path.Combine(userProfile, ".claude")}。",
            ForeColor = TextMuted,
            TextAlign = ContentAlignment.MiddleLeft,
            Font = _uiFont,
            Margin = new Padding(ScaleValue(4), ScaleValue(4), 0, 0)
        };
    }

    private async Task SaveProfilesOnlyAsync()
    {
        _currentStore = CollectProfilesFromForm();
        _repository.SaveProfiles(_currentStore);
        var result = await _switchService.SaveOnlyAsync(_currentStore, CancellationToken.None);
        AppendResult(result);
        RefreshLiveStatus();
        UpdatePendingActionSummary();
    }

    private async Task SwitchAsync(TargetMode mode)
    {
        _currentStore = CollectProfilesFromForm();
        _repository.SaveProfiles(_currentStore);
        _pendingDetailValue.Text = "正在后台写入客户端配置与环境变量，请稍候…";
        AppendLine("正在后台切换配置（界面保持可响应）…");
        var result = await _switchService.SwitchAsync(_currentStore, mode, CancellationToken.None);
        if (result.Success)
        {
            _currentStore.ActiveConnectionProfileId = ResolveActiveConnectionProfileId(_currentStore, mode);
            _repository.SaveProfiles(_currentStore);
        }

        AppendResult(result);
        RefreshLiveStatus();
        UpdatePendingActionSummary();
    }

    private async Task TestSelectedAsync(TargetMode mode)
    {
        _currentStore = CollectProfilesFromForm();
        _repository.SaveProfiles(_currentStore);
        AppendLine("开始测试当前模式，测试不会写入主配置...");
        var result = await _switchService.ValidateProfileAsync(_currentStore, mode, CancellationToken.None);
        AppendResult(result);
        UpdatePendingActionSummary();
    }

    private Task ReloadStatusAsync()
    {
        _currentStore = _repository.LoadProfiles();
        LoadProfilesIntoForm(_currentStore);
        var status = RefreshLiveStatus();
        UpdatePendingActionSummary();
        AppendLine($"已从 profiles.json 重新加载，并刷新当前状态: {status.ActiveTarget} - {status.Summary}");
        return Task.CompletedTask;
    }

    private Task RestoreBackupAsync()
    {
        var result = _switchService.RestoreLatestBackup();
        AppendResult(result);
        RefreshLiveStatus();
        UpdatePendingActionSummary();
        return Task.CompletedTask;
    }

    private ClientProfile? ResolveCodexApiFallback(ImportedLiveConfig current)
    {
        return EnumerateFallbackCandidates(current, isCodex: true)
            .Where(IsUsableApiFallback)
            .FirstOrDefault();
    }

    private ClientProfile? ResolveClaudeApiFallback(ImportedLiveConfig current)
    {
        return EnumerateFallbackCandidates(current, isCodex: false)
            .Where(IsUsableApiFallback)
            .OrderBy(profile => IsAnthropicOfficialBaseUrl(profile.BaseUrl) ? 1 : 0)
            .FirstOrDefault(profile => !IsAnthropicOfficialBaseUrl(profile.BaseUrl) || IsClaudeOfficialApiKey(profile.Secret));
    }

    private IEnumerable<ClientProfile> EnumerateFallbackCandidates(ImportedLiveConfig current, bool isCodex)
    {
        var seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);

        if (isCodex && current.Codex is not null)
        {
            yield return CloneClientProfile(current.Codex);
            seen.Add(BuildCandidateKey(current.Codex));
        }
        else if (!isCodex && current.Claude is not null)
        {
            yield return CloneClientProfile(current.Claude);
            seen.Add(BuildCandidateKey(current.Claude));
        }

        foreach (var source in EnumerateProfileSources(_currentStore))
        {
            var profile = isCodex ? source.Codex : source.Claude;
            var key = BuildCandidateKey(profile);
            if (seen.Add(key))
            {
                yield return CloneClientProfile(profile);
            }
        }
    }

    private static IEnumerable<ProfileDefinition> EnumerateProfileSources(ProfileStore store)
    {
        yield return store.Cloud;
        yield return store.Local;
        yield return store.Lan;
        foreach (var source in store.CloudSources)
        {
            yield return source;
        }

        foreach (var source in store.LocalSources)
        {
            yield return source;
        }
    }

    private bool IsUsableApiFallback(ClientProfile profile)
    {
        if (string.IsNullOrWhiteSpace(profile.Secret))
        {
            return false;
        }

        if (!Uri.TryCreate(profile.BaseUrl, UriKind.Absolute, out var uri))
        {
            return string.IsNullOrWhiteSpace(profile.BaseUrl);
        }

        return !IsPlaceholderHost(uri.Host);
    }

    private static bool IsAnthropicOfficialBaseUrl(string baseUrl)
    {
        if (string.IsNullOrWhiteSpace(baseUrl))
        {
            return true;
        }

        return Uri.TryCreate(baseUrl, UriKind.Absolute, out var uri) &&
               uri.Host.Equals("api.anthropic.com", StringComparison.OrdinalIgnoreCase);
    }

    private static bool IsClaudeOfficialApiKey(string secret)
    {
        return secret.Trim().StartsWith("sk-ant-api", StringComparison.OrdinalIgnoreCase);
    }

    private static string BuildCandidateKey(ClientProfile profile)
    {
        return $"{profile.BaseUrl.Trim()}|{profile.Secret.Trim()}";
    }


    private static string FindClaudeAppExecutablePath()
    {
        return FindInstalledClaudeAppExecutablePath();
    }

    private static string FindInstalledClaudeAppExecutablePath()
    {
        try
        {
            var windowsApps = Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles),
                "WindowsApps");

            return Directory.EnumerateDirectories(windowsApps, "Claude_*", SearchOption.TopDirectoryOnly)
                .Select(directory => Path.Combine(directory, "app", "Claude.exe"))
                .Where(File.Exists)
                .OrderByDescending(File.GetLastWriteTimeUtc)
                .FirstOrDefault() ?? string.Empty;
        }
        catch
        {
            return string.Empty;
        }
    }

    private static int StopRunningClaudeAppProcesses()
    {
        var stopped = 0;
        foreach (var process in Process.GetProcessesByName("claude"))
        {
            using (process)
            {
                try
                {
                    process.Kill();
                    stopped++;
                }
                catch
                {
                    // Best effort: a process may exit between enumeration and termination.
                }
            }
        }

        return stopped;
    }

    private static bool IsClaudeAppOrClaudeCodeProcess(Process process)
    {
        string processName;
        try
        {
            processName = process.ProcessName;
        }
        catch
        {
            return false;
        }

        if (!processName.Equals("Claude", StringComparison.OrdinalIgnoreCase) &&
            !processName.Equals("claude", StringComparison.OrdinalIgnoreCase))
        {
            return false;
        }

        var path = TryGetProcessPath(process);
        return path.Contains("\\WindowsApps\\Claude_", StringComparison.OrdinalIgnoreCase) ||
               path.Contains("\\AppData\\Local\\AnthropicClaude\\", StringComparison.OrdinalIgnoreCase);
    }

    private static string TryGetProcessPath(Process process)
    {
        try
        {
            return process.MainModule?.FileName ?? string.Empty;
        }
        catch
        {
            return string.Empty;
        }
    }
    private void ImportCurrentIntoProfile(TargetMode mode)
    {
        var current = _switchService.ReadCurrentClientConfig();
        if (current.Codex is null || current.Claude is null)
        {
            AppendLine("导入失败：当前主配置不完整，无法同时读取 Codex 和 Claude Code。");
            return;
        }

        var target = ResolveProfileForMode(_currentStore, mode);
        target.Codex.BaseUrl = current.Codex.BaseUrl;
        target.Codex.Secret = current.Codex.Secret;
        target.Claude.BaseUrl = current.Claude.BaseUrl;
        target.Claude.Secret = current.Claude.Secret;
        target.Gemini.BaseUrl = current.Gemini?.BaseUrl ?? string.Empty;
        target.Gemini.Secret = current.Gemini?.Secret ?? string.Empty;

        if (string.IsNullOrWhiteSpace(target.Notes))
        {
            target.Notes = $"导入于 {DateTime.Now:yyyy-MM-dd HH:mm:ss}";
        }

        LoadProfilesIntoForm(_currentStore);
        UpdatePendingActionSummary();
        AppendLine($"已将当前主配置导入到 {target.Name} profile。");
    }

    private void OpenProfileSite(TargetMode mode)
    {
        _currentStore = CollectProfilesFromForm();
        var url = _switchService.GetSiteUrl(_currentStore, mode);
        if (string.IsNullOrWhiteSpace(url))
        {
            AppendLine("当前模式的站点地址无效，无法打开。");
            return;
        }

        Process.Start(new ProcessStartInfo
        {
            FileName = url,
            UseShellExecute = true
        });
        AppendLine($"已打开站点: {url}");
    }

    private void OpenUrl(string url, string label)
    {
        Process.Start(new ProcessStartInfo
        {
            FileName = url,
            UseShellExecute = true
        });
        AppendLine($"已打开{label}: {url}");
    }

    private void OpenFileIfExists(string path, string label)
    {
        if (!File.Exists(path))
        {
            AppendLine($"无法打开{label}，文件不存在: {path}");
            return;
        }

        Process.Start(new ProcessStartInfo
        {
            FileName = path,
            UseShellExecute = true
        });
        AppendLine($"已打开{label}: {path}");
    }

    private void CopyCurrentSummary()
    {
        _currentStore = CollectProfilesFromForm();
        var mode = GetSelectedTargetMode();
        var profile = ResolveProfileForMode(_currentStore, mode);
        var summary =
            $"模式: {profile.Name}{Environment.NewLine}" +
            $"Codex: {profile.Codex.BaseUrl}{Environment.NewLine}" +
            $"Claude: {profile.Claude.BaseUrl}{Environment.NewLine}" +
            $"备注: {profile.Notes}";
        Clipboard.SetText(summary);
        AppendLine("已复制当前配置摘要到剪贴板。");
    }

    private void DuplicateCurrentSource()
    {
        _currentStore = CollectProfilesFromForm();
        var mode = GetSelectedTargetMode();
        if (mode == TargetMode.Local)
        {
            var source = FindLocalSource(GetSelectedLocalSourceId()) ?? _currentStore.Local;
            var clone = CloneProfile(source);
            clone.Id = Guid.NewGuid().ToString("N");
            clone.Name = $"{source.Name} 副本";
            _currentStore.LocalSources.Add(clone);
            _currentStore.SelectedLocalSourceId = clone.Id;
            LoadSourceGrid(_localSourcesGrid, _currentStore.LocalSources);
            RefreshSourceCombos(_currentStore);
            SelectSourceOption(_localSourceBox, clone.Id);
            LoadSelectedLocalSourceIntoFields();
            AppendLine($"已复制本地中转来源：{clone.Name}");
            return;
        }

        var cloud = FindCloudSource(GetSelectedCloudSourceId()) ?? _currentStore.Cloud;
        var cloudClone = CloneProfile(cloud);
        cloudClone.Id = Guid.NewGuid().ToString("N");
        cloudClone.Name = $"{cloud.Name} 副本";
        _currentStore.CloudSources.Add(cloudClone);
        _currentStore.SelectedCloudSourceId = cloudClone.Id;
        LoadSourceGrid(_cloudSourcesGrid, _currentStore.CloudSources);
        RefreshSourceCombos(_currentStore);
        SelectSourceOption(_cloudSourceBox, cloudClone.Id);
        LoadSelectedCloudSourceIntoFields();
        AppendLine($"已复制云端来源组：{cloudClone.Name}");
    }

    private void ResetCurrentSourceTemplate()
    {
        _currentStore = CollectProfilesFromForm();
        if (GetSelectedTargetMode() == TargetMode.Local)
        {
            var source = FindLocalSource(GetSelectedLocalSourceId()) ?? _currentStore.Local;
            var template = IsBuiltInLocalSource(source.Id) ? ProfileDefinition.CreateLocalDefaults() : ProfileDefinition.CreateLanDefaults();
            source.Codex.BaseUrl = template.Codex.BaseUrl;
            source.Codex.Secret = string.Empty;
            source.Claude.BaseUrl = template.Claude.BaseUrl;
            source.Claude.Secret = string.Empty;
            source.Gemini.BaseUrl = template.Gemini.BaseUrl;
            source.Gemini.Secret = string.Empty;
            LoadSourceGrid(_localSourcesGrid, _currentStore.LocalSources);
            LoadSelectedLocalSourceIntoFields();
            AppendLine($"已将 {source.Name} 重置为本地中转模板。");
            return;
        }

        var cloud = FindCloudSource(GetSelectedCloudSourceId()) ?? _currentStore.Cloud;
        var cloudTemplate = ProfileDefinition.CreateCloudDefaults();
        cloud.Codex.BaseUrl = cloudTemplate.Codex.BaseUrl;
        cloud.Codex.Secret = string.Empty;
        cloud.Claude.BaseUrl = cloudTemplate.Claude.BaseUrl;
        cloud.Claude.Secret = string.Empty;
        cloud.Gemini.BaseUrl = cloudTemplate.Gemini.BaseUrl;
        cloud.Gemini.Secret = string.Empty;
        LoadSourceGrid(_cloudSourcesGrid, _currentStore.CloudSources);
        LoadSelectedCloudSourceIntoFields();
        AppendLine($"已将 {cloud.Name} 重置为云端模板。");
    }

    private void OpenLocalGatewayUrl(string path, string label)
    {
        var baseUrl = _localGatewayService.WebUrl;
        var diagnostics = _lastLocalGatewayStatus?.Diagnostics;
        if (_lastLocalGatewayStatus?.WebReachable != true &&
            diagnostics?.LanHealthReachable == true &&
            Uri.TryCreate(diagnostics.LanHealthUrl, UriKind.Absolute, out var lanHealthUri))
        {
            baseUrl = $"{lanHealthUri.Scheme}://{lanHealthUri.Authority}";
            AppendLine($"localhost 暂不可用，改用可访问地址: {baseUrl}");
        }

        OpenUrl($"{baseUrl}{path}", label);
    }

    private void ConvertCpaAccounts()
    {
        using var dialog = new FolderBrowserDialog
        {
            Description = "选择包含 CPA JSON 或 Sub2API 导出 JSON 的文件夹",
            UseDescriptionForTitle = true,
            ShowNewFolderButton = false
        };

        if (dialog.ShowDialog(this) != DialogResult.OK ||
            string.IsNullOrWhiteSpace(dialog.SelectedPath))
        {
            return;
        }

        try
        {
            var result = _cpaSub2ApiConverter.ConvertDirectory(dialog.SelectedPath);
            AppendLine($"账号转换/合并完成：读取 {result.InputFileCount} 个 JSON，生成 {result.AccountCount} 个 Sub2API 账号。");
            AppendLine($"输出文件：{result.OutputPath}");

            foreach (var warning in result.Warnings.Take(8))
            {
                AppendLine($"转换提示：{warning}");
            }

            if (result.Warnings.Count > 8)
            {
                AppendLine($"还有 {result.Warnings.Count - 8} 条转换提示未显示。");
            }

            MessageBox.Show(
                this,
                $"转换/合并完成。\n\n账号数：{result.AccountCount}\n输出文件：\n{result.OutputPath}\n\n后台“更多操作 -> 导入”时请选择这个文件。",
                "账号转/合并 Sub2API",
                MessageBoxButtons.OK,
                MessageBoxIcon.Information);

            Process.Start(new ProcessStartInfo
            {
                FileName = Path.GetDirectoryName(result.OutputPath) ?? result.SourceDirectory,
                UseShellExecute = true
            });
        }
        catch (Exception ex)
        {
            AppendLine($"账号转换/合并失败：{ex.Message}");
            MessageBox.Show(
                this,
                ex.Message,
                "账号转/合并 Sub2API 失败",
                MessageBoxButtons.OK,
                MessageBoxIcon.Warning);
        }
    }

    private async Task OpenLanGatewayUrlAsync(string path, string label)
    {
        var baseUrl = GetLanGatewayBaseUrlFromSources();
        if (string.IsNullOrWhiteSpace(baseUrl))
        {
            var diagnostics = _lastLocalGatewayStatus?.Diagnostics;
            if (diagnostics?.LanHealthReachable == true &&
                Uri.TryCreate(diagnostics.LanHealthUrl, UriKind.Absolute, out var lanHealthUri))
            {
                baseUrl = $"{lanHealthUri.Scheme}://{lanHealthUri.Authority}";
            }
        }

        if (string.IsNullOrWhiteSpace(baseUrl))
        {
            AppendLine("未找到已配置的局域网后台地址，开始自动检测 LAN 中转站...");
            var status = await _localGatewayService.GetStatusAsync(CancellationToken.None, includeDeepDiagnostics: true);
            ApplyLocalGatewayStatus(status);
            if (status.Diagnostics.LanHealthReachable &&
                Uri.TryCreate(status.Diagnostics.LanHealthUrl, UriKind.Absolute, out var lanHealthUri))
            {
                baseUrl = $"{lanHealthUri.Scheme}://{lanHealthUri.Authority}";
            }
        }

        if (string.IsNullOrWhiteSpace(baseUrl))
        {
            AppendLine("未找到可打开的局域网中转后台地址。请先在“本地中转”页选择或填写局域网来源，例如 http://192.168.31.238:8080。");
            return;
        }

        OpenUrl($"{baseUrl}{path}", label);
    }

    private string? GetLanGatewayBaseUrlFromSources()
    {
        var candidates = new List<ProfileDefinition>();
        _currentStore = CollectProfilesFromForm();
        // The dedicated "局域网中转" source is the user-configured remote
        // target.  Do not accidentally prefer the currently selected local
        // 127.0.0.1 source when opening the LAN administration page.
        AddCandidate(candidates, _currentStore.LocalSources.FirstOrDefault(x =>
            string.Equals(x.Id, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase)));

        var savedStore = _repository.LoadProfiles();
        AddCandidate(candidates, savedStore.LocalSources.FirstOrDefault(x =>
            string.Equals(x.Id, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase)));

        foreach (var candidate in candidates)
        {
            var url = TryGetLanGatewayBaseUrl(candidate);
            if (!string.IsNullOrWhiteSpace(url))
            {
                return url;
            }
        }

        return null;
    }

    private static void AddCandidate(List<ProfileDefinition> candidates, ProfileDefinition? source)
    {
        if (source is null ||
            candidates.Any(x => string.Equals(x.Id, source.Id, StringComparison.OrdinalIgnoreCase)))
        {
            return;
        }

        candidates.Add(source);
    }

    private static string? TryGetLanGatewayBaseUrl(ProfileDefinition? source)
    {
        if (source is null)
        {
            return null;
        }

        var candidate = !string.IsNullOrWhiteSpace(source.Claude.BaseUrl)
            ? source.Claude.BaseUrl
            : source.Codex.BaseUrl;
        if (!Uri.TryCreate(candidate, UriKind.Absolute, out var uri) ||
            IsPlaceholderHost(uri.Host))
        {
            return null;
        }

        return new UriBuilder(uri.Scheme, uri.Host, uri.Port).Uri.ToString().TrimEnd('/');
    }

    private static bool IsLoopbackHost(string host)
    {
        if (host.Equals("localhost", StringComparison.OrdinalIgnoreCase))
        {
            return true;
        }

        return IPAddress.TryParse(host, out var address) && IPAddress.IsLoopback(address);
    }

    private static bool IsPlaceholderHost(string host)
    {
        return host.Contains('x', StringComparison.OrdinalIgnoreCase);
    }

    private void HideToTray(string message)
    {
        Hide();
        ShowInTaskbar = false;
        _trayIcon.Visible = true;
        _trayIcon.ShowBalloonTip(1800, "本地中转管理工具", message, ToolTipIcon.Info);
    }

    private void RestoreFromTray()
    {
        ShowInTaskbar = true;
        Show();
        WindowState = FormWindowState.Normal;
        Activate();
        BringToFront();
    }

    private void ExitForReal()
    {
        _forceExit = true;
        _trayIcon.Visible = false;
        Close();
    }

    private async Task ToggleLocalGatewayAsync()
    {
        if (_localGatewayIsRunning)
        {
            await RunLocalGatewayCommandAsync("关闭所有服务", _localGatewayService.StopAsync);
            return;
        }

        await StartLocalGatewayAsync();
    }

    private async Task StartLocalGatewayAsync()
    {
        await RunLocalGatewayCommandAsync("启动", _localGatewayService.StartAsync);
        AppendLine("等待 Sub2API 后台响应...");
        if (await _localGatewayService.WaitForWebAsync(TimeSpan.FromSeconds(90), CancellationToken.None))
        {
            await RefreshLocalGatewayStatusAsync();
            OpenLocalGatewayUrl("/dashboard", "Sub2API 后台");
            return;
        }

        AppendLine("服务已启动，但后台暂未响应，请稍后刷新状态查看详情。");
    }

    private async Task RunLocalGatewayCommandAsync(
        string actionName,
        Func<CancellationToken, Task<CommandResult>> command)
    {
        AppendLine($"{actionName}本地中转站...");
        var result = await command(CancellationToken.None);
        AppendLine($"{actionName}本地中转站{(result.Success ? "完成" : "失败")}，退出码 {result.ExitCode}。");

        var output = SummarizeCommandOutput(result.CombinedOutput);
        if (!string.IsNullOrWhiteSpace(output))
        {
            AppendLine(output);
        }

        await RefreshLocalGatewayStatusAsync();
    }

    private async Task RefreshLocalGatewayStatusAsync()
    {
        var status = await _localGatewayService.GetStatusAsync(CancellationToken.None);
        ApplyLocalGatewayStatus(status);
        AppendLine($"本地中转站状态: {status.Summary}");
        foreach (var message in status.Diagnostics.Messages)
        {
            AppendLine($"诊断: {message}");
        }
    }

    private void ApplyStartupLocalGatewayStatus()
    {
        ApplyLocalGatewayStatus(_localGatewayService.GetStartupStatus());
    }

    private void ApplyLocalGatewayStatus(LocalGatewayStatus status)
    {
        _lastLocalGatewayStatus = status;
        _localGatewayIsRunning = IsLocalGatewayRunning(status);
        _localGatewayDockerInstalled = status.ControlAvailable;
        _localGatewayWebReachable = status.WebReachable;
        _localGatewayComposeReady = status.ControlAvailable;
        _localGatewayStatusValue.Text = status.Summary;
        _localGatewayWebValue.Text = status.WebReachable ? $"{status.WebUrl} 可访问" : $"{status.WebUrl} 未响应";
        _localGatewayComposeValue.Text = status.NativeMode
            ? $"原生: {Path.GetFileName(status.NativeRoot)}"
            : string.IsNullOrWhiteSpace(status.ComposeFile) ? "未找到" : Path.GetFileName(status.ComposeFile);

        ApplyLocalGatewayPrimaryButtonStyle();
        ApplyLocalGatewayButtonAvailability();
        _localGatewayServicesBox.Text = BuildLocalGatewayServicesText(status);
    }

    private void ApplyLocalGatewayPrimaryButtonStyle()
    {
        _localGatewayStartButton.Enabled = _localGatewayDockerInstalled && _localGatewayComposeReady;

        if (_localGatewayIsRunning)
        {
            _localGatewayStartButton.Text = "关闭所有服务";
            _localGatewayStartButton.BackColor = Danger;
            _localGatewayStartButton.FlatAppearance.MouseOverBackColor = ControlPaint.Dark(Danger);
            _localGatewayStartButton.FlatAppearance.MouseDownBackColor = ControlPaint.Dark(Danger);
            return;
        }

        _localGatewayStartButton.Text = "启动并打开";
        _localGatewayStartButton.BackColor = Success;
        _localGatewayStartButton.FlatAppearance.MouseOverBackColor = ControlPaint.Dark(Success);
        _localGatewayStartButton.FlatAppearance.MouseDownBackColor = ControlPaint.Dark(Success);
    }

    private static async Task<NvidiaGpuInfo?> DetectNvidiaGpuAsync()
    {
        try
        {
            using var process = new Process
            {
                StartInfo = new ProcessStartInfo
                {
                    FileName = "nvidia-smi",
                    Arguments = "--query-gpu=name,memory.total --format=csv,noheader,nounits",
                    UseShellExecute = false,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true,
                    CreateNoWindow = true
                }
            };

            process.Start();
            var outputTask = process.StandardOutput.ReadToEndAsync();
            var waitTask = process.WaitForExitAsync();
            var completed = await Task.WhenAny(waitTask, Task.Delay(TimeSpan.FromSeconds(3)));
            if (completed != waitTask)
            {
                try { process.Kill(entireProcessTree: true); } catch { }
                return null;
            }

            var output = await outputTask;
            if (process.ExitCode != 0 || string.IsNullOrWhiteSpace(output))
            {
                return null;
            }

            return output
                .Split('\n', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
                .Select(ParseNvidiaGpuInfo)
                .Where(x => x is not null)
                .OrderByDescending(x => x!.TotalMemoryMiB)
                .FirstOrDefault();
        }
        catch
        {
            return null;
        }
    }

    private static NvidiaGpuInfo? ParseNvidiaGpuInfo(string line)
    {
        var parts = line.Split(',').Select(x => x.Trim()).ToArray();
        if (parts.Length < 2 || !int.TryParse(parts[^1], out var memoryMiB))
        {
            return null;
        }

        var name = string.Join(", ", parts.Take(parts.Length - 1));
        return name.Contains("NVIDIA", StringComparison.OrdinalIgnoreCase) || name.Contains("GeForce", StringComparison.OrdinalIgnoreCase) || name.Contains("RTX", StringComparison.OrdinalIgnoreCase) || name.Contains("GTX", StringComparison.OrdinalIgnoreCase)
            ? new NvidiaGpuInfo(name, memoryMiB)
            : null;
    }

    private sealed record NvidiaGpuInfo(string Name, int TotalMemoryMiB);

    private static bool IsLocalGatewayRunning(LocalGatewayStatus status)
    {
        return status.WebReachable ||
               status.Services.Any(service =>
                   string.Equals(service.State, "running", StringComparison.OrdinalIgnoreCase) &&
                   (string.Equals(service.Service, "sub2api", StringComparison.OrdinalIgnoreCase) ||
                     string.Equals(service.Service, "postgres", StringComparison.OrdinalIgnoreCase) ||
                    string.Equals(service.Service, "redis", StringComparison.OrdinalIgnoreCase)));
    }

    private void ApplyLocalGatewayButtonAvailability()
    {
        _localGatewayRefreshButton.Enabled = true;

        var canControl = _localGatewayDockerInstalled && _localGatewayComposeReady;
        _localGatewayStartButton.Enabled = canControl;
        _localGatewayRestartButton.Enabled = canControl && _localGatewayIsRunning;
        _localGatewayStopButton.Enabled = canControl && _localGatewayIsRunning;
        _localGatewayOpenAdminButton.Enabled = _localGatewayWebReachable;
        _localGatewayOpenLanAdminButton.Enabled = true;
        _convertCpaAccountsButton.Enabled = true;
    }

    private static string BuildLocalGatewayServicesText(LocalGatewayStatus status)
    {
        if (status.NativeMode)
        {
            var nativeLines = new List<string>
            {
                $"mode          native Windows",
                $"root          {status.NativeRoot}",
                string.Empty
            };
            nativeLines.AddRange(status.Services.Select(service =>
                $"{service.Service,-13} {service.State,-9} {service.Ports}"));
            nativeLines.Add(string.Empty);
            nativeLines.Add($"frontend      {status.WebUrl}");
            nativeLines.Add($"backend       http://127.0.0.1:8080/health");
            nativeLines.Add($"health        {(status.WebReachable ? "OK" : "FAIL")}");
            return string.Join(Environment.NewLine, nativeLines);
        }

        if (!status.DockerInstalled)
        {
            return "本机未检测到 Docker Desktop 或 docker CLI。本地中转站相关按钮已禁用，但配置切换功能仍可使用。";
        }

        if (string.IsNullOrWhiteSpace(status.ComposeFile))
        {
            return "未找到 docker-compose.local.yml。请把工具放在项目目录内，或从项目根目录启动。";
        }

        if (!status.DockerAvailable)
        {
            return string.IsNullOrWhiteSpace(status.CommandOutput)
                ? "Docker 未运行或不可用。"
                : status.CommandOutput;
        }

        if (status.Services.Count == 0)
        {
            return "没有发现运行中的 compose 服务。";
        }

        var lines = new List<string>();
        lines.AddRange(
            status.Services.Select(service =>
            {
                var health = string.IsNullOrWhiteSpace(service.Health) ? "no-healthcheck" : service.Health;
                var ports = string.IsNullOrWhiteSpace(service.Ports) ? "internal" : service.Ports;
                return $"{service.Service,-13} {service.State,-8} {health,-14} {ports}";
            }));

        lines.Add(string.Empty);
        lines.Add($"health localhost : {(status.Diagnostics.LocalhostHealthReachable ? "OK" : "FAIL")}");
        lines.Add($"health 127.0.0.1 : {(status.Diagnostics.LoopbackHealthReachable ? "OK" : "FAIL")}");
        if (!string.IsNullOrWhiteSpace(status.Diagnostics.LanHealthUrl))
        {
            lines.Add($"health LAN       : {(status.Diagnostics.LanHealthReachable ? "OK" : "FAIL")} {status.Diagnostics.LanHealthUrl}");
        }

        if (status.Diagnostics.PortListeners.Count > 0)
        {
            lines.Add("port 8080:");
            lines.AddRange(status.Diagnostics.PortListeners.Select(listener => $"  {listener.DisplayName}"));
        }

        if (status.Diagnostics.Messages.Count > 0)
        {
            lines.Add("diagnosis:");
            lines.AddRange(status.Diagnostics.Messages.Select(message => $"  {message}"));
        }

        return string.Join(Environment.NewLine, lines);
    }

    private static string SummarizeCommandOutput(string output)
    {
        if (string.IsNullOrWhiteSpace(output))
        {
            return string.Empty;
        }

        var compact = output
            .Replace("\r\n", "\n", StringComparison.Ordinal)
            .Replace('\r', '\n')
            .Split('\n', StringSplitOptions.RemoveEmptyEntries)
            .TakeLast(6);

        return string.Join(" | ", compact);
    }

    private static string FormatMetricNumber(double? value)
    {
        return value.HasValue ? value.Value.ToString("N0") : "-";
    }

    private static string FormatMetricPercent(double? value)
    {
        return value.HasValue ? $"{value.Value:0.##}%" : "-";
    }

    private static double? CalculateSavingsPercent(double savedTokens, double sentTokens)
    {
        var totalTokens = savedTokens + sentTokens;
        return totalTokens > 0 ? savedTokens / totalTokens * 100 : null;
    }

    private static string FormatMetricTime(DateTime? value)
    {
        return value.HasValue ? value.Value.ToString("HH:mm:ss") : "-";
    }

    private async Task RunActionAsync(Func<Task> action)
    {
        try
        {
            ToggleButtons(false);
            await action();
        }
        catch (Exception ex)
        {
            AppendLine($"操作失败: {ex.Message}");
        }
        finally
        {
            ToggleButtons(true);
        }
    }

    private LiveStatus RefreshLiveStatus()
    {
        var status = _switchService.ReadLiveStatus(_currentStore);
        _currentTargetValue.Text = status.ActiveTarget;
        _codexBaseValue.Text = status.CodexBaseUrl;
        _claudeBaseValue.Text = status.ClaudeBaseUrl;
        _geminiBaseValue.Text = status.GeminiBaseUrl;

        var latestBackup = _repository.GetLatestBackup();
        _backupValue.Text = latestBackup?.Exists == true
            ? Path.GetFileName(latestBackup.Folder)
            : "无";

        ApplyModeBadgeStyle(status);
        ApplyHealthBadgeStyle(status);
        ApplyValidationText(status);
        return status;
    }

    private void ApplyModeBadgeStyle(LiveStatus status)
    {
        _modeBadge.Text = $"当前: {status.ActiveTarget}";
        (_modeBadge.BackColor, _modeBadge.ForeColor) = status.Kind switch
        {
            LiveStatusKind.Cloud => (PrimarySoft, Primary),
            LiveStatusKind.Local => (SuccessSoft, Success),
            LiveStatusKind.Mixed => (WarningSoft, Warning),
            LiveStatusKind.Missing => (DangerSoft, Danger),
            _ => (Color.FromArgb(244, 244, 244), TextMain)
        };
    }

    private void ApplyHealthBadgeStyle(LiveStatus status)
    {
        _healthBadge.Text = status.HealthText;
        (_healthBadge.BackColor, _healthBadge.ForeColor) = status.Kind switch
        {
            LiveStatusKind.Cloud => (PrimarySoft, Primary),
            LiveStatusKind.Local => (SuccessSoft, Success),
            LiveStatusKind.Mixed => (WarningSoft, Warning),
            LiveStatusKind.Missing => (DangerSoft, Danger),
            _ => (Color.FromArgb(244, 244, 244), TextMain)
        };
    }

    private void ApplyValidationText(LiveStatus status)
    {
        if (status.Kind == LiveStatusKind.Missing)
        {
            _codexValidationValue.Text = "缺少主配置";
            _claudeValidationValue.Text = "缺少主配置";
            _geminiValidationValue.Text = "缺少主配置";
            return;
        }

        _codexValidationValue.Text = "切换后看日志验证结果";
        _claudeValidationValue.Text = "切换后看日志验证结果";
        _geminiValidationValue.Text = "切换后看日志验证结果";
    }

    private TargetMode GetSelectedTargetMode()
    {
        return _profileTabs.SelectedTab?.Text switch
        {
            "云端" => TargetMode.Cloud,
            "混合模式" => TargetMode.Mixed,
            "本地中转" => TargetMode.Local,
            _ => TargetMode.Local
        };
    }

    private string GetMixedSummaryText()
    {
        var codex = _mixedCodexSourceBox.SelectedItem is SourceOption codexSource ? codexSource.DisplayName : "云端";
        var claude = _mixedClaudeSourceBox.SelectedItem is SourceOption claudeSource ? claudeSource.DisplayName : "本机中转";
        var gemini = _mixedGeminiSourceBox.SelectedItem is SourceOption geminiSource ? geminiSource.DisplayName : "本地中转";
        var grok = _mixedGrokSourceBox.SelectedItem is SourceOption grokSource ? grokSource.DisplayName : "本地中转";
        return $"当前组合：Codex -> {codex}；Claude -> {claude}；Gemini -> {gemini}；Grok -> {grok}";
    }

    private void UpdatePendingActionSummary()
    {
        var mode = GetSelectedTargetMode();
        var cloudName = string.IsNullOrWhiteSpace(_cloudSourceNameBox.Text)
            ? (_cloudSourceBox.SelectedItem as SourceOption)?.DisplayName ?? (_currentStore.Cloud?.Name ?? "云端")
            : _cloudSourceNameBox.Text.Trim();
        var localName = string.IsNullOrWhiteSpace(_localSourceNameBox.Text)
            ? (_localSourceBox.SelectedItem as SourceOption)?.DisplayName ?? (_currentStore.Local?.Name ?? "本地中转")
            : _localSourceNameBox.Text.Trim();

        _pendingModeValue.Text = mode switch
        {
            TargetMode.Cloud => $"当前操作：应用 {cloudName} Profile",
            TargetMode.Local => $"当前操作：应用 {localName} Profile",
            TargetMode.Mixed => "当前操作：应用混合模式",
            _ => $"当前操作：应用 {localName} Profile"
        };

        _pendingDetailValue.Text = mode == TargetMode.Mixed
            ? GetMixedSummaryText()
            : "右侧按钮会应用当前页签选中的来源。";
    }

    private void ApplyMixedProfile(ProfileStore store)
    {
        store.Mixed.CodexSourceId = _mixedCodexSourceBox.SelectedItem is SourceOption codexSource ? codexSource.Id : ProfileSourceIds.Cloud;
        store.Mixed.ClaudeSourceId = _mixedClaudeSourceBox.SelectedItem is SourceOption claudeSource ? claudeSource.Id : store.SelectedLocalSourceId;
        store.Mixed.GeminiSourceId = _mixedGeminiSourceBox.SelectedItem is SourceOption geminiSource ? geminiSource.Id : store.SelectedLocalSourceId;
        store.Mixed.GrokSourceId = _mixedGrokSourceBox.SelectedItem is SourceOption grokSource ? grokSource.Id : store.SelectedLocalSourceId;
        store.Mixed.CodexSource = string.Equals(store.Mixed.CodexSourceId, ProfileSourceIds.Cloud, StringComparison.OrdinalIgnoreCase)
            ? ClientSourceMode.Cloud
            : ClientSourceMode.Local;
        store.Mixed.ClaudeSource = string.Equals(store.Mixed.ClaudeSourceId, ProfileSourceIds.Cloud, StringComparison.OrdinalIgnoreCase)
            ? ClientSourceMode.Cloud
            : ClientSourceMode.Local;
        store.Mixed.GeminiSource = string.Equals(store.Mixed.GeminiSourceId, ProfileSourceIds.Cloud, StringComparison.OrdinalIgnoreCase)
            ? ClientSourceMode.Cloud
            : ClientSourceMode.Local;
        store.Mixed.GrokSource = string.Equals(store.Mixed.GrokSourceId, ProfileSourceIds.Cloud, StringComparison.OrdinalIgnoreCase)
            ? ClientSourceMode.Cloud
            : ClientSourceMode.Local;
    }

    private ProfileStore CollectProfilesFromForm()
    {
        var cloudSources = CloneCloudSourcesWithCurrentFields();
        var selectedCloudId = GetSelectedCloudSourceId();
        var selectedCloud = cloudSources.FirstOrDefault(x =>
            string.Equals(x.Id, selectedCloudId, StringComparison.OrdinalIgnoreCase)) ?? cloudSources[0];
        var localSources = CloneLocalSourcesWithCurrentFields();
        var selectedLocalId = GetSelectedLocalSourceId();
        var selectedLocal = localSources.FirstOrDefault(x =>
            string.Equals(x.Id, selectedLocalId, StringComparison.OrdinalIgnoreCase)) ?? localSources[0];
        var store = new ProfileStore
        {
            Cloud = selectedCloud,
            CloudSources = cloudSources,
            SelectedCloudSourceId = selectedCloud.Id,
            Local = selectedLocal,
            LocalSources = localSources,
            SelectedLocalSourceId = selectedLocal.Id,
            ActiveConnectionProfileId = _currentStore.ActiveConnectionProfileId
        };

        store.Lan = store.LocalSources.FirstOrDefault(x =>
            string.Equals(x.Id, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase)) ?? ProfileDefinition.CreateLanDefaults();
        store.Mixed = new MixedProfileDefinition
        {
            CodexSourceId = _currentStore.Mixed.CodexSourceId,
            ClaudeSourceId = _currentStore.Mixed.ClaudeSourceId,
            GeminiSourceId = _currentStore.Mixed.GeminiSourceId,
            GrokSourceId = _currentStore.Mixed.GrokSourceId,
            CodexSource = _currentStore.Mixed.CodexSource,
            ClaudeSource = _currentStore.Mixed.ClaudeSource,
            GeminiSource = _currentStore.Mixed.GeminiSource,
            GrokSource = _currentStore.Mixed.GrokSource
        };
        ApplyMixedProfile(store);
        return store;
    }

    private void LoadProfilesIntoForm(ProfileStore store)
    {
        LoadSourceGrid(_cloudSourcesGrid, store.CloudSources);
        LoadSourceGrid(_localSourcesGrid, store.LocalSources);
        RefreshSourceCombos(store);
        SelectSourceOption(_cloudSourceBox, store.SelectedCloudSourceId);
        SelectSourceOption(_localSourceBox, store.SelectedLocalSourceId);
        LoadSelectedCloudSourceIntoFields();
        LoadSelectedLocalSourceIntoFields();

        if (_profileTabs.SelectedIndex < 0)
        {
            _profileTabs.SelectedIndex = _profileTabs.TabPages
                .Cast<TabPage>()
                .Select((page, index) => new { page.Text, index })
                .FirstOrDefault(x => x.Text == "本地中转")?.index ?? 0;
        }

        UpdatePendingActionSummary();
    }

    private void LoadSourceGrid(DataGridView grid, IEnumerable<ProfileDefinition> sources)
    {
        try
        {
            _loadingSourceGrids = true;
            grid.Rows.Clear();
            foreach (var source in sources)
            {
                grid.Rows.Add(
                    source.Id,
                    source.Name,
                    BuildSourceSummary(source),
                    source.Codex.Secret,
                    source.Claude.BaseUrl,
                    source.Claude.Secret,
                    source.Gemini.BaseUrl,
                    source.Gemini.Secret,
                    source.Grok.BaseUrl,
                    source.Grok.Secret,
                    source.Notes);
            }
        }
        finally
        {
            _loadingSourceGrids = false;
        }
    }

    private static string BuildSourceSummary(ProfileDefinition source)
    {
        var codex = string.IsNullOrWhiteSpace(source.Codex.BaseUrl) ? "Codex 未配置" : $"Codex: {source.Codex.BaseUrl}";
        var claude = string.IsNullOrWhiteSpace(source.Claude.BaseUrl) ? "Claude 未配置" : $"Claude: {source.Claude.BaseUrl}";
        var gemini = string.IsNullOrWhiteSpace(source.Gemini.BaseUrl) ? "Gemini 未配置" : $"Gemini: {source.Gemini.BaseUrl}";
        var grok = string.IsNullOrWhiteSpace(source.Grok.BaseUrl) ? "Grok 未配置" : $"Grok: {source.Grok.BaseUrl}";
        return $"{codex} | {claude} | {gemini} | {grok}";
    }

    private void RefreshSourceCombos(ProfileStore store)
    {
        var selectedCloudId = GetSelectedCloudSourceId();
        var selectedLocalId = GetSelectedLocalSourceId();
        var selectedCodexId = _mixedCodexSourceBox.SelectedItem is SourceOption codexOption ? codexOption.Id : store.Mixed.CodexSourceId;
        var selectedClaudeId = _mixedClaudeSourceBox.SelectedItem is SourceOption claudeOption ? claudeOption.Id : store.Mixed.ClaudeSourceId;
        var selectedGeminiId = _mixedGeminiSourceBox.SelectedItem is SourceOption geminiOption ? geminiOption.Id : store.Mixed.GeminiSourceId;
        var selectedGrokId = _mixedGrokSourceBox.SelectedItem is SourceOption grokOption ? grokOption.Id : store.Mixed.GrokSourceId;

        _cloudSourceBox.Items.Clear();
        foreach (var source in store.CloudSources)
        {
            _cloudSourceBox.Items.Add(new SourceOption(source.Id, source.Name));
        }

        _localSourceBox.Items.Clear();
        foreach (var source in store.LocalSources)
        {
            _localSourceBox.Items.Add(new SourceOption(source.Id, source.Name));
        }

        _mixedCodexSourceBox.Items.Clear();
        _mixedClaudeSourceBox.Items.Clear();
        _mixedGeminiSourceBox.Items.Clear();
        _mixedGrokSourceBox.Items.Clear();
        var allSources = BuildAllSourceOptions(store);
        _mixedCodexSourceBox.Items.AddRange(allSources.Cast<object>().ToArray());
        _mixedClaudeSourceBox.Items.AddRange(allSources.Cast<object>().ToArray());
        _mixedGeminiSourceBox.Items.AddRange(allSources.Cast<object>().ToArray());
        _mixedGrokSourceBox.Items.AddRange(allSources.Cast<object>().ToArray());

        SelectSourceOption(_cloudSourceBox, selectedCloudId);
        SelectSourceOption(_localSourceBox, selectedLocalId);
        SelectSourceOption(_mixedCodexSourceBox, selectedCodexId);
        SelectSourceOption(_mixedClaudeSourceBox, selectedClaudeId);
        SelectSourceOption(_mixedGeminiSourceBox, selectedGeminiId);
        SelectSourceOption(_mixedGrokSourceBox, selectedGrokId);
    }

    private static List<SourceOption> BuildAllSourceOptions(ProfileStore store)
    {
        return store.CloudSources
            .Concat(store.LocalSources)
            .Select(source => new SourceOption(source.Id, source.Name))
            .ToList();
    }

    private static void SelectSourceOption(ComboBox comboBox, string sourceId)
    {
        for (var i = 0; i < comboBox.Items.Count; i++)
        {
            if (comboBox.Items[i] is SourceOption option &&
                string.Equals(option.Id, sourceId, StringComparison.OrdinalIgnoreCase))
            {
                comboBox.SelectedIndex = i;
                return;
            }
        }

        if (comboBox.Items.Count > 0)
        {
            comboBox.SelectedIndex = 0;
        }
    }

    private string GetSelectedCloudSourceId()
    {
        return _cloudSourceBox.SelectedItem is SourceOption option
            ? option.Id
            : _currentStore.SelectedCloudSourceId;
    }

    private string GetSelectedLocalSourceId()
    {
        return _localSourceBox.SelectedItem is SourceOption option
            ? option.Id
            : _currentStore.SelectedLocalSourceId;
    }

    private ProfileDefinition? FindCloudSource(string sourceId)
    {
        return _currentStore.CloudSources.FirstOrDefault(x =>
            string.Equals(x.Id, sourceId, StringComparison.OrdinalIgnoreCase));
    }

    private ProfileDefinition? FindLocalSource(string sourceId)
    {
        return _currentStore.LocalSources.FirstOrDefault(x =>
            string.Equals(x.Id, sourceId, StringComparison.OrdinalIgnoreCase));
    }

    private static ProfileDefinition? FindSourceById(ProfileStore store, string sourceId)
    {
        return store.CloudSources.FirstOrDefault(x =>
                   string.Equals(x.Id, sourceId, StringComparison.OrdinalIgnoreCase)) ??
               store.LocalSources.FirstOrDefault(x =>
                   string.Equals(x.Id, sourceId, StringComparison.OrdinalIgnoreCase));
    }

    private static ClientProfile CloneClientProfile(ClientProfile profile)
    {
        return new ClientProfile
        {
            BaseUrl = profile.BaseUrl,
            Secret = profile.Secret
        };
    }

    private ProfileDefinition ResolveProfileForMode(ProfileStore store, TargetMode mode)
    {
        return mode switch
        {
            TargetMode.Cloud => FindCloudSource(GetSelectedCloudSourceId()) ?? store.Cloud,
            TargetMode.Local => FindLocalSource(GetSelectedLocalSourceId()) ?? store.Local,
            TargetMode.Mixed => new ProfileDefinition
            {
                Name = "混合模式",
                Notes = GetMixedSummaryText(),
                Codex = CloneClientProfile(FindSourceById(store, store.Mixed.CodexSourceId)?.Codex ?? store.Cloud.Codex),
                Claude = CloneClientProfile(FindSourceById(store, store.Mixed.ClaudeSourceId)?.Claude ?? store.Local.Claude),
                Gemini = CloneClientProfile(FindSourceById(store, store.Mixed.GeminiSourceId)?.Gemini ?? store.Local.Gemini)
            },
            _ => FindLocalSource(GetSelectedLocalSourceId()) ?? store.Local
        };
    }

    private static string ResolveActiveConnectionProfileId(ProfileStore store, TargetMode mode)
    {
        return mode switch
        {
            TargetMode.Cloud => store.SelectedCloudSourceId,
            TargetMode.Local => store.SelectedLocalSourceId,
            // Graphical Codex chat follows the Codex source when the legacy
            // switcher applies a per-client routing combination.
            TargetMode.Mixed when FindSourceById(store, store.Mixed.CodexSourceId) is not null =>
                store.Mixed.CodexSourceId,
            _ => store.SelectedLocalSourceId
        };
    }

    private void LoadSelectedCloudSourceIntoFields()
    {
        var source = FindCloudSource(GetSelectedCloudSourceId());
        if (source is null)
        {
            return;
        }

        try
        {
            _loadingCloudSourceFields = true;
            _cloudSourceNameBox.Text = source.Name;
            _cloudNotesBox.Text = source.Notes;
            _cloudCodexBaseBox.Text = source.Codex.BaseUrl;
            _cloudCodexKeyBox.Text = source.Codex.Secret;
            _cloudClaudeBaseBox.Text = source.Claude.BaseUrl;
            _cloudClaudeKeyBox.Text = source.Claude.Secret;
            _cloudGeminiBaseBox.Text = source.Gemini.BaseUrl;
            _cloudGeminiKeyBox.Text = source.Gemini.Secret;
            _cloudGrokBaseBox.Text = source.Grok.BaseUrl;
            _cloudGrokKeyBox.Text = source.Grok.Secret;
        }
        finally
        {
            _loadingCloudSourceFields = false;
        }

        _deleteCloudSourceButton.Enabled = _currentStore.CloudSources.Count > 1 &&
                                           !IsBuiltInCloudSource(source.Id);
        UpdatePendingActionSummary();
    }

    private void PersistCurrentCloudSourceFields()
    {
        if (_loadingCloudSourceFields)
        {
            return;
        }

        var source = FindCloudSource(GetSelectedCloudSourceId());
        if (source is null)
        {
            return;
        }

        source.Name = string.IsNullOrWhiteSpace(_cloudSourceNameBox.Text) ? source.Name : _cloudSourceNameBox.Text.Trim();
        source.Notes = _cloudNotesBox.Text.Trim();
        source.Codex.BaseUrl = _cloudCodexBaseBox.Text.Trim();
        source.Codex.Secret = _cloudCodexKeyBox.Text.Trim();
        source.Claude.BaseUrl = _cloudClaudeBaseBox.Text.Trim();
        source.Claude.Secret = _cloudClaudeKeyBox.Text.Trim();
        source.Gemini.BaseUrl = _cloudGeminiBaseBox.Text.Trim();
        source.Gemini.Secret = _cloudGeminiKeyBox.Text.Trim();
        source.Grok.BaseUrl = _cloudGrokBaseBox.Text.Trim();
        source.Grok.Secret = _cloudGrokKeyBox.Text.Trim();
        UpdateSourceComboLabel(_cloudSourceBox, source);
        UpdateSourceGridRow(_cloudSourcesGrid, source);
        UpdatePendingActionSummary();
    }

    private void LoadSelectedLocalSourceIntoFields()
    {
        var source = FindLocalSource(GetSelectedLocalSourceId());
        if (source is null)
        {
            return;
        }

        try
        {
            _loadingLocalSourceFields = true;
            _localSourceNameBox.Text = source.Name;
            _localSourceNameValue.Text = source.Name;
            _localNotesBox.Text = source.Notes;
            _localCodexBaseBox.Text = source.Codex.BaseUrl;
            _localCodexKeyBox.Text = source.Codex.Secret;
            _localClaudeBaseBox.Text = source.Claude.BaseUrl;
            _localClaudeKeyBox.Text = source.Claude.Secret;
            _localGeminiBaseBox.Text = source.Gemini.BaseUrl;
            _localGeminiKeyBox.Text = source.Gemini.Secret;
            _localGrokBaseBox.Text = source.Grok.BaseUrl;
            _localGrokKeyBox.Text = source.Grok.Secret;
        }
        finally
        {
            _loadingLocalSourceFields = false;
        }

        _deleteLocalSourceButton.Enabled = _currentStore.LocalSources.Count > 1 &&
                                           !IsBuiltInLocalSource(source.Id);
        UpdatePendingActionSummary();
    }

    private void PersistCurrentLocalSourceFields()
    {
        if (_loadingLocalSourceFields)
        {
            return;
        }

        var source = FindLocalSource(GetSelectedLocalSourceId());
        if (source is null)
        {
            return;
        }

        source.Name = string.Equals(source.Id, ProfileSourceIds.LocalMachine, StringComparison.OrdinalIgnoreCase)
            ? "本机中转"
            : "局域网中转";
        source.Notes = _localNotesBox.Text.Trim();
        source.Codex.BaseUrl = _localCodexBaseBox.Text.Trim();
        source.Codex.Secret = _localCodexKeyBox.Text.Trim();
        source.Claude.BaseUrl = _localClaudeBaseBox.Text.Trim();
        source.Claude.Secret = _localClaudeKeyBox.Text.Trim();
        source.Gemini.BaseUrl = _localGeminiBaseBox.Text.Trim();
        source.Gemini.Secret = _localGeminiKeyBox.Text.Trim();
        source.Grok.BaseUrl = _localGrokBaseBox.Text.Trim();
        source.Grok.Secret = _localGrokKeyBox.Text.Trim();
        UpdateSourceComboLabel(_localSourceBox, source);
        UpdateSourceGridRow(_localSourcesGrid, source);
        UpdatePendingActionSummary();
    }

    private static void UpdateSourceGridRow(DataGridView grid, ProfileDefinition source)
    {
        foreach (DataGridViewRow row in grid.Rows)
        {
            if (string.Equals(Convert.ToString(row.Cells["Id"].Value), source.Id, StringComparison.OrdinalIgnoreCase))
            {
                row.Cells["Name"].Value = source.Name;
                row.Cells["CodexBase"].Value = BuildSourceSummary(source);
                row.Cells["CodexKey"].Value = source.Codex.Secret;
                row.Cells["ClaudeBase"].Value = source.Claude.BaseUrl;
                row.Cells["ClaudeKey"].Value = source.Claude.Secret;
                row.Cells["GeminiBase"].Value = source.Gemini.BaseUrl;
                row.Cells["GeminiKey"].Value = source.Gemini.Secret;
                row.Cells["GrokBase"].Value = source.Grok.BaseUrl;
                row.Cells["GrokKey"].Value = source.Grok.Secret;
                row.Cells["Notes"].Value = source.Notes;
                return;
            }
        }
    }

    private void UpdateSourceComboLabel(ComboBox comboBox, ProfileDefinition source)
    {
        if (comboBox.SelectedItem is not SourceOption selected ||
            !string.Equals(selected.Id, source.Id, StringComparison.OrdinalIgnoreCase) ||
            string.Equals(selected.DisplayName, source.Name, StringComparison.Ordinal))
        {
            return;
        }

        var index = comboBox.SelectedIndex;
        if (index < 0)
        {
            return;
        }

        comboBox.Items[index] = new SourceOption(source.Id, source.Name);
        comboBox.SelectedIndex = index;
    }

    private List<ProfileDefinition> CloneCloudSourcesWithCurrentFields()
    {
        var selectedId = GetSelectedCloudSourceId();
        var sources = _currentStore.CloudSources.Select(CloneProfile).ToList();
        var typedName = _cloudSourceNameBox.Text.Trim();
        if (!string.IsNullOrWhiteSpace(typedName))
        {
            var existingByName = sources.FirstOrDefault(x =>
                string.Equals(x.Name, typedName, StringComparison.OrdinalIgnoreCase));
            if (existingByName is not null &&
                !string.Equals(existingByName.Id, selectedId, StringComparison.OrdinalIgnoreCase))
            {
                selectedId = existingByName.Id;
                _currentStore.SelectedCloudSourceId = selectedId;
            }
            else if (IsBuiltInCloudSource(selectedId) &&
                     !string.Equals(typedName, FindCloudSource(selectedId)?.Name, StringComparison.OrdinalIgnoreCase))
            {
                selectedId = Guid.NewGuid().ToString("N");
                sources.Add(new ProfileDefinition
                {
                    Id = selectedId,
                    Name = typedName,
                    Notes = string.Empty,
                    Codex = new ClientProfile(),
                    Claude = new ClientProfile(),
                    Gemini = new ClientProfile(),
                    Grok = new ClientProfile()
                });
                _currentStore.SelectedCloudSourceId = selectedId;
                AppendLine($"已按名称自动新增云端来源组：{typedName}");
            }
        }

        var selected = sources.FirstOrDefault(x => string.Equals(x.Id, selectedId, StringComparison.OrdinalIgnoreCase));
        if (selected is null)
        {
            selected = ProfileDefinition.CreateCloudDefaults();
            selected.Id = selectedId;
            sources.Add(selected);
        }

        selected.Name = string.IsNullOrWhiteSpace(typedName) ? "云端" : typedName;
        selected.Notes = _cloudNotesBox.Text.Trim();
        selected.Codex.BaseUrl = _cloudCodexBaseBox.Text.Trim();
        selected.Codex.Secret = _cloudCodexKeyBox.Text.Trim();
        selected.Claude.BaseUrl = _cloudClaudeBaseBox.Text.Trim();
        selected.Claude.Secret = _cloudClaudeKeyBox.Text.Trim();
        selected.Gemini.BaseUrl = _cloudGeminiBaseBox.Text.Trim();
        selected.Gemini.Secret = _cloudGeminiKeyBox.Text.Trim();
        selected.Grok.BaseUrl = _cloudGrokBaseBox.Text.Trim();
        selected.Grok.Secret = _cloudGrokKeyBox.Text.Trim();
        return sources;
    }

    private List<ProfileDefinition> CloneLocalSourcesWithCurrentFields()
    {
        var selectedId = GetSelectedLocalSourceId();
        var sources = _currentStore.LocalSources.Select(CloneProfile).ToList();
        var typedName = _localSourceNameBox.Text.Trim();
        if (!string.IsNullOrWhiteSpace(typedName))
        {
            var existingByName = sources.FirstOrDefault(x =>
                string.Equals(x.Name, typedName, StringComparison.OrdinalIgnoreCase));
            if (existingByName is not null &&
                !string.Equals(existingByName.Id, selectedId, StringComparison.OrdinalIgnoreCase))
            {
                selectedId = existingByName.Id;
                _currentStore.SelectedLocalSourceId = selectedId;
            }
            else if (IsBuiltInLocalSource(selectedId) &&
                     !string.Equals(typedName, FindLocalSource(selectedId)?.Name, StringComparison.OrdinalIgnoreCase))
            {
                selectedId = Guid.NewGuid().ToString("N");
                sources.Add(new ProfileDefinition
                {
                    Id = selectedId,
                    Name = typedName,
                    Notes = string.Empty,
                    Codex = new ClientProfile(),
                    Claude = new ClientProfile(),
                    Gemini = new ClientProfile(),
                    Grok = new ClientProfile()
                });
                _currentStore.SelectedLocalSourceId = selectedId;
                AppendLine($"已按名称自动新增本地中转来源：{typedName}");
            }
        }

        var selected = sources.FirstOrDefault(x => string.Equals(x.Id, selectedId, StringComparison.OrdinalIgnoreCase));
        if (selected is null)
        {
            selected = ProfileDefinition.CreateLocalDefaults();
            selected.Id = selectedId;
            sources.Add(selected);
        }

        selected.Name = string.IsNullOrWhiteSpace(typedName) ? "本地中转" : typedName;
        selected.Notes = _localNotesBox.Text.Trim();
        selected.Codex.BaseUrl = _localCodexBaseBox.Text.Trim();
        selected.Codex.Secret = _localCodexKeyBox.Text.Trim();
        selected.Claude.BaseUrl = _localClaudeBaseBox.Text.Trim();
        selected.Claude.Secret = _localClaudeKeyBox.Text.Trim();
        selected.Gemini.BaseUrl = _localGeminiBaseBox.Text.Trim();
        selected.Gemini.Secret = _localGeminiKeyBox.Text.Trim();
        selected.Grok.BaseUrl = _localGrokBaseBox.Text.Trim();
        selected.Grok.Secret = _localGrokKeyBox.Text.Trim();
        return sources;
    }

    private void AddCloudSource()
    {
        _currentStore = CollectProfilesFromForm();
        var index = _currentStore.CloudSources.Count + 1;
        var source = new ProfileDefinition
        {
            Id = Guid.NewGuid().ToString("N"),
            Name = $"云端 {index}",
            Notes = "新增的云端中转来源组。",
            Codex = new ClientProfile(),
            Claude = new ClientProfile(),
            Gemini = new ClientProfile(),
            Grok = new ClientProfile()
        };

        _currentStore.CloudSources.Add(source);
        _currentStore.SelectedCloudSourceId = source.Id;
        LoadSourceGrid(_cloudSourcesGrid, _currentStore.CloudSources);
        RefreshSourceCombos(_currentStore);
        SelectSourceOption(_cloudSourceBox, source.Id);
        LoadSelectedCloudSourceIntoFields();
        UpdatePendingActionSummary();
        AppendLine($"已新增云端来源组：{source.Name}");
    }

    private void DeleteSelectedCloudSource()
    {
        _currentStore = CollectProfilesFromForm();
        var selectedId = GetSelectedCloudSourceId();
        var source = _currentStore.CloudSources.FirstOrDefault(x => string.Equals(x.Id, selectedId, StringComparison.OrdinalIgnoreCase));
        if (source is null || IsBuiltInCloudSource(source.Id) || _currentStore.CloudSources.Count <= 1)
        {
            AppendLine("内置云端组或最后一个云端组不能删除。");
            return;
        }

        _currentStore.CloudSources.Remove(source);
        _currentStore.SelectedCloudSourceId = _currentStore.CloudSources[0].Id;
        LoadSourceGrid(_cloudSourcesGrid, _currentStore.CloudSources);
        if (string.Equals(_currentStore.Mixed.CodexSourceId, source.Id, StringComparison.OrdinalIgnoreCase))
        {
            _currentStore.Mixed.CodexSourceId = _currentStore.SelectedCloudSourceId;
        }

        if (string.Equals(_currentStore.Mixed.ClaudeSourceId, source.Id, StringComparison.OrdinalIgnoreCase))
        {
            _currentStore.Mixed.ClaudeSourceId = _currentStore.SelectedCloudSourceId;
        }

        if (string.Equals(_currentStore.Mixed.GeminiSourceId, source.Id, StringComparison.OrdinalIgnoreCase))
        {
            _currentStore.Mixed.GeminiSourceId = _currentStore.SelectedCloudSourceId;
        }

        if (string.Equals(_currentStore.Mixed.GrokSourceId, source.Id, StringComparison.OrdinalIgnoreCase))
        {
            _currentStore.Mixed.GrokSourceId = _currentStore.SelectedCloudSourceId;
        }

        RefreshSourceCombos(_currentStore);
        SelectSourceOption(_cloudSourceBox, _currentStore.SelectedCloudSourceId);
        LoadSelectedCloudSourceIntoFields();
        UpdatePendingActionSummary();
        AppendLine($"已删除云端来源组：{source.Name}");
    }

    private void AddLocalSource()
    {
        _currentStore = CollectProfilesFromForm();
        var index = _currentStore.LocalSources.Count + 1;
        var source = new ProfileDefinition
        {
            Id = Guid.NewGuid().ToString("N"),
            Name = $"本地中转 {index}",
            Notes = "新增的本地/局域网中转来源。",
            Codex = new ClientProfile { BaseUrl = "http://192.168.x.x:8080/v1" },
            Claude = new ClientProfile { BaseUrl = "http://192.168.x.x:8080" },
            Gemini = new ClientProfile { BaseUrl = "http://192.168.x.x:8080" },
            Grok = new ClientProfile { BaseUrl = "http://192.168.x.x:8080/v1" }
        };

        _currentStore.LocalSources.Add(source);
        _currentStore.SelectedLocalSourceId = source.Id;
        LoadSourceGrid(_localSourcesGrid, _currentStore.LocalSources);
        RefreshSourceCombos(_currentStore);
        SelectSourceOption(_localSourceBox, source.Id);
        LoadSelectedLocalSourceIntoFields();
        UpdatePendingActionSummary();
        AppendLine($"已新增本地中转来源：{source.Name}");
    }

    private void DeleteSelectedLocalSource()
    {
        _currentStore = CollectProfilesFromForm();
        var selectedId = GetSelectedLocalSourceId();
        var source = _currentStore.LocalSources.FirstOrDefault(x => string.Equals(x.Id, selectedId, StringComparison.OrdinalIgnoreCase));
        if (source is null || IsBuiltInLocalSource(source.Id) || _currentStore.LocalSources.Count <= 1)
        {
            AppendLine("内置来源或最后一个来源不能删除。");
            return;
        }

        _currentStore.LocalSources.Remove(source);
        _currentStore.SelectedLocalSourceId = _currentStore.LocalSources[0].Id;
        LoadSourceGrid(_localSourcesGrid, _currentStore.LocalSources);
        if (string.Equals(_currentStore.Mixed.CodexSourceId, source.Id, StringComparison.OrdinalIgnoreCase))
        {
            _currentStore.Mixed.CodexSourceId = ProfileSourceIds.Cloud;
        }

        if (string.Equals(_currentStore.Mixed.ClaudeSourceId, source.Id, StringComparison.OrdinalIgnoreCase))
        {
            _currentStore.Mixed.ClaudeSourceId = _currentStore.SelectedLocalSourceId;
        }

        if (string.Equals(_currentStore.Mixed.GeminiSourceId, source.Id, StringComparison.OrdinalIgnoreCase))
        {
            _currentStore.Mixed.GeminiSourceId = _currentStore.SelectedLocalSourceId;
        }

        if (string.Equals(_currentStore.Mixed.GrokSourceId, source.Id, StringComparison.OrdinalIgnoreCase))
        {
            _currentStore.Mixed.GrokSourceId = _currentStore.SelectedLocalSourceId;
        }

        RefreshSourceCombos(_currentStore);
        SelectSourceOption(_localSourceBox, _currentStore.SelectedLocalSourceId);
        LoadSelectedLocalSourceIntoFields();
        UpdatePendingActionSummary();
        AppendLine($"已删除本地中转来源：{source.Name}");
    }

    private static bool IsBuiltInLocalSource(string sourceId)
    {
        return string.Equals(sourceId, ProfileSourceIds.LocalMachine, StringComparison.OrdinalIgnoreCase) ||
               string.Equals(sourceId, ProfileSourceIds.LanDefault, StringComparison.OrdinalIgnoreCase);
    }

    private static bool IsBuiltInCloudSource(string sourceId)
    {
        return string.Equals(sourceId, ProfileSourceIds.Cloud, StringComparison.OrdinalIgnoreCase);
    }

    private static ProfileDefinition CloneProfile(ProfileDefinition source)
    {
        return new ProfileDefinition
        {
            Id = source.Id,
            Name = source.Name,
            Notes = source.Notes,
            Codex = new ClientProfile
            {
                BaseUrl = source.Codex.BaseUrl,
                Secret = source.Codex.Secret
            },
            Claude = new ClientProfile
            {
                BaseUrl = source.Claude.BaseUrl,
                Secret = source.Claude.Secret
            },
            Gemini = new ClientProfile
            {
                BaseUrl = source.Gemini.BaseUrl,
                Secret = source.Gemini.Secret
            },
            Grok = new ClientProfile
            {
                BaseUrl = source.Grok.BaseUrl,
                Secret = source.Grok.Secret
            }
        };
    }

    private static string GetSourceDisplayName(ProfileStore store, string sourceId)
    {
        var cloud = store.CloudSources.FirstOrDefault(x =>
            string.Equals(x.Id, sourceId, StringComparison.OrdinalIgnoreCase));
        if (cloud is not null) return cloud.Name;

        return store.LocalSources.FirstOrDefault(x =>
            string.Equals(x.Id, sourceId, StringComparison.OrdinalIgnoreCase))?.Name ?? "未知来源";
    }

    private void AppendResult(OperationResult result)
    {
        AppendLine(result.Summary);

        var codexDetail = result.Details.FirstOrDefault(x => x.Name == "Codex");
        if (codexDetail is not null)
        {
            _codexValidationValue.Text = codexDetail.Success ? "验证成功" : "验证失败";
        }

        var claudeDetail = result.Details.FirstOrDefault(x => x.Name == "Claude Code");
        if (claudeDetail is not null)
        {
            _claudeValidationValue.Text = claudeDetail.Success ? "验证成功" : "验证失败";
        }

        var geminiDetail = result.Details.FirstOrDefault(x => x.Name == "Gemini CLI");
        if (geminiDetail is not null)
        {
            _geminiValidationValue.Text = geminiDetail.Success ? "验证成功" : "验证失败";
        }

        foreach (var detail in result.Details)
        {
            AppendLine($"[{detail.Name}] {(detail.Success ? "OK" : "FAIL")} - {detail.Message}");
        }

        AppendLine(string.Empty);
    }

    private void AppendLine(string text)
    {
        _statusBox.AppendText($"[{DateTime.Now:HH:mm:ss}] {text}{Environment.NewLine}");
    }

    private void ToggleButtons(bool enabled)
    {
        _saveButton.Enabled = enabled;
        _applySelectedButton.Enabled = enabled;
        _testSelectedButton.Enabled = enabled;
        _openSelectedSiteButton.Enabled = enabled;
        _importToCloudButton.Enabled = enabled;
        _addCloudSourceButton.Enabled = enabled;
        _deleteCloudSourceButton.Enabled = enabled && _currentStore.CloudSources.Count > 1 && !IsBuiltInCloudSource(GetSelectedCloudSourceId());
        _importToLocalButton.Enabled = enabled;
        _addLocalSourceButton.Enabled = enabled;
        _deleteLocalSourceButton.Enabled = enabled && _currentStore.LocalSources.Count > 1 && !IsBuiltInLocalSource(GetSelectedLocalSourceId());
        _statsRefreshButton.Enabled = enabled;
        if (enabled)
        {
            ApplyLocalGatewayButtonAvailability();
        }
        else
        {
            _localGatewayStartButton.Enabled = false;
            _localGatewayRestartButton.Enabled = false;
            _localGatewayStopButton.Enabled = false;
            _localGatewayRefreshButton.Enabled = false;
            _localGatewayOpenAdminButton.Enabled = false;
            _localGatewayOpenLanAdminButton.Enabled = false;
            _convertCpaAccountsButton.Enabled = false;
        }
    }

    private void AddField(TableLayoutPanel panel, string label, Control input, Control? sideButton, int row)
    {
        panel.RowStyles.Add(new RowStyle(SizeType.AutoSize));

        panel.Controls.Add(CreateInputLabel(label), 0, row);

        input.Margin = new Padding(0, ScaleValue(4), ScaleValue(8), ScaleValue(10));
        input.Dock = DockStyle.Top;
        panel.Controls.Add(input, 1, row);

        if (sideButton is null)
        {
            panel.Controls.Add(new Panel { Dock = DockStyle.Fill }, 2, row);
        }
        else
        {
            sideButton.Margin = new Padding(0, ScaleValue(4), 0, ScaleValue(10));
            sideButton.Dock = DockStyle.Fill;
            panel.Controls.Add(sideButton, 2, row);
        }
    }

    private Label CreateInputLabel(string text)
    {
        return new Label
        {
            Text = text,
            AutoSize = true,
            Margin = new Padding(0, ScaleValue(10), ScaleValue(8), ScaleValue(8)),
            ForeColor = TextMain,
            Font = _uiFont
        };
    }

    private void ConfigureTextBox(TextBox textBox, bool multiline = false)
    {
        textBox.Multiline = multiline;
        textBox.BorderStyle = BorderStyle.FixedSingle;
        textBox.BackColor = Color.White;
        textBox.ForeColor = TextMain;
        textBox.Font = _uiFont;
        textBox.Margin = new Padding(0, ScaleValue(3), ScaleValue(8), ScaleValue(10));
        if (!multiline)
        {
            textBox.Height = ScaleValue(30);
        }
    }

    private void ConfigureSecretBox(TextBox textBox)
    {
        ConfigureTextBox(textBox);
        textBox.UseSystemPasswordChar = true;
    }

    private void ConfigureComboBox(ComboBox comboBox)
    {
        comboBox.DropDownStyle = ComboBoxStyle.DropDownList;
        comboBox.Font = _uiFont;
        comboBox.Width = ScaleValue(260);
        comboBox.FlatStyle = FlatStyle.Flat;
        comboBox.SelectedIndexChanged += (_, _) => UpdatePendingActionSummary();
    }

    private Button CreateToggleButton(TextBox target)
    {
        var button = new Button
        {
            Text = "显示",
            Width = ScaleValue(76),
            Height = ScaleValue(30),
            FlatStyle = FlatStyle.Flat,
            BackColor = Color.White,
            ForeColor = TextMain,
            Font = _uiFont
        };
        button.FlatAppearance.BorderColor = Border;
        button.Click += (_, _) =>
        {
            target.UseSystemPasswordChar = !target.UseSystemPasswordChar;
            button.Text = target.UseSystemPasswordChar ? "显示" : "隐藏";
        };
        return button;
    }

    private void ConfigurePrimaryButton(Button button, string text, Color color, EventHandler onClick)
    {
        button.Text = text;
        button.Height = ScaleValue(38);
        button.Dock = DockStyle.Fill;
        button.Margin = new Padding(0, 0, ScaleValue(8), ScaleValue(8));
        button.FlatStyle = FlatStyle.Flat;
        button.BackColor = color;
        button.ForeColor = Color.White;
        button.Font = _strongFont;
        button.FlatAppearance.BorderSize = 0;
        button.FlatAppearance.MouseOverBackColor = ControlPaint.Dark(color);
        button.FlatAppearance.MouseDownBackColor = ControlPaint.Dark(color);
        button.Click += onClick;
    }

    private void ConfigureSecondaryButton(Button button, string text, EventHandler onClick)
    {
        button.Text = text;
        button.Height = ScaleValue(34);
        button.Dock = DockStyle.Fill;
        button.Margin = new Padding(0, 0, ScaleValue(8), ScaleValue(8));
        button.FlatStyle = FlatStyle.Flat;
        button.BackColor = Color.FromArgb(250, 252, 255);
        button.ForeColor = TextMain;
        button.Font = _uiFont;
        button.FlatAppearance.BorderColor = Border;
        button.FlatAppearance.MouseOverBackColor = Color.FromArgb(241, 246, 253);
        button.FlatAppearance.MouseDownBackColor = Color.FromArgb(232, 240, 251);
        button.Click += onClick;
    }

    private void ConfigureBadge(Label label)
    {
        label.AutoSize = false;
        label.Size = ScaleSize(new Size(200, 32));
        label.TextAlign = ContentAlignment.MiddleCenter;
        label.Font = _strongFont;
        label.Margin = new Padding(0, 0, 0, ScaleValue(8));
        label.BorderStyle = BorderStyle.None;
        
        label.Paint += (sender, e) =>
        {
            if (sender is not Label lbl) return;
            
            e.Graphics.SmoothingMode = SmoothingMode.AntiAlias;
            e.Graphics.Clear(lbl.Parent?.BackColor ?? AppBg);
            
            using var brush = new SolidBrush(lbl.BackColor);
            var rect = new Rectangle(0, 0, lbl.Width - 1, lbl.Height - 1);
            
            using var path = new GraphicsPath();
            int radius = lbl.Height / 2 - 1;
            int diameter = radius * 2;
            path.AddArc(rect.X, rect.Y, diameter, diameter, 180, 90);
            path.AddArc(rect.Right - diameter, rect.Y, diameter, diameter, 270, 90);
            path.AddArc(rect.Right - diameter, rect.Bottom - diameter, diameter, diameter, 0, 90);
            path.AddArc(rect.X, rect.Bottom - diameter, diameter, diameter, 90, 90);
            path.CloseFigure();
            
            e.Graphics.FillPath(brush, path);
            
            TextRenderer.DrawText(
                e.Graphics,
                lbl.Text,
                lbl.Font,
                rect,
                lbl.ForeColor,
                TextFormatFlags.HorizontalCenter | TextFormatFlags.VerticalCenter | TextFormatFlags.EndEllipsis
            );
        };
    }

    private Control CreateSectionHeader(string title, string subtitle)
    {
        var layout = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            ColumnCount = 1,
            RowCount = 2,
            AutoSize = true,
            BackColor = Surface,
            Margin = Padding.Empty
        };

        layout.Controls.Add(new Label
        {
            Text = title,
            AutoSize = true,
            ForeColor = TextMain,
            Font = _sectionFont,
            Margin = new Padding(0, 0, 0, ScaleValue(4))
        }, 0, 0);

        layout.Controls.Add(new Label
        {
            Text = subtitle,
            AutoSize = true,
            MaximumSize = ScaleSize(new Size(700, 0)),
            ForeColor = TextMuted,
            Font = _uiFont,
            Margin = Padding.Empty
        }, 0, 1);

        return layout;
    }

    private Panel CreateSoftSection(string title)
    {
        var panel = new Panel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            BackColor = SurfaceAlt,
            Padding = new Padding(ScaleValue(16), ScaleValue(36), ScaleValue(16), ScaleValue(16)),
            Margin = new Padding(0, 0, 0, ScaleValue(12))
        };
        panel.Paint += DrawBorder;
        panel.Controls.Add(new Label
        {
            Text = title,
            AutoSize = true,
            ForeColor = TextMain,
            Font = _strongFont,
            BackColor = Color.Transparent,
            Location = new Point(ScaleValue(14), ScaleValue(10))
        });
        return panel;
    }

    private Control CreateMetricTile(string title, Label valueLabel)
    {
        var panel = new Panel
        {
            Dock = DockStyle.Fill,
            BackColor = SurfaceAlt,
            Margin = new Padding(0, 0, 0, ScaleValue(6)),
            Padding = new Padding(ScaleValue(14), ScaleValue(6), ScaleValue(14), ScaleValue(6)),
            MinimumSize = ScaleSize(new Size(0, 42)),
            MaximumSize = ScaleSize(new Size(0, 42))
        };
        panel.Paint += DrawBorder;

        valueLabel.AutoSize = false;
        valueLabel.AutoEllipsis = true;
        valueLabel.UseMnemonic = false;
        valueLabel.ForeColor = TextMain;
        valueLabel.Font = _strongFont;
        valueLabel.Dock = DockStyle.Fill;
        valueLabel.Margin = Padding.Empty;
        valueLabel.TextAlign = ContentAlignment.MiddleLeft;
        var titleLabel = new Label
        {
            Text = title,
            AutoSize = false,
            ForeColor = TextMuted,
            Font = _uiFont,
            Dock = DockStyle.Fill,
            TextAlign = ContentAlignment.MiddleLeft,
            Margin = Padding.Empty
        };

        var layout = new TableLayoutPanel
        {
            Dock = DockStyle.Fill,
            ColumnCount = 2,
            RowCount = 1,
            BackColor = panel.BackColor,
            Margin = Padding.Empty,
            Padding = Padding.Empty
        };
        layout.ColumnStyles.Add(CreateScaledAbsoluteColumn(86));
        layout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));
        layout.Controls.Add(titleLabel, 0, 0);
        layout.Controls.Add(valueLabel, 1, 0);

        panel.Controls.Add(layout);
        return panel;
    }

    private Control CreateCompactMetric(string title, Label valueLabel)
    {
        var layout = new TableLayoutPanel
        {
            Dock = DockStyle.Top,
            AutoSize = true,
            AutoSizeMode = AutoSizeMode.GrowAndShrink,
            ColumnCount = 2,
            RowCount = 1,
            BackColor = Surface,
            Margin = new Padding(0, 0, 0, ScaleValue(6))
        };
        layout.ColumnStyles.Add(CreateScaledAbsoluteColumn(68));
        layout.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F));

        layout.Controls.Add(new Label
        {
            Text = title,
            AutoSize = true,
            ForeColor = TextMuted,
            Font = _uiFont,
            Margin = Padding.Empty
        }, 0, 0);

        valueLabel.AutoSize = true;
        valueLabel.AutoEllipsis = true;
        valueLabel.MaximumSize = ScaleSize(new Size(260, 0));
        valueLabel.ForeColor = TextMain;
        valueLabel.Font = _strongFont;
        valueLabel.Margin = Padding.Empty;
        layout.Controls.Add(valueLabel, 1, 0);
        return layout;
    }

    private static Panel CreateCardPanel()
    {
        var panel = new Panel
        {
            BackColor = Surface,
            Margin = Padding.Empty
        };
        panel.Paint += DrawCardBorder;
        return panel;
    }

    private static void DrawCardBorder(object? sender, PaintEventArgs e)
    {
        if (sender is not Panel panel)
        {
            return;
        }

        e.Graphics.SmoothingMode = SmoothingMode.AntiAlias;
        using var pen = new Pen(Color.FromArgb(218, 226, 238));
        var rect = new Rectangle(0, 0, panel.Width - 1, panel.Height - 1);
        DrawRoundedRectangle(e.Graphics, pen, rect, 16);
    }

    private static void DrawBorder(object? sender, PaintEventArgs e)
    {
        if (sender is not Panel panel)
        {
            return;
        }

        e.Graphics.SmoothingMode = SmoothingMode.AntiAlias;
        using var pen = new Pen(Color.FromArgb(216, 226, 239));
        var rect = new Rectangle(0, 0, panel.Width - 1, panel.Height - 1);
        DrawRoundedRectangle(e.Graphics, pen, rect, 12);
    }

    private static void DrawBrandBorder(object? sender, PaintEventArgs e)
    {
        if (sender is not Control control)
        {
            return;
        }

        e.Graphics.SmoothingMode = SmoothingMode.AntiAlias;
        using var pen = new Pen(Color.FromArgb(96, 150, 255));
        var rect = new Rectangle(0, 0, control.Width - 1, control.Height - 1);
        DrawRoundedRectangle(e.Graphics, pen, rect, 14);
    }

    private static void DrawRoundedRectangle(Graphics graphics, Pen pen, Rectangle rect, int radius)
    {
        if (rect.Width <= 1 || rect.Height <= 1)
        {
            return;
        }

        radius = Math.Min(radius, Math.Min(rect.Width, rect.Height) / 2);
        if (radius <= 0)
        {
            graphics.DrawRectangle(pen, rect);
            return;
        }

        using var path = new GraphicsPath();
        var diameter = radius * 2;
        path.AddArc(rect.X, rect.Y, diameter, diameter, 180, 90);
        path.AddArc(rect.Right - diameter, rect.Y, diameter, diameter, 270, 90);
        path.AddArc(rect.Right - diameter, rect.Bottom - diameter, diameter, diameter, 0, 90);
        path.AddArc(rect.X, rect.Bottom - diameter, diameter, diameter, 90, 90);
        path.CloseFigure();
        graphics.DrawPath(pen, path);
    }
}
