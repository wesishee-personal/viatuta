# Viatuta iOS

SwiftUI client for the Viatuta API. Currently a scaffold: an app that shows a
map of Austin. No networking, auth or routing yet.

## Setup

1. Install Xcode from the App Store, open it once, accept the licence, then:
   `sudo xcode-select -s /Applications/Xcode.app/Contents/Developer`
2. `brew install xcodegen`
3. `cd ios && xcodegen generate && open Viatuta.xcodeproj`
4. Pick an iPhone simulator and press Run (Cmd+R).

`Viatuta.xcodeproj` is generated from `project.yml` and is not committed. Edit
`project.yml` (not the project in Xcode) to change targets or build settings,
then re-run `xcodegen generate`.

To run on your own iPhone, set your Apple ID team under
Signing & Capabilities in Xcode (free, no paid account needed).

## Tests

```bash
# Unit tests (fast, no network):
xcodebuild -project Viatuta.xcodeproj -scheme Viatuta \
  -destination 'platform=iOS Simulator,name=iPhone 17' test
```

Run them **signed** (the default). Passing `CODE_SIGNING_ALLOWED=NO` installs an
unsigned build on the simulator, which cannot use the Keychain, and breaks
sign-in for the copy you run by hand.

### UI tests (slow, need the API)

`ViatutaUITests` drives the real app through the hazard flow and saves a
screenshot at each step. It needs the API on `localhost:8080`
(`docker compose up -d`) and has its own scheme so it stays out of the normal
run:

```bash
TEST_RUNNER_SCREENSHOT_DIR=/tmp/shots \
xcodebuild -project Viatuta.xcodeproj -scheme ViatutaUITests \
  -destination 'platform=iOS Simulator,name=iPhone 17' test
```

It creates throwaway `ui-seed-*` and `ui-viewer-*` accounts and some hazards.
Remove them afterwards:

```bash
docker compose exec -T db psql -U viatuta -d viatuta -c "
  delete from hazard_report where user_id in
    (select id from user_account where email like 'ui-%@example.com');
  delete from user_account where email like 'ui-%@example.com';"
docker compose restart api   # the API caches hazard scores in memory
```
