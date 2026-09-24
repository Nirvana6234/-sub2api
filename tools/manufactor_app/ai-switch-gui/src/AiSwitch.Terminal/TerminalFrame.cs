namespace LanAi.Workspace.Terminal;

public sealed record TerminalFrame(
    int Columns,
    int Rows,
    IReadOnlyList<string> Lines,
    int CursorColumn,
    int CursorRow,
    bool CursorVisible,
    bool IsAlternateBuffer,
    string Title,
    bool IsRunning);

