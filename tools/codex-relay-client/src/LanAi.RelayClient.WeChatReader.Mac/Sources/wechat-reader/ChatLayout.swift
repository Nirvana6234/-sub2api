import Foundation

typealias RGB = (r: Int, g: Int, b: Int)

/// Where the parts of the open conversation are, in the captured image's pixels.
///
/// A line-for-line port of the Windows reader's ChatLayout.cs; keep the two in step. The rules
/// were measured on WeChat 4.1.13 for Windows (light theme). WeChat 4.x for Mac draws the same
/// cross-platform interface, but its colours and spacing have not been measured yet (docs §10.3):
/// if `--probe` finds no chat area on a Mac, this is where to look.
struct ChatLayout {
    let left: Int
    let right: Int
    let headerTop: Int
    let messagesTop: Int
    let messagesBottom: Int
    let background: RGB

    /// `scale`: image pixels per design pixel (2 on Retina).
    static func detect(_ p: Pixels, scale: Double) -> ChatLayout? {
        guard p.width >= 300, p.height >= 300 else { return nil }
        let background = dominantColor(p, x0: p.width / 2, x1: p.width - 1, y0: p.height / 4, y1: p.height * 3 / 4)

        // Left edge: on several rows, where the longest run of the background colour starts.
        var starts: [Int] = []
        var ends: [Int] = []
        for f in [0.3, 0.45, 0.6, 0.75, 0.9] {
            let run = longestRun(p, y: Int(Double(p.height) * f), color: background)
            if Double(run.end - run.start) >= Double(p.width) * 0.3 {
                starts.append(run.start)
                ends.append(run.end)
            }
        }
        guard starts.count >= 2 else { return nil }

        let left = starts.min()!
        let right = ends.sorted()[ends.count / 2]
        let pad = Int((12 * scale).rounded())

        var headerTop = -1
        for y in 0..<(p.height / 3) where near(p.at(left + pad, y), background, 3) {
            headerTop = y
            break
        }
        guard headerTop >= 0 else { return nil }

        let inputLine = fullWidthLine(p, x0: left + pad, x1: right - Int(40 * scale), y0: p.height * 2 / 5, y1: p.height - 1, background: background)
        guard inputLine >= 0 else { return nil }

        let headerLine = fullWidthLine(p, x0: left + pad, x1: right - Int(40 * scale),
                                       y0: headerTop + Int(20 * scale), y1: headerTop + Int(90 * scale), background: background)
        let messagesTop = headerLine > 0 ? headerLine + Int(10 * scale) : headerTop + Int(56 * scale)
        guard Double(inputLine - messagesTop) >= 80 * scale else { return nil }

        return ChatLayout(left: left, right: right, headerTop: headerTop, messagesTop: messagesTop,
                          messagesBottom: inputLine - 1, background: background)
    }

    /// FNV-1a over the message list only, every third pixel: the header (typing indicator) and the
    /// input box (caret) are outside it.
    func hash(_ p: Pixels) -> UInt64 {
        var hash: UInt64 = 14_695_981_039_346_656_037
        let prime: UInt64 = 1_099_511_628_211
        var y = messagesTop
        while y <= messagesBottom {
            let row = y * p.width * 4
            var x = left
            while x < right {
                let i = row + x * 4
                hash = (hash ^ UInt64(p.bgra[i])) &* prime
                hash = (hash ^ UInt64(p.bgra[i + 1])) &* prime
                hash = (hash ^ UInt64(p.bgra[i + 2])) &* prime
                x += 3
            }
            y += 3
        }
        return hash
    }

    /// The colour behind a line of text: the padding just outside it, both sides, three heights,
    /// most common value (quantised to 4) wins.
    static func sampleBehind(_ p: Pixels, x: Int, y: Int, w: Int, h: Int, scale: Double) -> [Int] {
        let gap = max(3, Int((5 * scale).rounded()))
        var counts: [Int: Int] = [:]
        var colours: [Int: RGB] = [:]
        for sx in [x - gap, x - gap - 2, x + w + gap, x + w + gap + 2] {
            for fy in [0.2, 0.5, 0.8] {
                let c = p.at(sx, y + Int(Double(h) * fy))
                let q: RGB = (c.r & ~3, c.g & ~3, c.b & ~3)
                let key = (q.r << 16) | (q.g << 8) | q.b
                counts[key, default: 0] += 1
                colours[key] = q
            }
        }
        let best = colours[counts.max(by: { $0.value < $1.value })!.key]!
        return [best.r, best.g, best.b]
    }

    private static func dominantColor(_ p: Pixels, x0: Int, x1: Int, y0: Int, y1: Int) -> RGB {
        var counts: [Int: Int] = [:]
        var y = y0
        while y < y1 {
            var x = x0
            while x < x1 {
                let c = p.at(x, y)
                counts[(c.r << 16) | (c.g << 8) | c.b, default: 0] += 1
                x += 7
            }
            y += 7
        }
        let key = counts.max(by: { $0.value < $1.value })?.key ?? 0xFAFAFA
        return ((key >> 16) & 0xFF, (key >> 8) & 0xFF, key & 0xFF)
    }

    private static func longestRun(_ p: Pixels, y: Int, color: RGB) -> (start: Int, end: Int) {
        var bestStart = 0, bestEnd = -1, start = -1
        for x in 0..<p.width {
            if near(p.at(x, y), color, 3) {
                if start < 0 { start = x }
                if x - start > bestEnd - bestStart {
                    bestStart = start
                    bestEnd = x
                }
            } else {
                start = -1
            }
        }
        return (bestStart, bestEnd)
    }

    /// The first row, top down, where nearly every sample along the row is one and the same
    /// non-background colour. WeChat insets its hairlines, so the ends are not required.
    private static func fullWidthLine(_ p: Pixels, x0: Int, x1: Int, y0: Int, y1: Int, background: RGB) -> Int {
        guard x1 - x0 >= 50, y0 <= y1 else { return -1 }
        let step = max(1, (x1 - x0) / 60)
        for y in y0...y1 {
            var counts: [Int: Int] = [:]
            var total = 0
            var x = x0
            while x <= x1 {
                total += 1
                let c = p.at(x, y)
                counts[((c.r & ~1) << 16) | ((c.g & ~1) << 8) | (c.b & ~1), default: 0] += 1
                x += step
            }
            guard let top = counts.max(by: { $0.value < $1.value }) else { continue }
            let colour: RGB = ((top.key >> 16) & 0xFF, (top.key >> 8) & 0xFF, top.key & 0xFF)
            if Double(top.value) >= Double(total) * 0.85 && !near(colour, background, 2) {
                return y
            }
        }
        return -1
    }

    static func near(_ a: RGB, _ b: RGB, _ tolerance: Int) -> Bool {
        abs(a.r - b.r) <= tolerance && abs(a.g - b.g) <= tolerance && abs(a.b - b.b) <= tolerance
    }
}
