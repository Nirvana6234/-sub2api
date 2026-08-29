using LanAi.RelayClient.Server;

namespace LanAi.RelayClient.Services;

public readonly record struct PaymentAmountValidation(bool IsValid, decimal Amount, string ErrorMessage)
{
    public static PaymentAmountValidation Invalid(string message) => new(false, 0, message);

    public static PaymentAmountValidation Valid(decimal amount) => new(true, amount, string.Empty);
}

public static class PaymentAmountValidator
{
    public static PaymentAmountValidation Validate(
        decimal? amount,
        PaymentCheckoutInfo checkout,
        string paymentType)
    {
        if (amount is null || amount <= 0)
        {
            return PaymentAmountValidation.Invalid("请输入大于 0 的充值金额。");
        }

        decimal value = decimal.Round(amount.Value, 2, MidpointRounding.AwayFromZero);
        if (checkout.BalanceDisabled)
        {
            return PaymentAmountValidation.Invalid("服务器暂未开启余额充值。");
        }

        if (checkout.GlobalMin > 0 && value < checkout.GlobalMin)
        {
            return PaymentAmountValidation.Invalid($"充值金额不能低于 {checkout.GlobalMin:0.##}。");
        }

        if (checkout.GlobalMax > 0 && value > checkout.GlobalMax)
        {
            return PaymentAmountValidation.Invalid($"充值金额不能高于 {checkout.GlobalMax:0.##}。");
        }

        if (!checkout.Methods.TryGetValue(paymentType, out PaymentMethodLimit? method) || !method.Available)
        {
            return PaymentAmountValidation.Invalid("当前支付方式不可用。");
        }

        if (method.SingleMin > 0 && value < method.SingleMin)
        {
            return PaymentAmountValidation.Invalid($"当前支付方式最低充值 {method.SingleMin:0.##}。");
        }

        if (method.SingleMax > 0 && value > method.SingleMax)
        {
            return PaymentAmountValidation.Invalid($"当前支付方式最高充值 {method.SingleMax:0.##}。");
        }

        return PaymentAmountValidation.Valid(value);
    }
}
