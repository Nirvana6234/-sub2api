using System.Collections.ObjectModel;
using System.Globalization;
using System.Net;
using System.Net.Http;
using CommunityToolkit.Mvvm.ComponentModel;
using LanAi.RelayClient.Server;
using LanAi.RelayClient.Services;

namespace LanAi.RelayClient.ViewModels;

public sealed partial class PaymentMethodOption : ObservableObject
{
    public PaymentMethodOption(string type, string displayName, bool available)
    {
        Type = type;
        DisplayName = displayName;
        Available = available;
    }

    public string Type { get; }

    public string DisplayName { get; }

    public bool Available { get; }

    /// <summary>Whether this method is WeChat Pay, which is shown in its own green.</summary>
    /// <remarks>
    /// A named property rather than a comparison in the view. WPF expressed this as a
    /// <c>DataTrigger</c> on <c>Type == "wxpay"</c>; Avalonia has no data triggers, and
    /// the alternative — a string comparison written into a style selector — would put
    /// a payment-provider identifier in two places. The brand colour is not decoration:
    /// a WeChat-green button that opens Alipay is how a user pays from the wrong wallet.
    /// </remarks>
    public bool IsWeChat => string.Equals(Type, "wxpay", StringComparison.Ordinal);

    [ObservableProperty]
    private string actionText = string.Empty;

    [ObservableProperty]
    private bool canPay;
}

public sealed record PaymentAmountOption(decimal Amount)
{
    public string Label => $"￥{Amount:0.##}";
}

public sealed partial class PaymentViewModel : ObservableObject, IDisposable
{
    private static readonly decimal[] PresetCandidates = [10, 20, 50, 100, 200, 500, 1000, 2000, 5000];

    private readonly IRelayServerClient _client;
    private readonly RelaySessionManager _session;
    private readonly IQRCodeRenderer _qrRenderer;
    private CancellationTokenSource? _orderCancellation;
    private Task? _pollTask;
    private PaymentCheckoutInfo? _checkout;
    private PaymentOrderCreateResult? _createdOrder;
    private bool _completedRaised;
    private decimal? _selectedPresetAmount;

    internal PaymentViewModel(
        IRelayServerClient client,
        RelaySessionManager session,
        IQRCodeRenderer qrRenderer,
        string? currentBalanceText = null)
    {
        _client = client ?? throw new ArgumentNullException(nameof(client));
        _session = session ?? throw new ArgumentNullException(nameof(session));
        _qrRenderer = qrRenderer ?? throw new ArgumentNullException(nameof(qrRenderer));
        CurrentBalanceText = string.IsNullOrEmpty(currentBalanceText) ? "￥0.00" : currentBalanceText;
    }

    public ObservableCollection<PaymentMethodOption> PaymentMethods { get; } = [];

    public ObservableCollection<PaymentAmountOption> PresetAmounts { get; } = [];

    public string CurrentBalanceText { get; }

    public bool HasPaymentMethods => PaymentMethods.Count > 0;

    public string PaymentAvailabilityMessage => HasPaymentMethods
        ? string.Empty
        : "服务器未配置可用支付方式。";

    [ObservableProperty]
    private string selectedPaymentType = string.Empty;

    [ObservableProperty]
    private string customAmountText = string.Empty;

    [ObservableProperty]
    private string statusMessage = string.Empty;

    [ObservableProperty]
    private string errorMessage = string.Empty;

    public bool HasError => !string.IsNullOrWhiteSpace(ErrorMessage);

    [ObservableProperty]
    private byte[]? qrCodePng;

    [ObservableProperty]
    private bool isLoading;

    [ObservableProperty]
    private bool isCreatingOrder;

    [ObservableProperty]
    private bool isOrderActive;

    [ObservableProperty]
    private bool isCompleted;

    [ObservableProperty]
    private int secondsRemaining;

    public Task? PollTask => _pollTask;

    public decimal? SelectedPresetAmount => _selectedPresetAmount;

    public decimal? CurrentAmount
    {
        get
        {
            if (!string.IsNullOrWhiteSpace(CustomAmountText) &&
                decimal.TryParse(CustomAmountText, NumberStyles.Number, CultureInfo.InvariantCulture, out decimal custom))
            {
                return custom;
            }

            return _selectedPresetAmount;
        }
    }

    public decimal FeeAmount
    {
        get
        {
            decimal amount = CurrentAmount ?? 0;
            decimal rate = _checkout?.RechargeFeeRate ?? 0;
            return decimal.Round(amount * rate / 100, 2, MidpointRounding.ToPositiveInfinity);
        }
    }

    public decimal TotalPayAmount => decimal.Round((CurrentAmount ?? 0) + FeeAmount, 2, MidpointRounding.AwayFromZero);

    public decimal CreditedAmount
    {
        get
        {
            decimal amount = CurrentAmount ?? 0;
            decimal multiplier = _checkout?.BalanceRechargeMultiplier ?? 1;
            return decimal.Round(amount * (multiplier > 0 ? multiplier : 1), 2, MidpointRounding.AwayFromZero);
        }
    }

    public bool CanCreateOrder => !IsLoading && !IsCreatingOrder && !IsOrderActive && _checkout is not null &&
        PaymentAmountValidator.Validate(CurrentAmount, _checkout, SelectedPaymentType).IsValid;

    /// <summary>
    /// What the QR code will actually charge.
    /// </summary>
    /// <remarks>
    /// <para>
    /// Read from the created order rather than recomputed from the form. The form
    /// collapses while an order is live, so the number beside the QR code is the
    /// only place the amount appears — and it has to be the amount the payment
    /// gateway will charge, not the client's own arithmetic. Should the two ever
    /// disagree over a rounding rule, showing the guess next to a code that
    /// charges something else is worse than showing nothing.
    /// </para>
    /// <para>
    /// Printed with two decimals, unlike the "0.##" used elsewhere on this page:
    /// money about to leave someone's account reads wrong as "￥12.3".
    /// </para>
    /// </remarks>
    public string OrderPayAmountText => _createdOrder is null
        ? string.Empty
        : $"￥{_createdOrder.PayAmount:0.00}";

    /// <summary>Spells out the fee when the charge exceeds the recharge amount.</summary>
    /// <remarks>
    /// Without this, a user who asked to top up ￥100 and is shown ￥102 has no way
    /// to tell a fee from a mistake.
    /// </remarks>
    public string OrderAmountBreakdownText
    {
        get
        {
            if (_createdOrder is null || _createdOrder.PayAmount <= _createdOrder.Amount)
            {
                return string.Empty;
            }

            decimal fee = _createdOrder.PayAmount - _createdOrder.Amount;
            return $"充值 ￥{_createdOrder.Amount:0.00} + 手续费 ￥{fee:0.00}";
        }
    }

    public bool HasOrderAmountBreakdown => !string.IsNullOrEmpty(OrderAmountBreakdownText);

    public event EventHandler? PaymentCompleted;

    internal PaymentOrderCreateResult? CreatedOrder => _createdOrder;

    public async Task LoadAsync(CancellationToken cancellationToken = default)
    {
        if (IsLoading)
        {
            return;
        }

        IsLoading = true;
        ErrorMessage = string.Empty;
        try
        {
            string accessToken = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);
            _checkout = await _client.GetCheckoutInfoAsync(accessToken, cancellationToken).ConfigureAwait(true);

            PaymentMethods.Clear();
            foreach ((string type, PaymentMethodLimit method) in _checkout.Methods)
            {
                if (method.Available)
                {
                    PaymentMethods.Add(new PaymentMethodOption(
                        type,
                        string.IsNullOrWhiteSpace(method.DisplayName) ? type : method.DisplayName,
                        method.Available));
                }
            }

            SelectedPaymentType = PaymentMethods.FirstOrDefault()?.Type ?? string.Empty;
            RefreshPresetAmounts();
            OnPropertyChanged(nameof(HasPaymentMethods));
            OnPropertyChanged(nameof(PaymentAvailabilityMessage));

            StatusMessage = _checkout.BalanceDisabled ? "服务器暂未开启余额充值。" : string.Empty;
            RaiseAmountProperties();
        }
        catch (Exception ex) when (ex is RelayApiException or HttpRequestException)
        {
            ErrorMessage = ex.Message;
        }
        finally
        {
            IsLoading = false;
            OnPropertyChanged(nameof(CanCreateOrder));
            RefreshPaymentActions();
        }
    }

    public void SetPresetAmount(decimal amount)
    {
        _selectedPresetAmount = amount;
        CustomAmountText = string.Empty;
        RaiseAmountProperties();
    }

    public Task CreateOrderAsync(CancellationToken cancellationToken = default) =>
        CreateOrderAsync(SelectedPaymentType, cancellationToken);

    public async Task CreateOrderAsync(string paymentType, CancellationToken cancellationToken = default)
    {
        if (_checkout is null)
        {
            return;
        }

        PaymentAmountValidation validation = PaymentAmountValidator.Validate(CurrentAmount, _checkout, paymentType);
        if (!validation.IsValid)
        {
            ErrorMessage = validation.ErrorMessage;
            return;
        }

        IsCreatingOrder = true;
        ErrorMessage = string.Empty;
        try
        {
            string accessToken = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);
            _createdOrder = await _client
                .CreateBalanceOrderAsync(accessToken, validation.Amount, paymentType, cancellationToken)
                .ConfigureAwait(true);

            if (string.IsNullOrWhiteSpace(_createdOrder.QrCode))
            {
                ErrorMessage = "服务器没有返回支付二维码。";
                return;
            }

            QrCodePng = _qrRenderer.Render(_createdOrder.QrCode);
            RaiseOrderAmountProperties();
            SecondsRemaining = Math.Max(0, (int)Math.Ceiling((_createdOrder.ExpiresAt - DateTimeOffset.UtcNow).TotalSeconds));
            IsOrderActive = true;
            StatusMessage = "请使用手机扫码支付，支付完成后页面会自动关闭。";
            _completedRaised = false;
            _orderCancellation?.Dispose();
            _orderCancellation = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            _pollTask = PollOrderAsync(accessToken, _createdOrder.OrderId, _orderCancellation.Token);
        }
        catch (Exception ex) when (ex is RelayApiException or HttpRequestException)
        {
            ErrorMessage = ex.Message;
        }
        finally
        {
            IsCreatingOrder = false;
            OnPropertyChanged(nameof(CanCreateOrder));
            RefreshPaymentActions();
        }
    }

    public async Task CancelOrderAsync(CancellationToken cancellationToken = default)
    {
        if (_createdOrder is null)
        {
            return;
        }

        _orderCancellation?.Cancel();
        if (_pollTask is not null)
        {
            await _pollTask.ConfigureAwait(true);
        }

        try
        {
            string accessToken = await _session.GetAccessTokenAsync(cancellationToken).ConfigureAwait(true);
            await _client.CancelPaymentOrderAsync(accessToken, _createdOrder.OrderId, cancellationToken).ConfigureAwait(true);
        }
        catch (Exception ex) when (ex is RelayApiException or HttpRequestException)
        {
            ErrorMessage = ex.Message;
        }

        IsOrderActive = false;
        StatusMessage = "订单已取消。";
        OnPropertyChanged(nameof(CanCreateOrder));
    }

    public void UpdateCountdown(DateTimeOffset now)
    {
        if (_createdOrder is null || !IsOrderActive)
        {
            SecondsRemaining = 0;
            return;
        }

        SecondsRemaining = Math.Max(0, (int)Math.Ceiling((_createdOrder.ExpiresAt - now).TotalSeconds));
    }

    private async Task PollOrderAsync(string accessToken, long orderId, CancellationToken cancellationToken)
    {
        var coordinator = new PaymentPollingCoordinator(
            token => _client.GetPaymentOrderAsync(accessToken, orderId, token));
        PaymentPollingOutcome? outcome = await coordinator.PollAsync(cancellationToken).ConfigureAwait(true);
        if (outcome is null)
        {
            return;
        }

        IsOrderActive = false;
        switch (outcome.Result)
        {
            case PaymentPollingResult.Completed:
                IsCompleted = true;
                StatusMessage = "充值完成，账户余额已更新。";
                if (!_completedRaised)
                {
                    _completedRaised = true;
                    PaymentCompleted?.Invoke(this, EventArgs.Empty);
                }
                break;
            case PaymentPollingResult.Expired:
                StatusMessage = "订单已过期，请重新下单。";
                break;
            case PaymentPollingResult.Cancelled:
                StatusMessage = "订单已取消。";
                break;
            case PaymentPollingResult.Failed:
                StatusMessage = "支付失败，请重新下单。";
                break;
        }

        OnPropertyChanged(nameof(CanCreateOrder));
    }

    private void RaiseOrderAmountProperties()
    {
        OnPropertyChanged(nameof(OrderPayAmountText));
        OnPropertyChanged(nameof(OrderAmountBreakdownText));
        OnPropertyChanged(nameof(HasOrderAmountBreakdown));
    }

    private void RaiseAmountProperties()
    {
        OnPropertyChanged(nameof(CurrentAmount));
        OnPropertyChanged(nameof(FeeAmount));
        OnPropertyChanged(nameof(TotalPayAmount));
        OnPropertyChanged(nameof(CreditedAmount));
        OnPropertyChanged(nameof(CanCreateOrder));
        RefreshPaymentActions();
    }

    partial void OnCustomAmountTextChanged(string value)
    {
        if (!string.IsNullOrWhiteSpace(value))
        {
            _selectedPresetAmount = null;
        }

        RaiseAmountProperties();
    }

    partial void OnSelectedPaymentTypeChanged(string value)
    {
        RefreshPresetAmounts();
        RaiseAmountProperties();
    }

    private void RefreshPresetAmounts()
    {
        PresetAmounts.Clear();
        if (_checkout is null)
        {
            return;
        }

        foreach (decimal candidate in PresetCandidates)
        {
            if (PaymentAmountValidator.Validate(candidate, _checkout, SelectedPaymentType).IsValid)
            {
                PresetAmounts.Add(new PaymentAmountOption(candidate));
            }
        }
    }

    private void RefreshPaymentActions()
    {
        foreach (PaymentMethodOption option in PaymentMethods)
        {
            PaymentAmountValidation validation = _checkout is null
                ? PaymentAmountValidation.Invalid(string.Empty)
                : PaymentAmountValidator.Validate(CurrentAmount, _checkout, option.Type);

            option.CanPay = !IsLoading && !IsCreatingOrder && !IsOrderActive && validation.IsValid;
            option.ActionText = BuildPaymentActionText(option, validation.IsValid ? validation.Amount : null);
        }
    }

    private static string BuildPaymentActionText(PaymentMethodOption option, decimal? amount)
    {
        string name = option.Type switch
        {
            "alipay" => "支付宝",
            "wxpay" => "微信",
            _ => option.DisplayName,
        };
        string action = name.EndsWith("支付", StringComparison.Ordinal) ? name : $"{name}支付";
        return amount is { } value ? $"{action} ￥{value:0.##}" : action;
    }

    partial void OnIsLoadingChanged(bool value) => RefreshPaymentActions();

    partial void OnIsCreatingOrderChanged(bool value) => RefreshPaymentActions();

    partial void OnIsOrderActiveChanged(bool value) => RefreshPaymentActions();

    partial void OnErrorMessageChanged(string value) => OnPropertyChanged(nameof(HasError));

    public void Dispose()
    {
        _orderCancellation?.Cancel();
        _orderCancellation?.Dispose();
    }
}
