import AppKit

/// The tackroom mark from docs/site/logo.svg, drawn in its 64-unit space:
/// a crossbar at y=20 and a hook dropping from x=28 into a U that ends at x=44.
enum Mark {
    private static func strokeMark(scale s: CGFloat, dx: CGFloat, dy: CGFloat, bar: NSColor, hook: NSColor) {
        func p(_ x: CGFloat, _ y: CGFloat) -> NSPoint { NSPoint(x: (x + dx) * s, y: (y + dy) * s) }
        let crossbar = NSBezierPath()
        crossbar.move(to: p(14, 20))
        crossbar.line(to: p(50, 20))
        crossbar.lineWidth = 7 * s
        crossbar.lineCapStyle = .round
        bar.setStroke()
        crossbar.stroke()

        let hookPath = NSBezierPath()
        hookPath.move(to: p(28, 20))
        hookPath.line(to: p(28, 40))
        hookPath.appendArc(withCenter: p(36, 40), radius: 8 * s, startAngle: 180, endAngle: 0, clockwise: true)
        hookPath.lineWidth = 7 * s
        hookPath.lineCapStyle = .round
        hook.setStroke()
        hookPath.stroke()
    }

    /// Template image for the menu bar; macOS tints it for light and dark bars.
    static let menuBarImage: NSImage = {
        let size: CGFloat = 18
        let image = NSImage(size: NSSize(width: size, height: size), flipped: true) { _ in
            // Crop the 64-unit box to the strokes (x 10...54, y 12...56).
            strokeMark(scale: size / 44, dx: -10, dy: -12, bar: .black, hook: .black)
            return true
        }
        image.isTemplate = true
        return image
    }()

    /// Full-color icon on the dark rounded tile, as on the website.
    static func appIcon(size: CGFloat) -> NSImage {
        NSImage(size: NSSize(width: size, height: size), flipped: true) { _ in
            let s = size / 64
            let tile = NSBezierPath(roundedRect: NSRect(x: 0, y: 0, width: size, height: size), xRadius: 15 * s, yRadius: 15 * s)
            NSColor(red: 0x1c / 255, green: 0x1c / 255, blue: 0x28 / 255, alpha: 1).setFill()
            tile.fill()
            strokeMark(scale: s, dx: 0, dy: 0,
                       bar: NSColor(red: 0xb4 / 255, green: 0xbe / 255, blue: 0xfe / 255, alpha: 1),
                       hook: NSColor(red: 0xe6 / 255, green: 0xe7 / 255, blue: 0xf2 / 255, alpha: 1))
            return true
        }
    }

    /// Writes an .iconset folder for `iconutil`; bundle.sh calls this through `--write-iconset`.
    static func writeIconset(to directory: URL) throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        for points in [16, 32, 128, 256, 512] {
            for scale in [1, 2] {
                let pixels = points * scale
                let name = scale == 1 ? "icon_\(points)x\(points).png" : "icon_\(points)x\(points)@2x.png"
                guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels, bitsPerSample: 8,
                                                 samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                                                 bytesPerRow: 0, bitsPerPixel: 0) else { continue }
                // A macOS icon tile leaves ~10% margin around the artwork.
                let inset = CGFloat(pixels) * 0.1
                NSGraphicsContext.saveGraphicsState()
                NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
                appIcon(size: CGFloat(pixels) - 2 * inset).draw(in: NSRect(x: inset, y: inset, width: CGFloat(pixels) - 2 * inset, height: CGFloat(pixels) - 2 * inset))
                NSGraphicsContext.restoreGraphicsState()
                try rep.representation(using: .png, properties: [:])?.write(to: directory.appendingPathComponent(name))
            }
        }
    }
}
