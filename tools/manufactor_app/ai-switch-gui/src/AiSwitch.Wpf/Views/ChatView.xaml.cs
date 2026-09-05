using System.Collections.Specialized;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using LanAi.Workspace.Wpf.ViewModels;

namespace LanAi.Workspace.Wpf.Views;

public partial class ChatView : UserControl
{
    private INotifyCollectionChanged? _messages;

    public ChatView() => InitializeComponent();

    private async void ChatView_OnLoaded(object sender, RoutedEventArgs e)
    {
        if (DataContext is not ChatViewModel viewModel)
        {
            return;
        }

        viewModel.RefreshContext();
        _messages = viewModel.Messages;
        _messages.CollectionChanged += Messages_OnCollectionChanged;
        ComposerTextBox.Focus();
        await viewModel.ActivateAsync();
    }

    private void ChatView_OnUnloaded(object sender, RoutedEventArgs e)
    {
        if (_messages is not null)
        {
            _messages.CollectionChanged -= Messages_OnCollectionChanged;
            _messages = null;
        }
    }

    private void Messages_OnCollectionChanged(object? sender, NotifyCollectionChangedEventArgs e) =>
        _ = Dispatcher.BeginInvoke(new Action(MessagesScrollViewer.ScrollToEnd));

    private void ComposerTextBox_OnKeyDown(object sender, KeyEventArgs e)
    {
        bool sendGesture = IsComposerSendGesture(e.Key, Keyboard.Modifiers);
        if (!sendGesture ||
            DataContext is not ChatViewModel viewModel || !viewModel.SendCommand.CanExecute(null))
        {
            return;
        }

        e.Handled = true;
        viewModel.SendCommand.Execute(null);
    }

    internal static bool IsComposerSendGesture(Key key, ModifierKeys modifiers)
    {
        // The composer is a real multi-line editor: a plain Enter inserts a
        // newline, while Ctrl+Enter is the explicit send gesture advertised by
        // the UI. This avoids accidentally dispatching a half-written prompt.
        // In WPF Key.Return is an alias of Key.Enter, so the single comparison
        // covers both without treating the Windows key as macOS Command.
        return key == Key.Enter && modifiers.HasFlag(ModifierKeys.Control);
    }
}
