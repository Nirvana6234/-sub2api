using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class PaymentWindowTests
{
    [Fact]
    public void PaymentWindowExposesTheNativeRechargeSurface()
    {
        string markup = AppSource.Read("Views", "PaymentWindow.axaml");
        string code = AppSource.Read("Views", "PaymentWindow.axaml.cs");

        Assert.Contains("internal PaymentWindow(PaymentViewModel", code, StringComparison.Ordinal);
        Assert.Contains("Name=\"QrCodeImage\"", markup, StringComparison.Ordinal);

        // The amount sits beside the QR code because the form above it collapses
        // once an order is live; lose this element and the scan screen stops
        // saying what is being paid.
        Assert.Contains("Name=\"OrderPayAmountText\"", markup, StringComparison.Ordinal);
        Assert.Contains("Name=\"CancelOrderButton\"", markup, StringComparison.Ordinal);
        Assert.Contains("Name=\"PaymentActionsPanel\"", markup, StringComparison.Ordinal);
        Assert.Contains("Click=\"PaymentAction_OnClick\"", markup, StringComparison.Ordinal);
        Assert.DoesNotContain("PaymentMethodsList", markup, StringComparison.Ordinal);
    }
}
