import SwiftUI

@main
struct FocacalMenuBarApp: App {
    @StateObject private var store = FocacalStore()

    var body: some Scene {
        // Menu bar item — always visible, no Dock icon (LSUIElement = YES in Info.plist)
        MenuBarExtra {
            ContentView()
                .environmentObject(store)
        } label: {
            MenuBarLabel()
                .environmentObject(store)
        }
        .menuBarExtraStyle(.window)

        // Settings window — opened via openWindow(id: "settings")
        Window("Settings", id: "settings") {
            SettingsView()
                .environmentObject(store)
        }
        .windowStyle(.hiddenTitleBar)
        .windowResizability(.contentSize)
        .defaultPosition(.center)
        .commands {
            // Remove the default File > New Window menu item
            CommandGroup(replacing: .newItem) {}
        }
    }
}
