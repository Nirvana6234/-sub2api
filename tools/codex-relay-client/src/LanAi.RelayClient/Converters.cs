using System.Globalization;
using System.Windows.Data;

namespace LanAi.RelayClient;

/// <summary>Inverts a boolean, for binding "enabled" to a "busy" flag.</summary>
public sealed class InverseBooleanConverter : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, CultureInfo culture) =>
        value is bool flag && !flag;

    public object ConvertBack(object value, Type targetType, object parameter, CultureInfo culture) =>
        value is bool flag && !flag;
}
