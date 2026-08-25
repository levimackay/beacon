# Beacon for macOS

The menu bar app and the WidgetKit widget. The Xcode project is generated
from `project.yml` by [XcodeGen](https://github.com/yonaskolb/XcodeGen)
rather than checked in, so the project file is never a merge conflict.

## Build

    brew install xcodegen
    cd apps/macos
    xcodegen generate
    xcodebuild -project Beacon.xcodeproj -scheme Beacon \
        -configuration Release -derivedDataPath build \
        CODE_SIGNING_ALLOWED=NO build

## Install

The widget has to be signed with a real team identity and sandboxed, or
macOS will not register it. `scripts/install-macos.sh` does the whole
sequence: build, sign the extension with its entitlements, sign the app,
copy it to `/Applications`, re-register it with LaunchServices, and launch
it.

    ./scripts/install-macos.sh

Then add the widget: right-click the desktop, choose Edit Widgets, find
Beacon, and pick a size.

## How the two halves share data

The app is not sandboxed, because it reads the hub's bearer token from
`~/Library/Application Support/Beacon/token`, which a sandboxed process
cannot reach. The widget must be sandboxed, because macOS refuses to
register an unsandboxed widget extension at all. They meet in an App Group
container.

Only the snapshot crosses that boundary, never the token. The widget
therefore does not poll the hub itself: it reports what the app last saw
and says how old that is. Copying the credential somewhere a second process
could read it would widen the blast radius of the one secret the whole
security model rests on, in exchange for a refresh the app already does.

If the app is not running, the widget says so rather than showing the last
healthy reading as though it were current.
