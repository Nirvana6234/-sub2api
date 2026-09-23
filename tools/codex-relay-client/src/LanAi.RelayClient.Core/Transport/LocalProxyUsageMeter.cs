using System.IO;
using System.Text;
using System.Text.Json;

namespace LanAi.RelayClient.Transport;

/// <summary>Tokens one answer used, as the official API reported them.</summary>
/// <param name="InputTokens">All input, cached included.</param>
/// <param name="CachedTokens">The part of the input read from cache.</param>
internal sealed record MeteredUsage(long InputTokens, long OutputTokens, long CachedTokens);

/// <summary>
/// Reads the usage out of an official answer while it streams through, without holding
/// the answer.
/// </summary>
/// <remarks>
/// <para>
/// Server-sent events are cut into lines as the chunks arrive; only a line that mentions
/// <c>usage</c> is parsed, so the per-token deltas cost a byte search and nothing more.
/// </para>
/// <para>
/// <c>response.completed</c> carries <c>response.usage</c> (<c>input_tokens</c> includes
/// <c>input_tokens_details.cached_tokens</c>). A non-streamed JSON answer (compact) is read
/// whole, up to a cap, from its top-level <c>usage</c>.
/// </para>
/// </remarks>
internal sealed class LocalProxyUsageMeter
{
    private const int MaxLineBytes = 16 * 1024 * 1024;
    private const int MaxJsonBodyBytes = 4 * 1024 * 1024;
    private static readonly byte[] UsageMarker = "\"usage\""u8.ToArray();

    private readonly MemoryStream _line = new();
    private bool _lineOverflowed;
    private bool _sawEvents;
    private bool _bodyOverflowed;
    private readonly MemoryStream _body = new();

    private long _input;
    private long _output;
    private long _cached;
    private bool _found;

    internal void Observe(ReadOnlyMemory<byte> chunk)
    {
        ReadOnlySpan<byte> span = chunk.Span;

        if (!_bodyOverflowed)
        {
            if (_body.Length + span.Length > MaxJsonBodyBytes)
            {
                _bodyOverflowed = true;
                _body.SetLength(0);
            }
            else
            {
                _body.Write(span);
            }
        }

        while (!span.IsEmpty)
        {
            int newline = span.IndexOf((byte)'\n');
            ReadOnlySpan<byte> piece = newline < 0 ? span : span[..newline];
            if (!_lineOverflowed)
            {
                if (_line.Length + piece.Length > MaxLineBytes)
                {
                    _lineOverflowed = true;
                    _line.SetLength(0);
                }
                else
                {
                    _line.Write(piece);
                }
            }

            if (newline < 0)
            {
                break;
            }

            if (!_lineOverflowed)
            {
                ProcessLine(_line.GetBuffer().AsSpan(0, (int)_line.Length));
            }
            _line.SetLength(0);
            _lineOverflowed = false;
            span = span[(newline + 1)..];
        }
    }

    /// <summary>The usage found, or null when the answer reported none.</summary>
    internal MeteredUsage? Finish()
    {
        if (!_lineOverflowed && _line.Length > 0)
        {
            ProcessLine(_line.GetBuffer().AsSpan(0, (int)_line.Length));
        }

        if (!_sawEvents && !_bodyOverflowed && _body.Length > 0)
        {
            ReadJsonBody(_body.GetBuffer().AsMemory(0, (int)_body.Length));
        }

        return _found ? new MeteredUsage(_input, _output, _cached) : null;
    }

    private void ProcessLine(ReadOnlySpan<byte> line)
    {
        line = line.TrimEnd((byte)'\r');
        if (!line.StartsWith("data:"u8))
        {
            if (line.StartsWith("event:"u8))
            {
                _sawEvents = true;
            }
            return;
        }

        _sawEvents = true;
        ReadOnlySpan<byte> data = line["data:".Length..].TrimStart((byte)' ');
        if (data.IndexOf(UsageMarker) < 0)
        {
            return;
        }

        try
        {
            using JsonDocument document = JsonDocument.Parse(data.ToArray());
            JsonElement root = document.RootElement;
            string type = root.TryGetProperty("type", out JsonElement t) && t.ValueKind == JsonValueKind.String
                ? t.GetString() ?? string.Empty
                : string.Empty;

            if (type == "response.completed" &&
                root.TryGetProperty("response", out JsonElement response) &&
                response.TryGetProperty("usage", out JsonElement usage))
            {
                ReadOpenAIUsage(usage);
            }
        }
        catch (JsonException)
        {
            // A malformed event is the tool's problem to report, not a reason to stop metering.
        }
    }

    private void ReadJsonBody(ReadOnlyMemory<byte> body)
    {
        try
        {
            using JsonDocument document = JsonDocument.Parse(body);
            if (document.RootElement.ValueKind != JsonValueKind.Object ||
                !document.RootElement.TryGetProperty("usage", out JsonElement usage))
            {
                return;
            }

            ReadOpenAIUsage(usage);
        }
        catch (JsonException)
        {
        }
    }

    private void ReadOpenAIUsage(JsonElement usage)
    {
        _input = Number(usage, "input_tokens");
        _output = Number(usage, "output_tokens");
        _cached = usage.TryGetProperty("input_tokens_details", out JsonElement details)
            ? Number(details, "cached_tokens")
            : 0;
        _found = true;
    }

    private static long Number(JsonElement element, string name) =>
        element.TryGetProperty(name, out JsonElement value) && value.ValueKind == JsonValueKind.Number &&
        value.TryGetInt64(out long number) && number > 0
            ? number
            : 0;
}
