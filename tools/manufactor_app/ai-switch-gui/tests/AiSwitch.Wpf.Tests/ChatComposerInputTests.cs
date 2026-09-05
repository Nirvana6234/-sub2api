using System.Windows.Input;
using LanAi.Workspace.Wpf.Views;

namespace AiSwitch.Wpf.Tests;

public sealed class ChatComposerInputTests
{
    [Theory]
    [InlineData(Key.Enter, ModifierKeys.None, false)]
    [InlineData(Key.Enter, ModifierKeys.Shift, false)]
    [InlineData(Key.Enter, ModifierKeys.Control, true)]
    [InlineData(Key.Enter, ModifierKeys.Control | ModifierKeys.Shift, true)]
    [InlineData(Key.Enter, ModifierKeys.Windows, false)]
    [InlineData(Key.A, ModifierKeys.Control, false)]
    public void SendGesture_RequiresControlAndEnter(Key key, ModifierKeys modifiers, bool expected)
    {
        Assert.Equal(expected, ChatView.IsComposerSendGesture(key, modifiers));
    }
}
