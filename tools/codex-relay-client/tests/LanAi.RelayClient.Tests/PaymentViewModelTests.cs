using System.Windows.Media.Imaging;
using System.Reflection;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using LanAi.RelayClient.ViewModels;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class PaymentViewModelTests
{
    [Fact]
    public async Task LoadingCheckoutInfoShowsAvailableMethodsAndLocalPresetsWithinLimits()
    {
        var relay = new FakeRelayClient
        {
            OnCheckoutInfo = () => new PaymentCheckoutInfo
            {
                GlobalMin = 5,
                GlobalMax = 5000,
                Methods = new Dictionary<string, PaymentMethodLimit>
                {
                    ["alipay"] = new() { DisplayName = "支付宝", Available = true, SingleMin = 5, SingleMax = 5000 },
                    ["wxpay"] = new() { DisplayName = "微信", Available = false, SingleMin = 5, SingleMax = 5000 },
                },
                BalanceRechargeMultiplier = 0.14m,
                RechargeFeeRate = 1.5m,
            },
        };
        PaymentViewModel viewModel = Build(relay);

        await viewModel.LoadAsync();

        Assert.Single(viewModel.PaymentMethods);
        Assert.Equal("alipay", viewModel.SelectedPaymentType);
        Assert.Equal(new[] { 10m, 20m, 50m, 100m, 200m, 500m, 1000m, 2000m, 5000m }, viewModel.PresetAmounts.Select(item => item.Amount));
    }

    [Theory]
    [InlineData(null, "￥0.00")]
    [InlineData("  ", "  ")]
    [InlineData("￥123.45", "￥123.45")]
    public void CurrentBalanceTextUsesProvidedValueOrDefault(string? currentBalanceText, string expected)
    {
        PaymentViewModel viewModel = Build(new FakeRelayClient(), currentBalanceText: currentBalanceText);

        Assert.Equal(expected, viewModel.CurrentBalanceText);
    }

    [Fact]
    public async Task CreatingAnOrderUsesBalanceTypeRendersQrAndStartsPolling()
    {
        var relay = new FakeRelayClient
        {
            OnCheckoutInfo = () => Checkout(),
            OnCreateBalanceOrder = (amount, paymentType) => new PaymentOrderCreateResult
            {
                OrderId = 42,
                Amount = amount,
                PayAmount = 50.75m,
                FeeRate = 1.5m,
                PaymentType = paymentType,
                OutTradeNo = "OUT42",
                QrCode = "qr-text",
                ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(10),
            },
            OnGetPaymentOrder = _ => new PaymentOrder
            {
                Id = 42,
                Status = PaymentOrderStatus.Completed,
                OutTradeNo = "OUT42",
                ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(10),
            },
        };
        var renderer = new FakeQRCodeRenderer();
        PaymentViewModel viewModel = Build(relay, renderer);
        await viewModel.LoadAsync();
        viewModel.SetPresetAmount(50);

        await viewModel.CreateOrderAsync();
        await viewModel.PollTask!.WaitAsync(TimeSpan.FromSeconds(5));

        Assert.Equal(50m, relay.LastCreatedAmount);
        Assert.Equal("alipay", relay.LastCreatedPaymentType);
        Assert.Equal("qr-text", renderer.LastText);
        Assert.True(viewModel.IsCompleted);
        Assert.Contains("充值完成", viewModel.StatusMessage, StringComparison.Ordinal);
    }

    [Fact]
    public async Task CancellingAnActiveOrderCallsServerAndStopsPolling()
    {
        var relay = new FakeRelayClient
        {
            OnCheckoutInfo = () => Checkout(),
            OnCreateBalanceOrder = (_, _) => new PaymentOrderCreateResult
            {
                OrderId = 42,
                Amount = 50,
                PayAmount = 50,
                PaymentType = "alipay",
                OutTradeNo = "OUT42",
                QrCode = "qr-text",
                ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(10),
            },
            OnGetPaymentOrder = _ => new PaymentOrder { Id = 42, Status = PaymentOrderStatus.Pending },
        };
        PaymentViewModel viewModel = Build(relay);
        await viewModel.LoadAsync();
        viewModel.SetPresetAmount(50);
        await viewModel.CreateOrderAsync();
        await viewModel.CancelOrderAsync();

        Assert.Equal(42, relay.LastCancelledOrderId);
        Assert.False(viewModel.IsOrderActive);
    }

    [Fact]
    public async Task PreviewShowsRechargeMultiplierAndFee()
    {
        var relay = new FakeRelayClient { OnCheckoutInfo = () => Checkout() };
        PaymentViewModel viewModel = Build(relay);
        await viewModel.LoadAsync();
        viewModel.SetPresetAmount(50);

        Assert.Equal(0.75m, viewModel.FeeAmount);
        Assert.Equal(50.75m, viewModel.TotalPayAmount);
        Assert.Equal(7m, viewModel.CreditedAmount);
    }

    [Fact]
    public async Task SwitchingPaymentMethodRebuildsPresetAmountsForItsLimits()
    {
        var relay = new FakeRelayClient
        {
            OnCheckoutInfo = () => new PaymentCheckoutInfo
            {
                GlobalMin = 1,
                GlobalMax = 500,
                Methods = new Dictionary<string, PaymentMethodLimit>
                {
                    ["alipay"] = new() { DisplayName = "支付宝", Available = true, SingleMin = 1, SingleMax = 500 },
                    ["wxpay"] = new() { DisplayName = "微信", Available = true, SingleMin = 100, SingleMax = 100 },
                },
            },
        };
        PaymentViewModel viewModel = Build(relay);
        await viewModel.LoadAsync();

        viewModel.SelectedPaymentType = "wxpay";

        Assert.Equal(new[] { 100m }, viewModel.PresetAmounts.Select(item => item.Amount));
    }

    [Fact]
    public async Task AvailableMethodsExposeDirectPaymentActionTextForSelectedAmount()
    {
        var relay = new FakeRelayClient
        {
            OnCheckoutInfo = () => new PaymentCheckoutInfo
            {
                GlobalMin = 1,
                GlobalMax = 5000,
                Methods = new Dictionary<string, PaymentMethodLimit>
                {
                    ["alipay"] = new() { DisplayName = "支付宝", Available = true, SingleMin = 1, SingleMax = 5000 },
                    ["wxpay"] = new() { DisplayName = "微信", Available = true, SingleMin = 1, SingleMax = 5000 },
                },
            },
        };
        PaymentViewModel viewModel = Build(relay);
        await viewModel.LoadAsync();
        viewModel.SetPresetAmount(2000);

        PropertyInfo? actionText = typeof(PaymentMethodOption).GetProperty("ActionText");
        PropertyInfo? canPay = typeof(PaymentMethodOption).GetProperty("CanPay");
        Assert.NotNull(actionText);
        Assert.NotNull(canPay);

        Assert.Equal("支付宝支付 ￥2000", actionText!.GetValue(viewModel.PaymentMethods[0]));
        Assert.True((bool)canPay!.GetValue(viewModel.PaymentMethods[0])!);
        Assert.Equal("微信支付 ￥2000", actionText.GetValue(viewModel.PaymentMethods[1]));
        Assert.True((bool)canPay.GetValue(viewModel.PaymentMethods[1])!);
    }

    [Fact]
    public async Task ExplicitPaymentMethodCreatesOrderForTheClickedMethod()
    {
        var relay = new FakeRelayClient
        {
            OnCheckoutInfo = () => new PaymentCheckoutInfo
            {
                GlobalMin = 1,
                GlobalMax = 5000,
                Methods = new Dictionary<string, PaymentMethodLimit>
                {
                    ["alipay"] = new() { Available = true, SingleMin = 1, SingleMax = 5000 },
                    ["wxpay"] = new() { Available = true, SingleMin = 1, SingleMax = 5000 },
                },
            },
            OnCreateBalanceOrder = (amount, paymentType) => new PaymentOrderCreateResult
            {
                OrderId = 84,
                Amount = amount,
                PaymentType = paymentType,
                QrCode = "qr-text",
                ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(10),
            },
            OnGetPaymentOrder = _ => new PaymentOrder
            {
                Id = 84,
                Status = PaymentOrderStatus.Completed,
                ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(10),
            },
        };
        PaymentViewModel viewModel = Build(relay);
        await viewModel.LoadAsync();
        viewModel.SetPresetAmount(2000);

        await viewModel.CreateOrderAsync("wxpay");
        await viewModel.PollTask!.WaitAsync(TimeSpan.FromSeconds(5));

        Assert.Equal(2000m, relay.LastCreatedAmount);
        Assert.Equal("wxpay", relay.LastCreatedPaymentType);
    }

    [Fact]
    public async Task NoAvailablePaymentMethodShowsAnExplanation()
    {
        var relay = new FakeRelayClient
        {
            OnCheckoutInfo = () => new PaymentCheckoutInfo { GlobalMin = 1, GlobalMax = 5000 },
        };
        PaymentViewModel viewModel = Build(relay);

        await viewModel.LoadAsync();

        Assert.False(viewModel.HasPaymentMethods);
        Assert.Equal("服务器未配置可用支付方式。", viewModel.PaymentAvailabilityMessage);
    }

    /// <summary>
    /// The amount beside the QR code comes from the order, not from the form: the
    /// form is collapsed while the order is live, so this is the only place the
    /// user can check what they are about to pay.
    /// </summary>
    [Fact]
    public async Task TheScanScreenShowsTheAmountTheGatewayWillCharge()
    {
        var relay = new FakeRelayClient
        {
            OnCheckoutInfo = () => Checkout(),
            OnCreateBalanceOrder = (amount, paymentType) => new PaymentOrderCreateResult
            {
                OrderId = 42,
                Amount = amount,
                PayAmount = 50.75m,
                PaymentType = paymentType,
                OutTradeNo = "OUT42",
                QrCode = "qr-text",
                ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(10),
            },
            OnGetPaymentOrder = _ => new PaymentOrder { Id = 42, Status = PaymentOrderStatus.Pending },
        };
        PaymentViewModel viewModel = Build(relay);
        await viewModel.LoadAsync();
        viewModel.SetPresetAmount(50);

        await viewModel.CreateOrderAsync();

        Assert.True(viewModel.IsOrderActive);
        Assert.Equal("￥50.75", viewModel.OrderPayAmountText);
    }

    /// <summary>
    /// A charge above the top-up amount needs saying, or the gap reads as an error.
    /// </summary>
    [Fact]
    public async Task AChargeAboveTheTopUpAmountIsBrokenDown()
    {
        var relay = new FakeRelayClient
        {
            OnCheckoutInfo = () => Checkout(),
            OnCreateBalanceOrder = (amount, paymentType) => new PaymentOrderCreateResult
            {
                OrderId = 42,
                Amount = amount,
                PayAmount = 50.75m,
                PaymentType = paymentType,
                QrCode = "qr-text",
                ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(10),
            },
            OnGetPaymentOrder = _ => new PaymentOrder { Id = 42, Status = PaymentOrderStatus.Pending },
        };
        PaymentViewModel viewModel = Build(relay);
        await viewModel.LoadAsync();
        viewModel.SetPresetAmount(50);

        await viewModel.CreateOrderAsync();

        Assert.True(viewModel.HasOrderAmountBreakdown);
        Assert.Equal("充值 ￥50.00 + 手续费 ￥0.75", viewModel.OrderAmountBreakdownText);
    }

    /// <summary>A fee-free order says nothing rather than "+ 手续费 ￥0.00".</summary>
    [Fact]
    public async Task AFeeFreeOrderShowsNoBreakdown()
    {
        var relay = new FakeRelayClient
        {
            OnCheckoutInfo = () => Checkout(),
            OnCreateBalanceOrder = (amount, paymentType) => new PaymentOrderCreateResult
            {
                OrderId = 42,
                Amount = amount,
                PayAmount = amount,
                PaymentType = paymentType,
                QrCode = "qr-text",
                ExpiresAt = DateTimeOffset.UtcNow.AddMinutes(10),
            },
            OnGetPaymentOrder = _ => new PaymentOrder { Id = 42, Status = PaymentOrderStatus.Pending },
        };
        PaymentViewModel viewModel = Build(relay);
        await viewModel.LoadAsync();
        viewModel.SetPresetAmount(50);

        await viewModel.CreateOrderAsync();

        Assert.Equal("￥50.00", viewModel.OrderPayAmountText);
        Assert.False(viewModel.HasOrderAmountBreakdown);
        Assert.Equal(string.Empty, viewModel.OrderAmountBreakdownText);
    }

    /// <summary>
    /// Before any order exists there is no charge to report, and the scan panel is
    /// hidden anyway — an empty string keeps a stale figure off the screen.
    /// </summary>
    [Fact]
    public void WithNoOrderThereIsNoAmountToShow()
    {
        PaymentViewModel viewModel = Build(new FakeRelayClient());

        Assert.Equal(string.Empty, viewModel.OrderPayAmountText);
        Assert.False(viewModel.HasOrderAmountBreakdown);
    }

    private static PaymentCheckoutInfo Checkout() => new()
    {
        GlobalMin = 5,
        GlobalMax = 500,
        Methods = new Dictionary<string, PaymentMethodLimit>
        {
            ["alipay"] = new() { DisplayName = "支付宝", Available = true, SingleMin = 5, SingleMax = 500 },
        },
        BalanceRechargeMultiplier = 0.14m,
        RechargeFeeRate = 1.5m,
    };

    private static PaymentViewModel Build(FakeRelayClient relay, IQRCodeRenderer? renderer = null, string? currentBalanceText = null)
    {
        var session = new RelaySessionManager(relay, new FakeSessionStore(), "https://relay.test/");
        session.SignInAsync("a@b.com", "pw").GetAwaiter().GetResult();
        return new PaymentViewModel(relay, session, renderer ?? new FakeQRCodeRenderer(), currentBalanceText);
    }
}

internal sealed class FakeQRCodeRenderer : IQRCodeRenderer
{
    public string? LastText { get; private set; }

    /// <remarks>
    /// Returns bytes rather than a WPF bitmap now. Worth noting what that bought: the
    /// payment view model no longer references any UI framework, which is what let it
    /// move to the shared project — and these tests stopped needing a WPF image type
    /// to exercise a payment flow.
    /// </remarks>
    public byte[] Render(string text)
    {
        LastText = text;
        return [0x89, 0x50, 0x4E, 0x47];
    }
}
