import XCTest

/// Drives the hazard flow through the real app against a running API and saves
/// a screenshot at each step.
///
/// Needs `docker compose up -d` (the API on localhost:8080). It creates its own
/// throwaway accounts and hazards; remove them afterwards with the clean-up
/// query in ios/README.md. Screenshots go to $SCREENSHOT_DIR when set (pass it
/// to xcodebuild as TEST_RUNNER_SCREENSHOT_DIR) and are always attached to the
/// result bundle.
final class HazardFlowUITests: XCTestCase {
    private let api = URL(string: "http://localhost:8080")!
    private let password = "correct-horse-battery-\(Int.random(in: 100...999))"
    private var viewerEmail = ""
    private var accidentID = 0

    override func setUp() async throws {
        continueAfterFailure = false
        let stamp = Int(Date().timeIntervalSince1970)
        viewerEmail = "ui-viewer-\(stamp)@example.com"

        // Hazards reported by someone else, so the signed-in rider can respond
        // to them (a rider cannot confirm their own report).
        let seedToken = try await register("ui-seed-\(stamp)@example.com")
        _ = try await register(viewerEmail)

        accidentID = try await report(seedToken, "accident", 30.2690, -97.7400, heading: 90)
        _ = try await report(seedToken, "road_closed", 30.2640, -97.7470)
        _ = try await report(seedToken, "pothole", 30.2780, -97.7520, heading: 200, notes: "Deep one in the bike lane")
        _ = try await report(seedToken, "glass", 30.2590, -97.7350)
    }

    func testReportAndRespondToHazards() throws {
        let app = XCUIApplication()
        app.launch()
        signIn(app)

        // 1. The map with hazards from the API.
        let accident = app.descendants(matching: .any)["hazard-\(accidentID)"]
        XCTAssertTrue(accident.waitForExistence(timeout: 20), "seeded hazards should appear on the map")
        snap("01-map-with-hazards")

        // 2. Tapping one shows what it is, which way it applies, and how long it lasts.
        accident.tap()
        let stillThere = app.buttons["stillThereButton"]
        XCTAssertTrue(stillThere.waitForExistence(timeout: 5))
        snap("02-hazard-detail")

        // 3. "It's gone" from a rider who did not report it: one vote, not enough.
        app.buttons["itsGoneButton"].tap()
        XCTAssertTrue(app.staticTexts["hazardResultMessage"].waitForExistence(timeout: 5))
        snap("03-hazard-resolve-vote")
        app.buttons["Done"].tap()

        // 4. Reporting: start placement mode.
        app.buttons["reportHazardButton"].tap()
        XCTAssertTrue(app.staticTexts["hazardPlacementBanner"].waitForExistence(timeout: 5))
        snap("04-placing-a-hazard")

        // 5. Tap an empty spot, then choose what it is.
        app.maps.firstMatch.coordinate(withNormalizedOffset: CGVector(dx: 0.3, dy: 0.7)).tap()
        let pothole = app.buttons["kind-pothole"]
        XCTAssertTrue(pothole.waitForExistence(timeout: 10))
        snap("05-report-sheet")

        // 6. A closure explains what it will do to routing.
        app.buttons["kind-road_closed"].tap()
        XCTAssertTrue(app.staticTexts["hazardLifetimeHint"].waitForExistence(timeout: 5))
        snap("06-report-sheet-road-closed")

        // 7. Report it.
        app.buttons["submitHazardReport"].tap()
        XCTAssertTrue(app.staticTexts["hazardToast"].waitForExistence(timeout: 10))
        snap("07-reported")
    }

    // MARK: Steps

    private func signIn(_ app: XCUIApplication) {
        // The Keychain outlives app installs on the simulator, so a sign-in
        // from an earlier run may still be there. Start from signed out.
        let signOut = app.buttons["Sign out"]
        if signOut.waitForExistence(timeout: 4) { signOut.tap() }

        let email = app.textFields["Email"]
        XCTAssertTrue(email.waitForExistence(timeout: 10))
        email.tap()
        email.typeText(viewerEmail)
        let field = app.secureTextFields["Password"]
        field.tap()
        field.typeText(password)
        app.buttons["Sign in"].firstMatch.tap()

        dismissSavePasswordPrompt(app)
    }

    /// iOS offers to save the password after a sign-in. That sheet belongs to
    /// the system, not the app, so it is not in the app's accessibility tree.
    /// It sits over the map and swallows taps until it is dismissed.
    private func dismissSavePasswordPrompt(_ app: XCUIApplication) {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for candidate in [springboard.buttons["Not Now"], app.buttons["Not Now"], springboard.alerts.buttons["Not Now"]] {
            if candidate.waitForExistence(timeout: 4) {
                candidate.tap()
                return
            }
        }
        // Not found anywhere: leave a trace so the next change is informed.
        print("SAVE-PASSWORD PROMPT NOT FOUND. Springboard hierarchy:\n\(springboard.debugDescription)")
    }

    private func snap(_ name: String) {
        // Give map tiles and animations a moment to settle.
        Thread.sleep(forTimeInterval: 1.5)
        let shot = XCUIScreen.main.screenshot()
        let attachment = XCTAttachment(screenshot: shot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["SCREENSHOT_DIR"] {
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            try? shot.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name).png"))
        }
    }

    // MARK: API helpers

    private func register(_ email: String) async throws -> String {
        let body = try JSONSerialization.data(withJSONObject: ["email": email, "password": password])
        let json = try await post("v1/auth/register", body: body, token: nil)
        return try XCTUnwrap(json["token"] as? String)
    }

    private func report(_ token: String, _ kind: String, _ lat: Double, _ lon: Double,
                        heading: Double? = nil, notes: String? = nil) async throws -> Int {
        var payload: [String: Any] = ["location": ["lat": lat, "lon": lon], "kind": kind]
        if let heading { payload["heading_deg"] = heading }
        if let notes { payload["notes"] = notes }
        let json = try await post("v1/hazards", body: JSONSerialization.data(withJSONObject: payload), token: token)
        return try XCTUnwrap(json["id"] as? Int)
    }

    private func post(_ path: String, body: Data, token: String?) async throws -> [String: Any] {
        var request = URLRequest(url: api.appending(path: path))
        request.httpMethod = "POST"
        request.httpBody = body
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let token { request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        XCTAssertTrue((200..<300).contains(status), "POST \(path) -> \(status): \(String(data: data, encoding: .utf8) ?? "")")
        return try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }
}
