import CoreGraphics
import Foundation
import ScreenCaptureKit

/// A captured window: 32-bit BGRA, top-down, `width * 4` bytes per row, plus the image itself
/// for cropping into Vision.
struct Pixels {
    let width: Int
    let height: Int
    let bgra: [UInt8]
    let image: CGImage

    init?(image: CGImage) {
        let width = image.width
        let height = image.height
        var bytes = [UInt8](repeating: 0, count: width * height * 4)
        let info = CGImageAlphaInfo.premultipliedFirst.rawValue | CGBitmapInfo.byteOrder32Little.rawValue
        let drawn = bytes.withUnsafeMutableBytes { buffer -> Bool in
            guard let context = CGContext(data: buffer.baseAddress, width: width, height: height, bitsPerComponent: 8,
                                          bytesPerRow: width * 4, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: info) else {
                return false
            }
            context.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
            return true
        }
        guard drawn else { return nil }
        self.width = width
        self.height = height
        self.bgra = bytes
        self.image = image
    }

    func at(_ x: Int, _ y: Int) -> (r: Int, g: Int, b: Int) {
        let cx = min(max(x, 0), width - 1)
        let cy = min(max(y, 0), height - 1)
        let i = (cy * width + cx) * 4
        return (Int(bgra[i + 2]), Int(bgra[i + 1]), Int(bgra[i]))
    }
}

enum CaptureError: Error {
    case windowNotShareable
    case conversionFailed
}

/// One frame of a window with ScreenCaptureKit (macOS 14+). Needs 「屏幕录制」 permission, which
/// macOS attributes to the app that launched this process — 共飞-ChatGPT助手.
final class WindowCapture {
    func capture(windowId: CGWindowID) async throws -> Pixels {
        let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false)
        guard let window = content.windows.first(where: { $0.windowID == windowId }) else {
            throw CaptureError.windowNotShareable
        }

        let filter = SCContentFilter(desktopIndependentWindow: window)
        let configuration = SCStreamConfiguration()
        let scale = CGFloat(filter.pointPixelScale)
        configuration.width = Int(filter.contentRect.width * scale)
        configuration.height = Int(filter.contentRect.height * scale)
        configuration.showsCursor = false
        // Without the shadow the image's origin is the window's top-left, which the client relies on.
        configuration.ignoreShadowsSingleWindow = true

        let image = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: configuration)
        guard let pixels = Pixels(image: image) else { throw CaptureError.conversionFailed }
        return pixels
    }
}
