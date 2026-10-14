import SwiftUI

struct RootView: View {
    @Environment(AuthStore.self) private var auth

    var body: some View {
        switch auth.state {
        case .restoring:
            ProgressView().task { await auth.restore() }
        case .signedOut:
            LoginView()
        case .signedIn:
            ContentView(api: auth.api)
        }
    }
}
