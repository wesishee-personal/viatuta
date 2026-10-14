import SwiftUI

@main
struct ViatutaApp: App {
    @State private var auth: AuthStore
    @State private var hazards: HazardService

    init() {
        let auth = AuthStore()
        _auth = State(initialValue: auth)
        // One hazard service for the whole app, so every screen that reports or
        // responds to hazards (planning now, navigation later) shares it.
        _hazards = State(initialValue: HazardService(api: auth.api))
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(auth)
                .environment(hazards)
        }
    }
}
