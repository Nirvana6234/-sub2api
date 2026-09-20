using System.ComponentModel;
using System.Runtime.CompilerServices;
using Avalonia.Controls;
using Avalonia.Interactivity;
using Avalonia.Markup.Xaml;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient.App.Views;

/// <summary>Selects the OpenAI groups and policy used by automatic routing.</summary>
public partial class AutoGroupDialog : Window, INotifyPropertyChanged
{
    private static readonly string[] Labels = ["低价优先", "均衡", "速度优先"];
    private static readonly string[] Keys = ["price", "balanced", "speed"];
    private string _selectedStrategyLabel = Labels[0];
    private string _validationMessage = string.Empty;

    public AutoGroupDialog()
    {
        Candidates = [];
        InitializeComponent();
        DataContext = this;
    }

    private AutoGroupDialog(PawAutoGroupSettings settings, IReadOnlyList<GroupItemViewModel> candidates)
    {
        Candidates = candidates;
        HashSet<long> selected = settings.AutoGroupIds.ToHashSet();
        foreach (GroupItemViewModel candidate in Candidates)
        {
            candidate.IsAutoGroupCandidateSelected = selected.Contains(candidate.Id);
        }

        int strategy = Array.IndexOf(Keys, settings.AutoGroupStrategy);
        SelectedStrategyLabel = Labels[strategy >= 0 ? strategy : 0];
        InitializeComponent();
        DataContext = this;
    }

    public IReadOnlyList<GroupItemViewModel> Candidates { get; }
    public IReadOnlyList<string> StrategyLabels => Labels;
    public PawAutoGroupSettings? Result { get; private set; }

    public string SelectedStrategyLabel
    {
        get => _selectedStrategyLabel;
        set
        {
            if (_selectedStrategyLabel == value)
            {
                return;
            }

            _selectedStrategyLabel = value;
            OnPropertyChanged();
        }
    }

    public string ValidationMessage
    {
        get => _validationMessage;
        private set
        {
            if (_validationMessage == value)
            {
                return;
            }

            _validationMessage = value;
            OnPropertyChanged();
        }
    }

    public new event PropertyChangedEventHandler? PropertyChanged;

    internal static async Task<PawAutoGroupSettings?> ShowAsync(
        Window owner,
        PawAutoGroupSettings settings,
        IReadOnlyList<GroupItemViewModel> candidates)
    {
        var dialog = new AutoGroupDialog(settings, candidates);
        await dialog.ShowDialog(owner);
        return dialog.Result;
    }

    private void InitializeComponent() => AvaloniaXamlLoader.Load(this);

    private void Save_OnClick(object? sender, RoutedEventArgs e)
    {
        long[] ids = Candidates
            .Where(candidate => candidate.IsAutoGroupCandidateSelected)
            .Select(candidate => candidate.Id)
            .ToArray();
        if (ids.Length == 0)
        {
            ValidationMessage = "请至少选择一个候选分组。";
            return;
        }

        int strategy = Array.IndexOf(Labels, SelectedStrategyLabel);
        Result = new PawAutoGroupSettings(true, ids, Keys[strategy >= 0 ? strategy : 0]);
        Close();
    }

    private void Cancel_OnClick(object? sender, RoutedEventArgs e) => Close();

    private void OnPropertyChanged([CallerMemberName] string? name = null) =>
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}
