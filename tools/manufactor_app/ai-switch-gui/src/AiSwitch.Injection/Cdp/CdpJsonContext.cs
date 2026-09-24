using System.Text.Json.Serialization;

namespace LanAi.Workspace.Injection.Cdp;

/// <summary>
/// Source-generated metadata for the DevTools discovery endpoints.
/// </summary>
/// <remarks>
/// <para>
/// Added when the Avalonia client first pulled this project into a trimmed publish.
/// Only the two <c>/json/*</c> reads need it; everything sent to the browser is now
/// built as a <c>JsonNode</c> and involves no serializer metadata at all.
/// </para>
/// <para>
/// Both records are positional, so the generator binds through their constructors and
/// an absent field arrives as <c>null</c> — which is what every property here already
/// declares and what the reflection binder produced too. No behaviour changes.
/// </para>
/// <para>
/// No naming policy, deliberately: each property carries an explicit
/// <c>[JsonPropertyName]</c>, and one of them — <c>Protocol-Version</c> — is not a
/// legal identifier in any policy's output. The attributes are the contract.
/// </para>
/// </remarks>
[JsonSerializable(typeof(CdpBrowserInfo))]
[JsonSerializable(typeof(List<CdpTarget>))]
internal sealed partial class CdpJsonContext : JsonSerializerContext;
