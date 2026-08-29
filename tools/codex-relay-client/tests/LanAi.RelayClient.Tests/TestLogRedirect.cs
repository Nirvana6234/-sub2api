using System.IO;
using System.Runtime.CompilerServices;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Points the client log at a scratch file for the whole test run.
/// </summary>
/// <remarks>
/// Card loaders log their failures, and several tests provoke exactly those
/// failures on purpose — so without this the suite would append noise to the
/// developer's real log at <c>%LOCALAPPDATA%</c>, in the same file a user would
/// be asked to send in for support.
/// </remarks>
internal static class TestLogRedirect
{
    [ModuleInitializer]
    internal static void Redirect() =>
        ClientLog.UseFile(Path.Combine(
            Path.GetTempPath(),
            $"lanai-test-log-{Guid.NewGuid():N}",
            "client.log"));
}
