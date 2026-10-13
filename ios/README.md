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
