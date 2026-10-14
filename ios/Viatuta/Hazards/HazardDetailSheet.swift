import SwiftUI

/// A hazard on the map, with the two ways a rider can respond to it: "it's
/// still there" or "it's gone".
struct HazardDetailSheet: View {
    let hazard: HazardReport

    @Environment(HazardService.self) private var service
    @Environment(\.dismiss) private var dismiss

    @State private var isSending = false
    @State private var resultMessage: String?
    @State private var errorMessage: String?

    private var kind: HazardKindInfo? { service.kind(named: hazard.kind) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 12) {
                HazardBadge(hazard: hazard, kind: kind)
                VStack(alignment: .leading, spacing: 2) {
                    Text(kind?.label ?? hazard.kind).font(.headline)
                    if let direction { Text(direction).font(.subheadline).foregroundStyle(.secondary) }
                }
                Spacer()
                Button("Done") { dismiss() }
            }

            if let notes = hazard.notes { Text(notes).font(.callout) }

            VStack(alignment: .leading, spacing: 2) {
                Text("Reported \(HazardFormat.reportedAgo(hazard.createdAt)) · \(HazardFormat.expiresIn(hazard.expiresAt))")
                Text(hazard.confirmations == 0
                     ? "Nobody has confirmed this yet"
                     : "Confirmed by \(hazard.confirmations) other rider\(hazard.confirmations == 1 ? "" : "s")")
            }
            .font(.footnote)
            .foregroundStyle(.secondary)

            if let resultMessage {
                Label(resultMessage, systemImage: "checkmark.circle.fill")
                    .font(.subheadline)
                    .foregroundStyle(.green)
                    .accessibilityIdentifier("hazardResultMessage")
            } else {
                HStack {
                    Button { respond(confirm: true) } label: {
                        Text("It's still there").frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .accessibilityIdentifier("stillThereButton")

                    Button { respond(confirm: false) } label: {
                        Text("It's gone").frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                    .accessibilityIdentifier("itsGoneButton")
                }
                .controlSize(.large)
                .disabled(isSending)
            }

            if let errorMessage {
                Text(errorMessage).font(.footnote).foregroundStyle(.red)
            }
        }
        .padding()
        .presentationDetents([.height(250)])
        .presentationBackgroundInteraction(.enabled)
    }

    /// Directional kinds only affect riders going one way. Say which, so the
    /// marker is not read as covering the whole street.
    private var direction: String? {
        guard kind?.directional == true else { return nil }
        if let heading = hazard.headingDeg {
            return "Affects riders heading \(HazardFormat.compass(heading))"
        }
        return "Affects riders in both directions"
    }

    private func respond(confirm: Bool) {
        isSending = true
        errorMessage = nil
        Task {
            defer { isSending = false }
            do {
                if confirm {
                    _ = try await service.confirm(hazard)
                    resultMessage = "Thanks — routing already knows."
                } else {
                    let outcome = try await service.resolve(hazard)
                    resultMessage = outcome.tookDown
                        ? "Thanks — it's been taken down."
                        : "Thanks. It comes down once one more rider agrees it's gone."
                }
            } catch {
                errorMessage = HazardFormat.userMessage(for: error)
            }
        }
    }
}
