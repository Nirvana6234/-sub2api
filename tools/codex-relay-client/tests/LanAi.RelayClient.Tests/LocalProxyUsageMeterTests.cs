using System.Text;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>Reading usage out of an official answer as it streams past.</summary>
public sealed class LocalProxyUsageMeterTests
{
    private const string CodexStream =
        "event: response.created\n" +
        "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n" +
        "event: response.output_text.delta\n" +
        "data: {\"type\":\"response.output_text.delta\",\"delta\":\"the word usage appears in text\"}\n\n" +
        "event: response.completed\n" +
        "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":1200,\"input_tokens_details\":{\"cached_tokens\":1000},\"output_tokens\":35}}}\n\n";

    private const string ClaudeStream =
        "event: message_start\r\n" +
        "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":200,\"cache_read_input_tokens\":3000,\"output_tokens\":1}}}\r\n\r\n" +
        "event: content_block_delta\r\n" +
        "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hi\"}}\r\n\r\n" +
        "event: message_delta\r\n" +
        "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":42}}\r\n\r\n";

    private static MeteredUsage? Meter(RelayProtocol protocol, string text, int chunkSize)
    {
        var meter = new LocalProxyUsageMeter(protocol);
        byte[] bytes = Encoding.UTF8.GetBytes(text);
        for (int offset = 0; offset < bytes.Length; offset += chunkSize)
        {
            meter.Observe(bytes.AsMemory(offset, Math.Min(chunkSize, bytes.Length - offset)));
        }
        return meter.Finish();
    }

    [Theory]
    [InlineData(1)]
    [InlineData(7)]
    [InlineData(4096)]
    public void CodexUsageComesFromResponseCompletedWhereverTheChunksAreCut(int chunkSize)
    {
        Assert.Equal(new MeteredUsage(1200, 35, 1000), Meter(RelayProtocol.Responses, CodexStream, chunkSize));
    }

    [Theory]
    [InlineData(1)]
    [InlineData(13)]
    [InlineData(4096)]
    public void ClaudeInputIsAllThreeKindsAndOutputIsTheLastRunningCount(int chunkSize)
    {
        Assert.Equal(new MeteredUsage(3210, 42, 3000), Meter(RelayProtocol.Messages, ClaudeStream, chunkSize));
    }

    [Fact]
    public void AStreamThatWasCutOffBeforeItsUsageReportsNone()
    {
        string cut = CodexStream[..CodexStream.IndexOf("event: response.completed", StringComparison.Ordinal)];

        Assert.Null(Meter(RelayProtocol.Responses, cut, 64));
    }

    [Fact]
    public void ANonStreamedAnswerIsReadFromItsTopLevelUsage()
    {
        const string body = "{\"type\":\"message\",\"usage\":{\"input_tokens\":5,\"output_tokens\":6}}";

        Assert.Equal(new MeteredUsage(5, 6, 0), Meter(RelayProtocol.Messages, body, 8));
    }

    [Fact]
    public void ATokenCountIsNotUsage()
    {
        Assert.Null(Meter(RelayProtocol.Messages, "{\"input_tokens\":1234}", 64));
    }
}
