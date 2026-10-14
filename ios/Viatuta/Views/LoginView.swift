import SwiftUI

struct LoginView: View {
    @Environment(AuthStore.self) private var auth
    @State private var isRegistering = false
    @State private var email = ""
    @State private var password = ""
    @State private var displayName = ""

    private var canSubmit: Bool {
        !email.isEmpty && !password.isEmpty && !auth.isWorking
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Email", text: $email)
                        .textContentType(.emailAddress)
                        .keyboardType(.emailAddress)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    SecureField("Password", text: $password)
                        .textContentType(isRegistering ? .newPassword : .password)
                    if isRegistering {
                        TextField("Display name (optional)", text: $displayName)
                            .textContentType(.nickname)
                    }
                }

                if let message = auth.errorMessage {
                    Section { Text(message).foregroundStyle(.red) }
                }

                Section {
                    Button(action: submit) {
                        HStack {
                            Text(isRegistering ? "Create account" : "Sign in")
                            if auth.isWorking { Spacer(); ProgressView() }
                        }
                    }
                    .disabled(!canSubmit)

                    Button(isRegistering ? "I already have an account" : "Create an account") {
                        isRegistering.toggle()
                        auth.errorMessage = nil
                    }
                    .font(.footnote)
                }
            }
            .navigationTitle("Viatuta")
        }
    }

    private func submit() {
        Task {
            if isRegistering {
                await auth.register(email: email, password: password, displayName: displayName)
            } else {
                await auth.login(email: email, password: password)
            }
        }
    }
}
