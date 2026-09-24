using LanAi.RelayClient.DesktopSync;
using Xunit;

namespace LanAi.RelayClient.Tests.DesktopSync;

/// <summary>
/// The phone (Paw, tools/chat/src/client/remote/protocol.ts) and this assistant must
/// agree byte for byte on three things, or sends are refused and pairing cannot be
/// confirmed — silently. These vectors were produced by the phone's own code running
/// on WebCrypto; the same values are asserted in protocol.test.ts.
/// </summary>
public sealed class PhoneProtocolVectorTests
{
    private const string Thread = "01a0ced9-0000-7000-8000-000000000001";
    private const string Text = "请只回复 OK";
    private const long Ts = 1790200000000;
    private const string Nonce = "bm9uY2Utbm9uY2Utbm9uY2Ut";

    // A throwaway key made by WebCrypto for this vector, and its signature over the
    // canonical string below: raw r‖s, as a browser produces it.
    private const string WebCryptoSpki =
        "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEH384x1mpq4vYLk2qIvRhWy6zBDrlfaUyEOsOgPodP3gK5jAGoAZ3AYKvDzJsx+FHA9fsJ31IToKfkbifFRggTA==";

    private const string WebCryptoSignature =
        "+70buVGxQK9ubqH/KbOam18UE0gCNoC7GU3C1Gu7W13cfZPoFZChGG4MCySCXY0hjGqFBlLy1crdfS0RpocJzw==";

    [Fact]
    public void TheSignedStringMatchesThePhones() =>
        Assert.Equal(
            "cofly-remote/1\nmessage.send\n5\n01a0ced9-0000-7000-8000-000000000001\nqueue\n" +
            "5cf4668abee37f0614db4b4df55676a9f0c0119d21a4cf6f269ce0c0be6f2267\n1790200000000\nbm9uY2Utbm9uY2Utbm9uY2Ut",
            SignedSendVerifier.Canonical(5, Thread, "queue", Text, Ts, Nonce));

    [Fact]
    public void TheFingerprintMatchesThePhones() =>
        Assert.Equal("63C 1DD", DesktopSyncLink.Fingerprint("AAAA"));

    /// <summary>A signature made by a browser's WebCrypto is accepted here.</summary>
    [Fact]
    public void AWebCryptoSignatureVerifies()
    {
        var verifier = new SignedSendVerifier(() => DateTimeOffset.FromUnixTimeMilliseconds(Ts + 1000));
        var phone = new ApprovedPhone(5, "iPhone", WebCryptoSpki, DateTimeOffset.UnixEpoch);

        Assert.Null(verifier.Verify(phone, Thread, "queue", Text, Ts, Nonce, WebCryptoSignature));
    }

    [Fact]
    public void TheSameSignatureFailsForAnyOtherText()
    {
        var verifier = new SignedSendVerifier(() => DateTimeOffset.FromUnixTimeMilliseconds(Ts + 1000));
        var phone = new ApprovedPhone(5, "iPhone", WebCryptoSpki, DateTimeOffset.UnixEpoch);

        Assert.NotNull(verifier.Verify(phone, Thread, "queue", Text + "!", Ts, Nonce, WebCryptoSignature));
    }
}
