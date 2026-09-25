// swift-tools-version:5.9
//
// wechat-reader for macOS: the counterpart of LanAi.RelayClient.WeChatReader (Windows), speaking
// the same line-delimited JSON protocol (Core/WeChatIntent/ReaderProtocol.cs). ScreenCaptureKit
// takes the window, Vision reads it. See docs/WECHAT_INTENT_ASSISTANT.md §4.4.
//
// Build on a Mac (the release runner is Windows and cannot build Swift):
//   ./build.sh            → .build/apple/Products/Release/wechat-reader (arm64 + x86_64)
// then package the client with -p:IncludeWeChatReader=true for an osx RID; the App project picks
// the binary up from there (or from -p:WeChatReaderMacBinary=<path>).

import PackageDescription

let package = Package(
    name: "wechat-reader",
    // SCScreenshotManager, SCContentFilter.pointPixelScale and .contentRect are macOS 14.
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "wechat-reader",
            path: "Sources/wechat-reader",
            linkerSettings: [
                .linkedFramework("AppKit"),
                .linkedFramework("ScreenCaptureKit"),
                .linkedFramework("Vision"),
                .linkedFramework("CoreGraphics"),
            ]
        ),
    ]
)
