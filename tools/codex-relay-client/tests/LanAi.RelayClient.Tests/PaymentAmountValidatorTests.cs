using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;
using Xunit;

namespace LanAi.RelayClient.Tests;

public sealed class PaymentAmountValidatorTests
{
    private static PaymentCheckoutInfo Checkout() => new()
    {
        GlobalMin = 5,
        GlobalMax = 500,
        Methods = new Dictionary<string, PaymentMethodLimit>
        {
            ["alipay"] = new() { SingleMin = 10, SingleMax = 300, Available = true },
        },
    };

    [Theory]
    [InlineData(10)]
    [InlineData(300)]
    public void AcceptsAnAmountInsideTheSelectedMethodLimits(decimal amount)
    {
        PaymentAmountValidation result = PaymentAmountValidator.Validate(amount, Checkout(), "alipay");

        Assert.True(result.IsValid);
        Assert.Equal(amount, result.Amount);
        Assert.Empty(result.ErrorMessage);
    }

    [Theory]
    [InlineData(0)]
    [InlineData(9.99)]
    [InlineData(300.01)]
    [InlineData(501)]
    public void RejectsAnAmountOutsideTheConfiguredLimits(decimal amount)
    {
        PaymentAmountValidation result = PaymentAmountValidator.Validate(amount, Checkout(), "alipay");

        Assert.False(result.IsValid);
        Assert.NotEmpty(result.ErrorMessage);
    }

    [Fact]
    public void RejectsAnUnavailablePaymentMethod()
    {
        PaymentCheckoutInfo checkout = Checkout() with
        {
            Methods = new Dictionary<string, PaymentMethodLimit>
            {
                ["alipay"] = new() { SingleMin = 1, SingleMax = 500, Available = false },
            },
        };

        PaymentAmountValidation result = PaymentAmountValidator.Validate(20, checkout, "alipay");

        Assert.False(result.IsValid);
    }
}
