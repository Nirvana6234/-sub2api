import CoreGraphics
import Foundation

/// The polling loop, the same as the Windows reader's: window state every 200 ms, a capture
/// whenever one is due, text read only once the picture has settled.
final class Reader {
    private static let tick: UInt64 = 200_000_000
    private static let settleInterval: TimeInterval = 0.25
    /// An animated sticker keeps the picture from ever settling; read it anyway after this.
    private static let settleGiveUp: TimeInterval = 3
    private static let aliveInterval: TimeInterval = 10

    private let window = WeChatWindow()
    private let capture = WindowCapture()
    private let lock = NSLock()

    private var started = false
    private var snapshotRequested = false
    private var interval: TimeInterval = 0.6
    private var quit = false

    private var lastState: String?
    private var lastRect: ReaderRect?
    private var lastScale = 0.0
    private var lastFront: Int?

    private var nextCapture = Date.distantPast
    private var emittedHash: UInt64?
    private var pendingHash: UInt64?
    private var pendingSince = Date.distantPast
    private var lastAlive = Date.distantPast
    private var lastErrorCode: String?
    private var seq: Int64 = 0

    func accept(_ command: ReaderCommand) {
        lock.lock()
        defer { lock.unlock() }
        switch command.cmd {
        case ReaderCommand.start:
            started = true
            if let ms = command.intervalMs {
                interval = min(max(Double(ms) / 1000, 0.25), 60)
            }
            nextCapture = .distantPast
            emittedHash = nil
            pendingHash = nil
        case ReaderCommand.snapshot:
            snapshotRequested = true
        case ReaderCommand.stop:
            started = false
            pendingHash = nil
        default:
            break
        }
    }

    func stop() {
        lock.lock()
        quit = true
        lock.unlock()
    }

    func run() async {
        while true {
            lock.lock()
            let done = quit
            lock.unlock()
            if done { return }

            await step()
            try? await Task.sleep(nanoseconds: Self.tick)
        }
    }

    private func step() async {
        let now = Date()
        if now.timeIntervalSince(lastAlive) >= Self.aliveInterval {
            lastAlive = now
            Output.emit(ReaderEvent(type: ReaderEvent.alive))
        }

        window.refresh()
        let state = window.state()
        let rect = window.frame().map { ReaderRect(x: Int($0.minX), y: Int($0.minY), w: Int($0.width), h: Int($0.height)) }
        let imageScale = window.imageScale()
        let front = window.frontPid().map(Int.init)
        if state != lastState || rect != lastRect || abs(imageScale - lastScale) > 0.001 || front != lastFront {
            lastState = state
            lastRect = rect
            lastScale = imageScale
            lastFront = front
            // scale: screen units (points) per design pixel, 1 on macOS. image_scale: image pixels per point.
            Output.emit(ReaderEvent(type: ReaderEvent.window, state: state, rect: rect, scale: 1.0,
                                    imageScale: imageScale, frontPid: front))
        }

        lock.lock()
        let isStarted = started
        let snapshot = snapshotRequested
        let due = nextCapture
        lock.unlock()

        guard isStarted, state == ReaderEvent.stateForeground, snapshot || now >= due,
              let windowId = window.windowId else {
            if state != ReaderEvent.stateForeground { pendingHash = nil }
            return
        }

        let pixels: Pixels
        do {
            pixels = try await capture.capture(windowId: windowId)
        } catch {
            report(ReaderEvent.errorCaptureFailed, "截图失败：\(type(of: error))")
            setNext(now.addingTimeInterval(interval))
            return
        }

        guard let layout = ChatLayout.detect(pixels, scale: imageScale) else {
            report(ReaderEvent.errorNoChatArea, "没有找到聊天区域")
            setNext(now.addingTimeInterval(interval))
            lock.lock()
            snapshotRequested = false
            lock.unlock()
            return
        }

        lastErrorCode = nil
        let hash = layout.hash(pixels)

        if snapshot {
            lock.lock()
            snapshotRequested = false
            lock.unlock()
            emitFrame(pixels, layout, hash, imageScale)
            setNext(now.addingTimeInterval(interval))
            return
        }

        if hash == emittedHash {
            pendingHash = nil
            setNext(now.addingTimeInterval(interval))
            return
        }

        if hash == pendingHash || (pendingHash != nil && now.timeIntervalSince(pendingSince) >= Self.settleGiveUp) {
            pendingHash = nil
            emitFrame(pixels, layout, hash, imageScale)
            setNext(now.addingTimeInterval(interval))
            return
        }

        if pendingHash == nil {
            pendingSince = now
            if emittedHash != nil {
                Output.emit(ReaderEvent(type: ReaderEvent.scrolling))
            }
        }

        pendingHash = hash
        setNext(now.addingTimeInterval(Self.settleInterval))
    }

    private func setNext(_ date: Date) {
        lock.lock()
        nextCapture = date
        lock.unlock()
    }

    private func emitFrame(_ p: Pixels, _ layout: ChatLayout, _ hash: UInt64, _ scale: Double) {
        let width = layout.right - layout.left
        let title = (try? TextRecognizer.read(p, x: layout.left, y: layout.headerTop, w: width,
                                              h: layout.messagesTop - layout.headerTop, scale: scale)) ?? []
        let lines = (try? TextRecognizer.read(p, x: layout.left, y: layout.messagesTop, w: width,
                                              h: layout.messagesBottom - layout.messagesTop + 1, scale: scale)) ?? []
        emittedHash = hash
        seq += 1
        let bg = layout.background
        Output.emit(ReaderEvent(
            type: ReaderEvent.frame,
            seq: seq,
            hash: String(format: "%016llx", hash),
            size: ReaderRect(x: 0, y: 0, w: p.width, h: p.height),
            area: ReaderRect(x: layout.left, y: layout.messagesTop, w: width, h: layout.messagesBottom - layout.messagesTop + 1),
            bg: [bg.r & ~3, bg.g & ~3, bg.b & ~3],
            title: title.first?.text ?? "",
            lines: lines))
    }

    /// Once per distinct problem, not once per tick.
    private func report(_ code: String, _ message: String) {
        guard code != lastErrorCode else { return }
        lastErrorCode = code
        Output.emit(ReaderEvent(type: ReaderEvent.error, code: code, message: message))
    }
}

/// One capture, geometry only — for tuning ChatLayout on a Mac without reading anyone's chat.
enum Probe {
    static func run() async -> Int32 {
        let window = WeChatWindow()
        window.refresh()
        guard let windowId = window.windowId else {
            print("未找到微信主窗口")
            return 1
        }

        let scale = window.imageScale()
        let started = Date()
        guard let p = try? await WindowCapture().capture(windowId: windowId) else {
            print("截图失败（检查「屏幕录制」权限）")
            return 1
        }
        print("状态 \(window.state())  窗口 \(window.frame().map { "\($0)" } ?? "?")  每点像素 \(scale)  截图 \(p.width)x\(p.height) \(Int(Date().timeIntervalSince(started) * 1000))ms")

        guard let layout = ChatLayout.detect(p, scale: scale) else {
            print("未识别到聊天区域")
            return 1
        }

        print("聊天区 x=\(layout.left)..\(layout.right)  标题 y=\(layout.headerTop)  消息区 y=\(layout.messagesTop)..\(layout.messagesBottom)  底色 \(layout.background)")
        let lines = (try? TextRecognizer.read(p, x: layout.left, y: layout.messagesTop, w: layout.right - layout.left,
                                              h: layout.messagesBottom - layout.messagesTop + 1, scale: scale)) ?? []
        print("消息区 \(lines.count) 行")
        for line in lines {
            print("  y=\(line.y) x=[\(line.x),\(line.x + line.w)] h=\(line.h) 字数=\(line.text.count) 底色=(\(line.bg.map(String.init).joined(separator: ",")))")
        }
        return 0
    }
}
