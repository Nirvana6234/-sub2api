using System.Runtime.InteropServices;
using System.Runtime.InteropServices.WindowsRuntime;
using Windows.Graphics.Capture;
using Windows.Graphics.DirectX;
using Windows.Graphics.DirectX.Direct3D11;
using Windows.Graphics.Imaging;
using WinRT;

namespace LanAi.RelayClient.WeChatReader;

/// <summary>A captured window: 32-bit BGRA, top-down, <c>Width * 4</c> bytes per row.</summary>
internal sealed class Pixels(int width, int height, byte[] bgra)
{
    public int Width { get; } = width;

    public int Height { get; } = height;

    public byte[] Bgra { get; } = bgra;

    public (int R, int G, int B) At(int x, int y)
    {
        x = Math.Clamp(x, 0, Width - 1);
        y = Math.Clamp(y, 0, Height - 1);
        int i = ((y * Width) + x) * 4;
        return (Bgra[i + 2], Bgra[i + 1], Bgra[i]);
    }
}

/// <summary>Takes one frame of a window with Windows.Graphics.Capture.</summary>
/// <remarks>
/// WGC rather than BitBlt/PrintWindow: WeChat 4.x renders part of its window through a
/// hardware-accelerated child (<c>MMUIRenderSubWindowHW</c>), which the GDI paths can return
/// as black. WGC also works while the window is partly covered. Measured on the development
/// machine at about 25 ms per frame (docs §10.1).
///
/// A capture session per frame rather than a standing one: a standing session keeps the
/// compositor producing frames at display rate, while this reader needs one every second or
/// so, and the per-frame setup is cheap.
/// </remarks>
internal sealed class WindowCapture : IDisposable
{
    private readonly IDirect3DDevice _device = CreateDevice();

    public async Task<Pixels> CaptureAsync(IntPtr hwnd, TimeSpan timeout)
    {
        GraphicsCaptureItem item = CreateItemForWindow(hwnd);
        using Direct3D11CaptureFramePool pool = Direct3D11CaptureFramePool.CreateFreeThreaded(
            _device, DirectXPixelFormat.B8G8R8A8UIntNormalized, 1, item.Size);
        var arrived = new TaskCompletionSource<SoftwareBitmap>(TaskCreationOptions.RunContinuationsAsynchronously);
        pool.FrameArrived += async (sender, _) =>
        {
            try
            {
                using Direct3D11CaptureFrame? frame = sender.TryGetNextFrame();
                if (frame is null || arrived.Task.IsCompleted)
                {
                    return;
                }

                arrived.TrySetResult(await SoftwareBitmap.CreateCopyFromSurfaceAsync(frame.Surface, BitmapAlphaMode.Premultiplied));
            }
            catch (Exception ex)
            {
                arrived.TrySetException(ex);
            }
        };

        using GraphicsCaptureSession session = pool.CreateCaptureSession(item);
        session.IsCursorCaptureEnabled = false;
        TryRemoveBorder(session);
        session.StartCapture();

        using SoftwareBitmap bitmap = await arrived.Task.WaitAsync(timeout).ConfigureAwait(false);
        var bytes = new byte[bitmap.PixelWidth * bitmap.PixelHeight * 4];
        bitmap.CopyToBuffer(bytes.AsBuffer());
        return new Pixels(bitmap.PixelWidth, bitmap.PixelHeight, bytes);
    }

    /// <summary>The yellow capture border. Removable on Windows 11; on 10 the call throws and the border stays.</summary>
    private static void TryRemoveBorder(GraphicsCaptureSession session)
    {
        if (!OperatingSystem.IsWindowsVersionAtLeast(10, 0, 20348))
        {
            return;
        }

        try
        {
            session.IsBorderRequired = false;
        }
        catch (Exception ex) when (ex is InvalidCastException or COMException or NotSupportedException or MissingMethodException)
        {
        }
    }

    public void Dispose() => (_device as IDisposable)?.Dispose();

    private static GraphicsCaptureItem CreateItemForWindow(IntPtr hwnd)
    {
        const string ClassName = "Windows.Graphics.Capture.GraphicsCaptureItem";
        Marshal.ThrowExceptionForHR(WindowsCreateString(ClassName, ClassName.Length, out IntPtr hstring));
        try
        {
            Guid interopId = GraphicsCaptureItemInteropGuid;
            Marshal.ThrowExceptionForHR(RoGetActivationFactory(hstring, ref interopId, out IntPtr factory));
            try
            {
                IntPtr raw = CreateForWindow(factory, hwnd, GraphicsCaptureItemGuid);
                try
                {
                    return MarshalInterface<GraphicsCaptureItem>.FromAbi(raw);
                }
                finally
                {
                    Marshal.Release(raw);
                }
            }
            finally
            {
                Marshal.Release(factory);
            }
        }
        finally
        {
            WindowsDeleteString(hstring);
        }
    }

    private static IDirect3DDevice CreateDevice()
    {
        const int DriverTypeHardware = 1;
        const uint BgraSupport = 0x20;
        const uint SdkVersion = 7;
        Marshal.ThrowExceptionForHR(D3D11CreateDevice(IntPtr.Zero, DriverTypeHardware, IntPtr.Zero, BgraSupport,
            IntPtr.Zero, 0, SdkVersion, out IntPtr d3d, out _, out IntPtr context));
        try
        {
            Guid dxgiId = DxgiDeviceGuid;
            Marshal.ThrowExceptionForHR(Marshal.QueryInterface(d3d, ref dxgiId, out IntPtr dxgi));
            try
            {
                Marshal.ThrowExceptionForHR(CreateDirect3D11DeviceFromDXGIDevice(dxgi, out IntPtr inspectable));
                try
                {
                    return MarshalInterface<IDirect3DDevice>.FromAbi(inspectable);
                }
                finally
                {
                    Marshal.Release(inspectable);
                }
            }
            finally
            {
                Marshal.Release(dxgi);
            }
        }
        finally
        {
            if (context != IntPtr.Zero)
            {
                Marshal.Release(context);
            }

            Marshal.Release(d3d);
        }
    }

    private static readonly Guid GraphicsCaptureItemGuid = new("79C3F95B-31F7-4EC2-A464-632EF5D30760");
    private static readonly Guid DxgiDeviceGuid = new("54ec77fa-1377-44e6-8c32-88fd5f44c84c");

    private static readonly Guid GraphicsCaptureItemInteropGuid = new("3628E81B-3CAC-4C60-B7F4-23CE0E0C3356");

    /// <summary>
    /// <c>IGraphicsCaptureItemInterop::CreateForWindow</c>, called through its vtable slot.
    /// </summary>
    /// <remarks>
    /// Not a <c>[ComImport]</c> interface: a trimmed publish removes built-in COM, and even with
    /// it switched back on the trimmed interface fails to load. Slot 3 is the first method after
    /// IUnknown's three; the interface has not changed since Windows 10 1803.
    /// </remarks>
    private static unsafe IntPtr CreateForWindow(IntPtr interop, IntPtr hwnd, Guid iid)
    {
        IntPtr result;
        void** vtable = *(void***)interop;
        var method = (delegate* unmanaged[Stdcall]<IntPtr, IntPtr, Guid*, IntPtr*, int>)vtable[3];
        Marshal.ThrowExceptionForHR(method(interop, hwnd, &iid, &result));
        return result;
    }

    [DllImport("combase.dll")]
    private static extern int RoGetActivationFactory(IntPtr activatableClassId, ref Guid iid, out IntPtr factory);

    [DllImport("combase.dll", CharSet = CharSet.Unicode)]
    private static extern int WindowsCreateString(string source, int length, out IntPtr hstring);

    [DllImport("combase.dll")]
    private static extern int WindowsDeleteString(IntPtr hstring);

    [DllImport("d3d11.dll")]
    private static extern int D3D11CreateDevice(IntPtr adapter, int driverType, IntPtr software, uint flags,
        IntPtr featureLevels, uint featureLevelCount, uint sdkVersion, out IntPtr device, out int featureLevel, out IntPtr context);

    [DllImport("d3d11.dll")]
    private static extern int CreateDirect3D11DeviceFromDXGIDevice(IntPtr dxgiDevice, out IntPtr graphicsDevice);
}
