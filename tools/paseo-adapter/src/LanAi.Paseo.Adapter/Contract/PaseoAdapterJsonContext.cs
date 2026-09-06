using System.Text.Json;
using System.Text.Json.Serialization;

namespace LanAi.Paseo.Adapter;

/// <summary>
/// Source-generated metadata for every payload that crosses the pipe.
/// </summary>
/// <remarks>
/// <para>
/// Wired in from the start, unlike <c>RelayJsonContext</c> — which could not be
/// switched on because its contracts used <c>init</c>-only properties with
/// defaults. Every type here is a positional record instead, so the
/// parameterized-constructor path the generator uses is also the path the tests
/// exercise. There is no second, reflection-based behaviour to drift from.
/// </para>
/// <para>
/// Request frames are written with <see cref="Utf8JsonWriter"/> by hand rather
/// than serialized from a model. A request carries an operation-specific
/// <c>args</c> object, and modelling that generically would mean either
/// polymorphic serialization (not trim-safe) or a generic frame type per
/// operation (noise). Writing four fields by hand is cheaper than both.
/// </para>
/// </remarks>
[JsonSourceGenerationOptions(PropertyNamingPolicy = JsonKnownNamingPolicy.CamelCase)]
[JsonSerializable(typeof(HelloResult))]
[JsonSerializable(typeof(HealthPayload))]
[JsonSerializable(typeof(AgentsListPayload))]
[JsonSerializable(typeof(AgentSummary))]
[JsonSerializable(typeof(ContractErrorPayload))]
[JsonSerializable(typeof(WorkdirsListPayload))]
[JsonSerializable(typeof(AgentCreatePayload))]
[JsonSerializable(typeof(ArchivePayload))]
[JsonSerializable(typeof(SubscriptionPayload))]
[JsonSerializable(typeof(RelayStatusPayload))]
[JsonSerializable(typeof(RelayPairPayload))]
[JsonSerializable(typeof(RelayDisablePayload))]
[JsonSerializable(typeof(TimelineBatchPayload))]
[JsonSerializable(typeof(AttentionEventPayload))]
internal sealed partial class PaseoAdapterJsonContext : JsonSerializerContext
{
}
