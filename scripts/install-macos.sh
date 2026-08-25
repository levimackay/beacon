#!/usr/bin/env bash
# Build, sign and install Beacon.app with its widget.
#
# The widget extension needs two things macOS will not compromise on: a
# signature from a real team identity, and the App Sandbox. Without either,
# the extension builds and installs perfectly and then never appears in the
# widget gallery, with nothing logged to explain why.
set -euo pipefail

cd "$(dirname "$0")/.."
PROJECT_DIR="apps/macos"
APP_PATH="$PROJECT_DIR/build/Build/Products/Release/Beacon.app"

IDENTITY="${BEACON_SIGN_IDENTITY:-}"
if [ -z "$IDENTITY" ]; then
    IDENTITY=$(security find-identity -v -p codesigning \
        | grep -m1 "Apple Development" \
        | sed -E 's/.*"(.*)"/\1/') || true
fi
if [ -z "$IDENTITY" ]; then
    echo "No Apple Development signing identity found." >&2
    echo "Open Xcode, add your Apple ID under Settings > Accounts, then retry." >&2
    echo "Or set BEACON_SIGN_IDENTITY to the identity you want to use." >&2
    exit 1
fi
echo "Signing with: $IDENTITY"

command -v xcodegen >/dev/null || { echo "xcodegen is required: brew install xcodegen" >&2; exit 1; }

( cd "$PROJECT_DIR" && xcodegen generate )

rm -rf "$PROJECT_DIR/build"
xcodebuild -project "$PROJECT_DIR/Beacon.xcodeproj" -scheme Beacon \
    -configuration Release -derivedDataPath "$PROJECT_DIR/build" \
    CODE_SIGNING_ALLOWED=NO build >/dev/null

# The extension is signed first and with its entitlements; signing the app
# afterwards seals the already-signed extension inside it.
codesign --force --sign "$IDENTITY" \
    --entitlements "$PROJECT_DIR/BeaconWidget/BeaconWidget.entitlements" \
    --timestamp=none "$APP_PATH/Contents/PlugIns/BeaconWidget.appex"
codesign --force --sign "$IDENTITY" --timestamp=none "$APP_PATH"

pkill -f "/Applications/Beacon.app" 2>/dev/null || true
sleep 1
rm -rf /Applications/Beacon.app
cp -R "$APP_PATH" /Applications/

# LaunchServices does not always notice a replaced bundle on its own, and
# an unregistered bundle means an invisible widget.
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister \
    -f /Applications/Beacon.app

open /Applications/Beacon.app
sleep 3

if pluginkit -m -p com.apple.widgetkit-extension 2>/dev/null | grep -q "com.levimackay.beacon.widget"; then
    echo "Installed. The widget is registered."
    echo "Add it: right-click the desktop, choose Edit Widgets, search for Beacon."
else
    echo "Installed, but the widget did not register." >&2
    echo "Check that the extension is sandboxed and signed with a team identity." >&2
    exit 1
fi
