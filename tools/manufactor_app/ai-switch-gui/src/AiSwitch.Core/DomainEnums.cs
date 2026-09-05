namespace LanAi.Workspace.Core;

public enum ConnectionProfileKind
{
    Local,
    Lan,
    Cloud,
}

public enum ClientConfigurationMode
{
    KeepExisting,
    Managed,
}

public enum ConversationStorageMode
{
    NativeIndex,
    EncryptedImport,
    Managed,
}

public enum ConversationStatus
{
    Available,
    SourceMissing,
    ClientMissing,
    Archived,
}

public enum CliLaunchMode
{
    New,
    Resume,
    Fork,
}

[Flags]
public enum CliCapability
{
    None = 0,
    NewSession = 1 << 0,
    ResumeSession = 1 << 1,
    ForkSession = 1 << 2,
    ListSessions = 1 << 3,
    StructuredOutput = 1 << 4,
    ConfigurationOverride = 1 << 5,
}
