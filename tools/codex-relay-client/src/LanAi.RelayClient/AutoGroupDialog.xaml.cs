using System.ComponentModel;
using System.Runtime.CompilerServices;
using System.Windows;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.ViewModels;

namespace LanAi.RelayClient;

public partial class AutoGroupDialog : Window, INotifyPropertyChanged
{
    private static readonly string[] Labels = ["低价优先", "均衡", "速度优先"];
    private static readonly string[] Keys = ["price", "balanced", "speed"];
    private string _selectedStrategyLabel = Labels[0];
    private string _validationMessage = string.Empty;

    public AutoGroupDialog(PawAutoGroupSettings settings, IReadOnlyList<GroupItemViewModel> candidates)
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
        set { _selectedStrategyLabel = value; OnPropertyChanged(); }
    }

    public string ValidationMessage
    {
        get => _validationMessage;
        private set { _validationMessage = value; OnPropertyChanged(); }
    }

    public event PropertyChangedEventHandler? PropertyChanged;

    private void Save_OnClick(object sender, RoutedEventArgs e)
    {
        long[] ids = Candidates.Where(c => c.IsAutoGroupCandidateSelected).Select(c => c.Id).ToArray();
        if (ids.Length == 0)
        {
            ValidationMessage = "请至少选择一个候选分组。";
            return;
        }
        int strategy = Array.IndexOf(Labels, SelectedStrategyLabel);
        Result = new PawAutoGroupSettings(true, ids, Keys[strategy >= 0 ? strategy : 0]);
        DialogResult = true;
    }

    private void OnPropertyChanged([CallerMemberName] string? name = null) =>
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}
