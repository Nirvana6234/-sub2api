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

    private static MeteredUsage? Meter(string text, int chunkSize)
    {
        var meter = new LocalProxyUsageMeter();
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
        Assert.Equal(new MeteredUsage(1200, 35, 1000), Meter(CodexStream, chunkSize));
    }

    [Fact]
    public void AStreamThatWasCutOffBeforeItsUsageReportsNone()
    {
        string cut = CodexStream[..CodexStream.IndexOf("event: response.completed", StringComparison.Ordinal)];

        Assert.Null(Meter(cut, 64));
    }

    [Fact]
    public void ANonStreamedAnswerIsReadFromItsTopLevelUsage()
    {
        const string body = "{\"output\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":6,\"input_tokens_details\":{\"cached_tokens\":2}}}";

        Assert.Equal(new MeteredUsage(5, 6, 2), Meter(body, 8));
    }

    [Fact]
    public void ATokenCountIsNotUsage()
    {
        Assert.Null(Meter("{\"input_tokens\":1234}", 64));
    }
}
