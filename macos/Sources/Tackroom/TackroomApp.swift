import AppKit
import SwiftUI

@main
enum Entry {
    static func main() {
        let arguments = CommandLine.arguments
        if let flag = arguments.firstIndex(of: "--write-iconset"), flag + 1 < arguments.count {
            do {
                try Mark.writeIconset(to: URL(fileURLWithPath: arguments[flag + 1]))
            } catch {
                FileHandle.standardError.write(Data("write iconset: \(error)\n".utf8))
                exit(1)
            }
            return
        }
        if arguments.contains("--check") {
            Task { @MainActor in exit(await HeadlessCheck.run()) }
            dispatchMain()
        }
        TackroomApp.main()
    }
}

struct TackroomApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var model = AppModel.shared

    var body: some Scene {
        MenuBarExtra {
            MenuContent(model: model)
        } label: {
            MenuBarLabel(model: model)
        }
        .menuBarExtraStyle(.window)

        Window("Tackroom", id: "main") {
            MainView(model: model)
        }
        .defaultSize(width: 1120, height: 720)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        // Menu bar first: the Dock icon appears only while the window is open.
        NSApp.setActivationPolicy(.accessory)
        NSApp.applicationIconImage = Mark.appIcon(size: 512)
        Task { @MainActor in await AppModel.shared.start() }
    }

    func applicationWillTerminate(_ notification: Notification) {
        MainActor.assumeIsolated { AppModel.shared.shutdown() }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    // Opening the app again (Finder, Spotlight, `open`) while it runs shows the window.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        NotificationCenter.default.post(name: .showMainWindow, object: nil)
        return false
    }
}

extension Notification.Name {
    static let showMainWindow = Notification.Name("dev.tackroom.mac.showMainWindow")
}
