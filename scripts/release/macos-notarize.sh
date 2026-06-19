#!/usr/bin/env bash
set -euo pipefail

app_path="${1:-}"
zip_path="${2:-}"

if [ -z "$app_path" ] || [ -z "$zip_path" ]; then
  echo "Usage: $0 <AINexus.app> <output.zip>" >&2
  exit 1
fi

if [ ! -d "$app_path" ]; then
  echo "App bundle not found: $app_path" >&2
  exit 1
fi

for var in APPLE_TEAM_ID APPLE_ID APPLE_APP_SPECIFIC_PASSWORD; do
  if [ -z "${!var:-}" ]; then
    echo "Missing required environment variable: $var" >&2
    exit 1
  fi
done

identity="$(security find-identity -v -p codesigning | awk -F '"' '/Developer ID Application/ { print $2; exit }')"
if [ -z "$identity" ]; then
  echo "No Developer ID Application identity found in the active keychain" >&2
  exit 1
fi

codesign --deep --force --options runtime --timestamp --sign "$identity" "$app_path"
ditto -c -k --keepParent "$app_path" "$zip_path"
xcrun notarytool submit "$zip_path" \
  --apple-id "$APPLE_ID" \
  --team-id "$APPLE_TEAM_ID" \
  --password "$APPLE_APP_SPECIFIC_PASSWORD" \
  --wait
xcrun stapler staple "$app_path"
spctl --assess --type execute --verbose "$app_path"
ditto -c -k --keepParent "$app_path" "$zip_path"

echo "Signed, notarized, stapled, and repacked: $zip_path"
