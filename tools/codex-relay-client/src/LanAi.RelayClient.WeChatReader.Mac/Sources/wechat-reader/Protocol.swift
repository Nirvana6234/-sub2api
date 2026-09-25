import Foundation

// The wire format of Core/WeChatIntent/ReaderProtocol.cs, field for field. That file is the
// authority: change it first, then this one.

struct ReaderCommand: Decodable {
    let cmd: String
    let intervalMs: Int?

    enum CodingKeys: String, CodingKey {
        case cmd
        case intervalMs = "interval_ms"
    }

    static let start = "start"
    static let snapshot = "snapshot"
    static let stop = "stop"
}

struct ReaderRect: Encodable, Equatable {
    var x: Int
    var y: Int
    var w: Int
    var h: Int
}

struct ReaderLine: Encodable {
    var text: String
    var x: Int
    var y: Int
    var w: Int
    var h: Int
    var bg: [Int]
}

/// One event. Optional fields are left out of the JSON when nil (synthesised Encodable uses
/// encodeIfPresent for optionals), matching WhenWritingNull on the client.
struct ReaderEvent: Encodable {
    var type: String
    var state: String? = nil
    var rect: ReaderRect? = nil
    var scale: Double? = nil
    var imageScale: Double? = nil
    var frontPid: Int? = nil
    var seq: Int64? = nil
    var hash: String? = nil
    var size: ReaderRect? = nil
    var area: ReaderRect? = nil
    var bg: [Int]? = nil
    var title: String? = nil
    var lines: [ReaderLine]? = nil
    var code: String? = nil
    var message: String? = nil

    enum CodingKeys: String, CodingKey {
        case type, state, rect, scale
        case imageScale = "image_scale"
        case frontPid = "front_pid"
        case seq, hash, size, area, bg, title, lines, code, message
    }

    static let ready = "ready"
    static let window = "window"
    static let scrolling = "scrolling"
    static let frame = "frame"
    static let alive = "alive"
    static let error = "error"

    static let stateForeground = "foreground"
    static let stateBackground = "background"
    static let stateMinimized = "minimized"
    static let stateGone = "gone"

    static let errorCaptureFailed = "capture_failed"
    static let errorOcrLanguageMissing = "ocr_language_missing"
    static let errorOsUnsupported = "os_unsupported"
    static let errorNoChatArea = "no_chat_area"
    static let errorScreenRecordingDenied = "screen_recording_denied"
}

/// stdout, one JSON object per line. Serialised: the stdin thread and the loop both write.
enum Output {
    private static let lock = NSLock()
    private static let encoder = JSONEncoder()

    static func emit(_ event: ReaderEvent) {
        lock.lock()
        defer { lock.unlock() }
        guard var data = try? encoder.encode(event) else { return }
        data.append(0x0A)
        FileHandle.standardOutput.write(data)
    }

    /// Diagnostics only. Never recognised text.
    static func diagnose(_ message: String) {
        FileHandle.standardError.write(Data((message + "\n").utf8))
    }
}
