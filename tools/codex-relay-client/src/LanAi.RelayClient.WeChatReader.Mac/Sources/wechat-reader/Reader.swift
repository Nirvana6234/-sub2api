import CoreGraphics
import Foundation
import ScreenCaptureKit

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

    // Diagnostics (stderr → the client's log): counts and states only, never text.
    private static let summaryInterval: TimeInterval = 60
    private var lastSummary = Date()
    private var loggedState: String?
    private var loggedLayoutMissing: String?
    private var failuresInRow = 0
    private var steps = 0, notInFront = 0, captured = 0, captureFailed = 0, noChatArea = 0, unchanged = 0, settling = 0, frames = 0
    private var captureMsTotal = 0, captureMsMax = 0

    func accept(_ command: ReaderCommand) {
        Output.diagnose("收到命令：\(command.cmd)\(command.intervalMs.map { "（间隔 \($0)ms）" } ?? "")")
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

        if now.timeIntervalSince(lastSummary) >= Self.summaryInterval {
            logSummary(now)
        }

        window.refresh()
        let state = window.state()
        let rect = window.frame().map { ReaderRect(x: Int($0.minX), y: Int($0.minY), w: Int($0.width), h: Int($0.height)) }
        let imageScale = window.imageScale()
        let front = window.frontPid().map(Int.init)
        steps += 1
        if state != loggedState {
            loggedState = state
            let where_ = rect.map { "位置 \($0.x),\($0.y) 尺寸 \($0.w)x\($0.h)" } ?? "没有找到窗口"
            Output.diagnose("窗口状态：\(state)，\(where_)，每点像素 \(imageScale)，窗口号 \(window.windowId.map { "\($0)" } ?? "无")，前台 pid \(front.map { "\($0)" } ?? "无")")
        }
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
            if state != ReaderEvent.stateForeground {
                pendingHash = nil
                if isStarted { notInFront += 1 }
            }
            return
        }

        let pixels: Pixels
        let captureStart = Date()
        do {
            pixels = try await capture.capture(windowId: windowId)
        } catch {
            captureFailed += 1
            failuresInRow += 1
            let elapsed = Int(Date().timeIntervalSince(captureStart) * 1000)
            if failuresInRow <= 5 || failuresInRow % 50 == 0 {
                Output.diagnose("截图失败（连续第 \(failuresInRow) 次）：\(type(of: error)) \((error as NSError).domain) \((error as NSError).code)，用时 \(elapsed)ms")
            }
            // The user declined 「屏幕录制」 (or took it back): said as such, so the client can offer
            // the way to System Settings instead of a bare capture failure.
            if let streamError = error as? SCStreamError, streamError.code == .userDeclined {
                report(ReaderEvent.errorScreenRecordingDenied, "需要「屏幕录制」权限")
            } else {
                report(ReaderEvent.errorCaptureFailed, "截图失败：\(type(of: error))")
            }
            setNext(now.addingTimeInterval(interval))
            return
        }

        let captureMs = Int(Date().timeIntervalSince(captureStart) * 1000)
        captured += 1
        captureMsTotal += captureMs
        captureMsMax = max(captureMsMax, captureMs)
        if failuresInRow > 0 {
            Output.diagnose("截图恢复：此前连续失败 \(failuresInRow) 次，这次 \(captureMs)ms")
            failuresInRow = 0
        }

        guard let layout = ChatLayout.detect(pixels, scale: imageScale) else {
            noChatArea += 1
            let problem = "截图 \(pixels.width)x\(pixels.height)，每点像素 \(imageScale)"
            if problem != loggedLayoutMissing {
                loggedLayoutMissing = problem
                Output.diagnose("没有找到聊天区域（\(problem)）")
            }
            report(ReaderEvent.errorNoChatArea, "没有找到聊天区域")
            setNext(now.addingTimeInterval(interval))
            lock.lock()
            snapshotRequested = false
            lock.unlock()
            return
        }

        lastErrorCode = nil
        if loggedLayoutMissing != nil {
            loggedLayoutMissing = nil
            Output.diagnose("聊天区域：x=\(layout.left)..\(layout.right)，标题 y=\(layout.headerTop)，消息区 y=\(layout.messagesTop)..\(layout.messagesBottom)")
        }
        let hash = layout.hash(pixels, scale: imageScale)

        if snapshot {
            lock.lock()
            snapshotRequested = false
            lock.unlock()
            emitFrame(pixels, layout, hash, imageScale)
            setNext(now.addingTimeInterval(interval))
            return
        }

        if hash == emittedHash {
            unchanged += 1
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

        settling += 1
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
        let ocrStart = Date()
        let title = (try? TextRecognizer.read(p, x: layout.left, y: layout.headerTop, w: width,
                                              h: layout.messagesTop - layout.headerTop, scale: scale)) ?? []
        let lines = (try? TextRecognizer.read(p, x: layout.left, y: layout.messagesTop, w: width,
                                              h: layout.messagesBottom - layout.messagesTop + 1, scale: scale)) ?? []
        emittedHash = hash
        seq += 1
        frames += 1
        Output.diagnose("发出画面 #\(seq)：标题 \(title.first?.text.count ?? 0) 字，消息区 \(lines.count) 行，识别 \(Int(Date().timeIntervalSince(ocrStart) * 1000))ms")
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

    /// What the last minute amounted to, like the Windows reader's: a quiet reader can be told from
    /// a stuck or a blind one.
    private func logSummary(_ now: Date) {
        lock.lock()
        let isStarted = started
        lock.unlock()
        let average = captured > 0 ? "（平均 \(captureMsTotal / captured)ms，最长 \(captureMsMax)ms）" : ""
        Output.diagnose("近 \(Int(now.timeIntervalSince(lastSummary))) 秒\(isStarted ? "" : "（未开始）")：轮询 \(steps) 次，微信不在前台 \(notInFront) 次，截图成功 \(captured) 次\(average)、失败 \(captureFailed) 次，没找到聊天区域 \(noChatArea) 次，画面没变 \(unchanged) 次，等画面稳定 \(settling) 次，发出画面 \(frames) 个")
        steps = 0; notInFront = 0; captured = 0; captureFailed = 0; noChatArea = 0; unchanged = 0; settling = 0; frames = 0
        captureMsTotal = 0; captureMsMax = 0
        lastSummary = now
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
