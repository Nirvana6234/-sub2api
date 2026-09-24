using System.Text;
using LanAi.RelayClient.Transport;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>The same rules as the server's <c>openai_encrypted_content_lineage.go</c>.</summary>
public sealed class EncryptedContentRecoveryTests
{
    private static byte[] Json(string text) => Encoding.UTF8.GetBytes(text);

    private static string Text(byte[]? body) => body is null ? "<unchanged>" : Encoding.UTF8.GetString(body);

    [Theory]
    [InlineData(400, """{"error":{"code":"invalid_encrypted_content","message":"x"}}""", true)]
    [InlineData(400, """{"error":{"code":null,"message":"The encrypted content gAAA could not be verified."}}""", true)]
    [InlineData(400, """{"error":{"message":"Encrypted content could not be decrypted or parsed."}}""", true)]
    [InlineData(400, """{"error":{"message":"Unsupported parameter: encrypted"}}""", false)]
    [InlineData(400, """{"error":{"message":"bad input"}}""", false)]
    [InlineData(500, """{"error":{"code":"invalid_encrypted_content"}}""", false)]
    [InlineData(400, null, false)]
    public void OnlyA400AboutEncryptedContentCounts(int status, string? body, bool expected)
    {
        Assert.Equal(expected, EncryptedContentRecovery.IsInvalidEncryptedContent(status, body));
    }

    [Fact]
    public void ReasoningKeepsItsSkeletonAndCompactionGoesWhole()
    {
        byte[] body = Json("""{"input":[{"type":"reasoning","id":"rs_1","summary":[],"content":null,"encrypted_content":"A"},{"type":"compaction","encrypted_content":"B"},{"type":"message","role":"user","content":"中文"}]}""");

        Assert.Equal(
            """{"input":[{"type":"reasoning","id":"rs_1","summary":[]},{"type":"message","role":"user","content":"中文"}]}""",
            Text(EncryptedContentRecovery.StripAll(body)));
    }

    [Fact]
    public void AReasoningItemWithNothingElseLeftIsDropped()
    {
        byte[] body = Json("""{"input":[{"type":"reasoning","encrypted_content":"A"},{"type":"message","role":"user","content":"hi"}]}""");

        Assert.Equal(
            """{"input":[{"type":"message","role":"user","content":"hi"}]}""",
            Text(EncryptedContentRecovery.StripAll(body)));
    }

    [Fact]
    public void NothingEncryptedMeansUnchanged()
    {
        Assert.Null(EncryptedContentRecovery.StripAll(Json("""{"input":[{"type":"message","role":"user","content":"hi"}]}""")));
        Assert.Null(EncryptedContentRecovery.StripAll(Json("not json")));
        Assert.Null(EncryptedContentRecovery.StripAll([]));
    }

    [Fact]
    public void OtherItemTypesAreNeverTouched()
    {
        byte[] body = Json("""{"input":[{"type":"function_call_output","call_id":"c","output":"o","encrypted_content":"A"}]}""");

        Assert.Null(EncryptedContentRecovery.StripAll(body));
        Assert.Empty(EncryptedContentRecovery.CollectDigests(body));
    }

    [Fact]
    public void OnlyKnownDigestsAreStripped()
    {
        byte[] body = Json("""{"input":[{"type":"reasoning","id":"a","encrypted_content":"OLD"},{"type":"reasoning","id":"b","encrypted_content":"NEW"}]}""");
        var rejected = new HashSet<string> { EncryptedContentRecovery.Digest("OLD") };

        Assert.Equal(
            """{"input":[{"type":"reasoning","id":"a"},{"type":"reasoning","id":"b","encrypted_content":"NEW"}]}""",
            Text(EncryptedContentRecovery.StripKnown(body, rejected)));
        Assert.Null(EncryptedContentRecovery.StripKnown(body, new HashSet<string>()));
    }

    [Fact]
    public void DigestsAreCollectedFromTheCoveredTypes()
    {
        byte[] body = Json("""{"input":[{"type":"reasoning","encrypted_content":"A"},{"type":"compaction_summary","encrypted_content":"B"},{"type":"message","content":"x"}]}""");

        Assert.Equal(
            [EncryptedContentRecovery.Digest("A"), EncryptedContentRecovery.Digest("B")],
            EncryptedContentRecovery.CollectDigests(body));
    }

    [Fact]
    public void RefusalsAreRememberedPerAccount()
    {
        var store = new RejectedEncryptedContent();
        store.Remember(7, ["d1"]);

        Assert.Contains("d1", store.For(7));
        Assert.Empty(store.For(8));
    }
}
