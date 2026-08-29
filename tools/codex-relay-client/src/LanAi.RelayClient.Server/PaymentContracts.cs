using System.Text.Json.Serialization;

namespace LanAi.RelayClient.Server;

public sealed record PaymentCheckoutInfo
{
    [JsonConstructor]
    public PaymentCheckoutInfo(
        IReadOnlyDictionary<string, PaymentMethodLimit>? methods = null,
        decimal globalMin = default,
        decimal globalMax = default,
        bool balanceDisabled = default,
        decimal balanceRechargeMultiplier = default,
        decimal rechargeFeeRate = default,
        string? helpText = null,
        string? helpImageUrl = null)
    {
        Methods = methods ?? new Dictionary<string, PaymentMethodLimit>(StringComparer.OrdinalIgnoreCase);
        GlobalMin = globalMin;
        GlobalMax = globalMax;
        BalanceDisabled = balanceDisabled;
        BalanceRechargeMultiplier = balanceRechargeMultiplier;
        RechargeFeeRate = rechargeFeeRate;
        HelpText = helpText ?? string.Empty;
        HelpImageUrl = helpImageUrl ?? string.Empty;
    }

    [JsonPropertyName("methods")]
    public IReadOnlyDictionary<string, PaymentMethodLimit> Methods { get; init; } =
        new Dictionary<string, PaymentMethodLimit>(StringComparer.OrdinalIgnoreCase);

    [JsonPropertyName("global_min")]
    public decimal GlobalMin { get; init; }

    [JsonPropertyName("global_max")]
    public decimal GlobalMax { get; init; }

    [JsonPropertyName("balance_disabled")]
    public bool BalanceDisabled { get; init; }

    [JsonPropertyName("balance_recharge_multiplier")]
    public decimal BalanceRechargeMultiplier { get; init; }

    [JsonPropertyName("recharge_fee_rate")]
    public decimal RechargeFeeRate { get; init; }

    [JsonPropertyName("help_text")]
    public string HelpText { get; init; } = string.Empty;

    [JsonPropertyName("help_image_url")]
    public string HelpImageUrl { get; init; } = string.Empty;
}

public sealed record PaymentMethodLimit
{
    [JsonConstructor]
    public PaymentMethodLimit(
        string? currency = null,
        string? displayName = null,
        decimal singleMin = default,
        decimal singleMax = default,
        decimal feeRate = default,
        bool available = true)
    {
        Currency = currency ?? string.Empty;
        DisplayName = displayName ?? string.Empty;
        SingleMin = singleMin;
        SingleMax = singleMax;
        FeeRate = feeRate;
        Available = available;
    }

    [JsonPropertyName("currency")]
    public string Currency { get; init; } = string.Empty;

    [JsonPropertyName("display_name")]
    public string DisplayName { get; init; } = string.Empty;

    [JsonPropertyName("single_min")]
    public decimal SingleMin { get; init; }

    [JsonPropertyName("single_max")]
    public decimal SingleMax { get; init; }

    [JsonPropertyName("fee_rate")]
    public decimal FeeRate { get; init; }

    [JsonPropertyName("available")]
    public bool Available { get; init; } = true;
}

public sealed record PaymentOrderCreateResult
{
    [JsonConstructor]
    public PaymentOrderCreateResult(
        long orderId = default,
        decimal amount = default,
        decimal payAmount = default,
        decimal feeRate = default,
        string? currency = null,
        string? paymentType = null,
        string? qrCode = default,
        string? payUrl = default,
        string? outTradeNo = null,
        DateTimeOffset expiresAt = default)
    {
        OrderId = orderId;
        Amount = amount;
        PayAmount = payAmount;
        FeeRate = feeRate;
        Currency = currency ?? string.Empty;
        PaymentType = paymentType ?? string.Empty;
        QrCode = qrCode;
        PayUrl = payUrl;
        OutTradeNo = outTradeNo ?? string.Empty;
        ExpiresAt = expiresAt;
    }

    [JsonPropertyName("order_id")]
    public long OrderId { get; init; }

    [JsonPropertyName("amount")]
    public decimal Amount { get; init; }

    [JsonPropertyName("pay_amount")]
    public decimal PayAmount { get; init; }

    [JsonPropertyName("fee_rate")]
    public decimal FeeRate { get; init; }

    [JsonPropertyName("currency")]
    public string Currency { get; init; } = string.Empty;

    [JsonPropertyName("payment_type")]
    public string PaymentType { get; init; } = string.Empty;

    [JsonPropertyName("qr_code")]
    public string? QrCode { get; init; }

    [JsonPropertyName("pay_url")]
    public string? PayUrl { get; init; }

    [JsonPropertyName("out_trade_no")]
    public string OutTradeNo { get; init; } = string.Empty;

    [JsonPropertyName("expires_at")]
    public DateTimeOffset ExpiresAt { get; init; }
}

public enum PaymentOrderStatus
{
    Pending,
    Paid,
    Recharging,
    Completed,
    Expired,
    Cancelled,
    Failed,
    RefundRequested,
    Refunding,
    RefundPending,
    PartiallyRefunded,
    Refunded,
    RefundFailed,
}

public sealed record PaymentOrder
{
    [JsonConstructor]
    public PaymentOrder(
        long id = default,
        decimal amount = default,
        decimal payAmount = default,
        decimal feeRate = default,
        string? currency = null,
        string? paymentType = null,
        string? outTradeNo = null,
        PaymentOrderStatus status = default,
        string? orderType = null,
        string? qrCode = default,
        DateTimeOffset expiresAt = default,
        DateTimeOffset? paidAt = default,
        DateTimeOffset? completedAt = default)
    {
        Id = id;
        Amount = amount;
        PayAmount = payAmount;
        FeeRate = feeRate;
        Currency = currency ?? string.Empty;
        PaymentType = paymentType ?? string.Empty;
        OutTradeNo = outTradeNo ?? string.Empty;
        Status = status;
        OrderType = orderType ?? string.Empty;
        QrCode = qrCode;
        ExpiresAt = expiresAt;
        PaidAt = paidAt;
        CompletedAt = completedAt;
    }

    [JsonPropertyName("id")]
    public long Id { get; init; }

    [JsonPropertyName("amount")]
    public decimal Amount { get; init; }

    [JsonPropertyName("pay_amount")]
    public decimal PayAmount { get; init; }

    [JsonPropertyName("fee_rate")]
    public decimal FeeRate { get; init; }

    [JsonPropertyName("currency")]
    public string Currency { get; init; } = string.Empty;

    [JsonPropertyName("payment_type")]
    public string PaymentType { get; init; } = string.Empty;

    [JsonPropertyName("out_trade_no")]
    public string OutTradeNo { get; init; } = string.Empty;

    // The generic converter, not the open one: JsonStringEnumConverter resolves the
    // enum by reflection, which the source generator rejects outright (SYSLIB1034)
    // because a trimmed build has no reflection data to resolve it with. The status
    // would come back as the default — "pending" — for a paid order.
    [JsonPropertyName("status")]
    [JsonConverter(typeof(JsonStringEnumConverter<PaymentOrderStatus>))]
    public PaymentOrderStatus Status { get; init; }

    [JsonPropertyName("order_type")]
    public string OrderType { get; init; } = string.Empty;

    [JsonPropertyName("qr_code")]
    public string? QrCode { get; init; }

    [JsonPropertyName("expires_at")]
    public DateTimeOffset ExpiresAt { get; init; }

    [JsonPropertyName("paid_at")]
    public DateTimeOffset? PaidAt { get; init; }

    [JsonPropertyName("completed_at")]
    public DateTimeOffset? CompletedAt { get; init; }
}
