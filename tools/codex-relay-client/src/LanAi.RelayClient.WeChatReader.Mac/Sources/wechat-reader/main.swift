import AppKit
import CoreGraphics
import Foundation

// wechat-reader (macOS) — reads the open WeChat conversation off the screen for the client.
//
// Protocol (docs/WECHAT_INTENT_ASSISTANT.md §4.2): one JSON command per line on stdin, one JSON
// event per line on stdout. stderr carries diagnostics only and never recognised text. The
// process ends when stdin closes, so it cannot outlive the client.
//
// `wechat-reader --probe` captures once and prints the layout and each line's geometry and
// character count — never the text.

// NSScreen needs the shared application to exist; it is never run.
_ = NSApplication.shared

guard #available(macOS 14.0, *) else {
    Output.emit(ReaderEvent(type: ReaderEvent.error, code: ReaderEvent.errorOsUnsupported, message: "需要 macOS 14 或更高版本"))
    exit(2)
}

guard TextRecognizer.supportsChinese() else {
    Output.emit(ReaderEvent(type: ReaderEvent.error, code: ReaderEvent.errorOcrLanguageMissing, message: "这台 Mac 不支持简体中文文字识别"))
    exit(3)
}

// Screen Recording. The first request shows macOS's own prompt, attributed to the client app
// that launched this process; after the user allows it, macOS usually wants the app restarted.
if !CGPreflightScreenCaptureAccess() {
    _ = CGRequestScreenCaptureAccess()
    Output.emit(ReaderEvent(type: ReaderEvent.error, code: ReaderEvent.errorScreenRecordingDenied,
                            message: "需要「屏幕录制」权限"))
}

if CommandLine.arguments.contains("--probe") {
    exit(await Probe.run())
}

let reader = Reader()
let input = Thread {
    let decoder = JSONDecoder()
    while let line = readLine(strippingNewline: true) {
        if let data = line.data(using: .utf8), let command = try? decoder.decode(ReaderCommand.self, from: data) {
            reader.accept(command)
        }
    }
    // stdin closed: the client is gone.
    reader.stop()
}
input.start()

Output.emit(ReaderEvent(type: ReaderEvent.ready))
await reader.run()
exit(0)
