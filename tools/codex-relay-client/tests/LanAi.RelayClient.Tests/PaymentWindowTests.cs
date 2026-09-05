using System.Reflection;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class PaymentWindowTests
{
    [Fact]
    public void PaymentWindowExposesTheNativeRechargeSurface()
    {
        Type windowType = typeof(PaymentWindow);

        Assert.NotNull(windowType.GetConstructor(
            BindingFlags.Instance | BindingFlags.NonPublic,
            binder: null,
            [typeof(PaymentViewModel)],
            modifiers: null));
        Assert.NotNull(windowType.GetField("QrCodeImage", BindingFlags.Instance | BindingFlags.NonPublic));

        // The amount sits beside the QR code because the form above it collapses
        // once an order is live; lose this element and the scan screen stops
        // saying what is being paid.
        Assert.NotNull(windowType.GetField("OrderPayAmountText", BindingFlags.Instance | BindingFlags.NonPublic));
        Assert.NotNull(windowType.GetField("CancelOrderButton", BindingFlags.Instance | BindingFlags.NonPublic));
        Assert.NotNull(windowType.GetField("PaymentActionsPanel", BindingFlags.Instance | BindingFlags.NonPublic));
        Assert.NotNull(windowType.GetMethod("PaymentAction_OnClick", BindingFlags.Instance | BindingFlags.NonPublic));
        Assert.Null(windowType.GetField("PaymentMethodsList", BindingFlags.Instance | BindingFlags.NonPublic));
    }
}
