namespace LanAi.RelayClient.Services;

/// <summary>Where a signed-in session is kept between runs.</summary>
/// <remarks>
/// The contract is platform-neutral; the storage behind it is not. Windows encrypts
/// with DPAPI, macOS will use the Keychain, and both are reached only through here so
/// that session handling itself never has to know which one it is talking to.
/// </remarks>
internal interface ISessionStore
{
    /// <summary>Returns the stored session, or null when there is none to use.</summary>
    StoredSession? Load();

    void Save(StoredSession session);

    void Clear();
}
