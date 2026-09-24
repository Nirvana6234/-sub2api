namespace LanAi.Workspace.Terminal;

public enum TerminalInputKey
{
    Enter,
    Tab,
    Backspace,
    Escape,
    Up,
    Down,
    Left,
    Right,
    Home,
    End,
    PageUp,
    PageDown,
    Insert,
    Delete,
    F1,
    F2,
    F3,
    F4,
    F5,
    F6,
    F7,
    F8,
    F9,
    F10,
    F11,
    F12
}

[Flags]
public enum TerminalInputModifiers
{
    None = 0,
    Shift = 1,
    Alt = 2,
    Control = 4
}

