import AppKit
import CoreGraphics

/// Finds WeChat's main window and reports where it is and whether it is in front.
///
/// Everything comes from the window server list (CGWindowListCopyWindowInfo), not from
/// NSWorkspace.frontmostApplication: that property is updated by notifications on the main run
/// loop, and this process has none running, so it would go stale. The list is ordered front to
/// back, so the owner of the first normal-layer window on screen is the app in front.
///
/// Positions are in points, top-left origin on the primary display — the same space Avalonia's
/// window positions use on macOS (unverified on a real machine, docs §10.3). The captured image is
/// in pixels; `imageScale` converts.
final class WeChatWindow {
    static let bundleId = "com.tencent.xinWeChat"

    private(set) var windowId: CGWindowID?
    private(set) var pid: pid_t?
    private var info: [String: Any]?

    /// Re-reads the window list. Cheap enough to do on every 200 ms tick.
    func refresh() {
        guard let app = NSRunningApplication.runningApplications(withBundleIdentifier: Self.bundleId).first else {
            windowId = nil
            pid = nil
            info = nil
            return
        }

        pid = app.processIdentifier
        let list = CGWindowListCopyWindowInfo([.optionAll, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
        var best: (id: CGWindowID, area: CGFloat, info: [String: Any])?
        for entry in list {
            guard (entry[kCGWindowOwnerPID as String] as? pid_t) == app.processIdentifier,
                  (entry[kCGWindowLayer as String] as? Int) == 0,
                  let number = entry[kCGWindowNumber as String] as? CGWindowID,
                  let bounds = Self.bounds(of: entry),
                  bounds.width >= 300, bounds.height >= 300 else { continue }
            let area = bounds.width * bounds.height
            if best == nil || area > best!.area {
                best = (number, area, entry)
            }
        }

        windowId = best?.id
        info = best?.info
    }

    func state() -> String {
        guard let info, windowId != nil else { return ReaderEvent.stateGone }
        let onScreen = info[kCGWindowIsOnscreen as String] as? Bool ?? false
        if !onScreen {
            return ReaderEvent.stateMinimized
        }

        return frontPid() == pid ? ReaderEvent.stateForeground : ReaderEvent.stateBackground
    }

    /// The window's frame in points.
    func frame() -> CGRect? {
        guard let info else { return nil }
        return Self.bounds(of: info)
    }

    /// The pid owning the frontmost normal window on screen: WeChat, the client (a card was
    /// clicked), or anything else.
    func frontPid() -> pid_t? {
        let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
        for entry in list where (entry[kCGWindowLayer as String] as? Int) == 0 {
            if let owner = entry[kCGWindowOwnerPID as String] as? pid_t {
                return owner
            }
        }

        return nil
    }

    /// Pixels per point on the screen showing most of the window: 2 on Retina.
    func imageScale() -> Double {
        guard let frame = frame() else { return 1.0 }
        // NSScreen frames are bottom-left origin; the window list's are top-left on the primary.
        let primaryHeight = NSScreen.screens.first?.frame.height ?? 0
        let centre = CGPoint(x: frame.midX, y: primaryHeight - frame.midY)
        let screen = NSScreen.screens.first(where: { $0.frame.contains(centre) }) ?? NSScreen.main
        return Double(screen?.backingScaleFactor ?? 1.0)
    }

    private static func bounds(of entry: [String: Any]) -> CGRect? {
        guard let dictionary = entry[kCGWindowBounds as String] as? NSDictionary else { return nil }
        return CGRect(dictionaryRepresentation: dictionary as CFDictionary)
    }
}
