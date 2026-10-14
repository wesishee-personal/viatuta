import SwiftUI

@main
struct ViatutaApp: App {
    @State private var auth = AuthStore()

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(auth)
        }
    }
}
