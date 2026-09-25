import CoreGraphics
import Foundation
import Vision

/// Vision's text recogniser, Simplified Chinese first. On the device; nothing is sent anywhere.
enum TextRecognizer {
    /// Whether this macOS offers Simplified Chinese recognition (macOS 13+ at the accurate level).
    static func supportsChinese() -> Bool {
        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .accurate
        let languages = (try? request.supportedRecognitionLanguages()) ?? []
        return languages.contains("zh-Hans")
    }

    /// Recognises the region and returns its lines in the image's coordinates, top to bottom.
    static func read(_ p: Pixels, x: Int, y: Int, w: Int, h: Int, scale: Double) throws -> [ReaderLine] {
        let region = CGRect(x: max(0, x), y: max(0, y),
                            width: min(w, p.width - max(0, x)), height: min(h, p.height - max(0, y)))
        guard region.width > 0, region.height > 0, let crop = p.image.cropping(to: region) else { return [] }

        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .accurate
        request.recognitionLanguages = ["zh-Hans", "en-US"]
        request.usesLanguageCorrection = true
        try VNImageRequestHandler(cgImage: crop, options: [:]).perform([request])

        let width = Double(region.width)
        let height = Double(region.height)
        var lines: [ReaderLine] = []
        for observation in request.results ?? [] {
            guard let candidate = observation.topCandidates(1).first else { continue }
            // Vision's boxes are normalised with the origin at the bottom left.
            let box = observation.boundingBox
            let lx = Int(region.minX) + Int(box.minX * width)
            let ly = Int(region.minY) + Int((1 - box.maxY) * height)
            let lw = Int(box.width * width)
            let lh = Int(box.height * height)
            lines.append(ReaderLine(text: candidate.string, x: lx, y: ly, w: lw, h: lh,
                                    bg: ChatLayout.sampleBehind(p, x: lx, y: ly, w: lw, h: lh, scale: scale)))
        }

        return lines.sorted { $0.y != $1.y ? $0.y < $1.y : $0.x < $1.x }
    }
}
