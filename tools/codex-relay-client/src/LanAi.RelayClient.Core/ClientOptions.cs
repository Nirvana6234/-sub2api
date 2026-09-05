namespace LanAi.RelayClient;

/// <summary>Build-time client configuration.</summary>
/// <remarks>
/// <para>
/// Lives here rather than beside a window because two heads now need it, and the one
/// thing worse than duplicating a constant is duplicating this one: a copy that drifts
/// sends a build at the wrong relay, and nothing about the running client says which
/// one it is talking to.
/// </para>
/// <para>
/// <c>TEST_SERVER</c> is turned on by the <c>TestServer</c> MSBuild property, whose
/// conditional group lives in <b>this project only</b>. A head does not need its own
/// copy: <see cref="ServerAddress"/> is a <c>const</c>, so a referencing assembly
/// inlines the value out of this one's metadata at its own compile time, and passing
/// <c>-p:TestServer=true</c> sets a global property that forces this project to
/// rebuild with the define before the head compiles against it.
/// </para>
/// <para>
/// Verified rather than assumed, in both directions and on the published binaries —
/// the head's own assembly, not just this one — because this is the single value
/// that, if wrong, points real users' credentials at the wrong host. A build with
/// the flag carries <c>test.gongfeiai.com</c> and not the production address; a
/// build without it carries the production address and not the test one.
/// </para>
/// </remarks>
internal static class ClientOptions
{
    /// <summary>What this build reports itself as.</summary>
    /// <remarks>
    /// Compared against <c>client-version.json</c> to decide whether to offer an
    /// update, so it has to move with every release the server advertises — a build
    /// left behind the manifest tells its own users, forever, that they are out of
    /// date. Whatever displays it must derive from here rather than restate it; see
    /// <see cref="ViewModels.ClientUpdateViewModel.CurrentVersionText"/>.
    /// </remarks>
    public static readonly Version CurrentVersion = new(0, 2);

    /// <summary>
    /// The relay this build talks to.
    /// </summary>
#if TEST_SERVER
    // 明文：该域名的证书当前不受信任（SSL/TLS 信任关系建立失败），走 https
    // 连不上。测试渠道接受这个取舍，代价是登录凭据与中转 API Key 明文传输 ——
    // 证书修好后应改回 https。正式渠道不受影响。
    public const string ServerAddress = "http://test.gongfeiai.com/";
#else
    public const string ServerAddress = "https://gongfeiai.com/";
#endif
}
