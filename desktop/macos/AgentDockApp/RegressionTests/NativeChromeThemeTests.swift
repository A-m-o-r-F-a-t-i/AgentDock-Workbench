import XCTest
import AppKit
@testable import WorkbenchKit

final class NativeChromeThemeTests: XCTestCase {
    @MainActor func testApplicationAppearanceIncludesExistingAndNewNativeWindows() throws {
        _ = NSApplication.shared
        let appearance = WorkbenchAppearance.shared
        let previous = appearance.theme
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 300),
                              styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        defer {
            window.close()
            appearance.setTheme(previous, persist: false)
        }
        for (theme, expected) in [(WorkbenchTheme.dark, NSAppearance.Name.darkAqua), (.light, .aqua)] {
            appearance.setTheme(theme, persist: false)
            XCTAssertEqual(window.effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]), expected)
            XCTAssertTrue(window.styleMask.contains(.titled))
            XCTAssertTrue(window.styleMask.contains(.resizable))
            let panel = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 300, height: 200),
                                styleMask: [.titled, .closable], backing: .buffered, defer: false)
            panel.isReleasedWhenClosed = false
            XCTAssertEqual(panel.effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]), expected)
            panel.close()
        }
        appearance.setTheme(.system, persist: false)
        XCTAssertNil(NSApp.appearance)
        XCTAssertNil(window.appearance)
        XCTAssertEqual(window.effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]),
                       NSApp.effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]))
    }
}
