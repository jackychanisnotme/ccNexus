#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PUBLIC_KEY_FILE="${CCNEXUS_LICENSE_PUBLIC_KEY_FILE:-$HOME/.ccnexus-license/public_key.txt}"
OUT_DIR="$ROOT_DIR/dist/pro"
PUBLIC_KEY=""

show_error() {
  osascript -e "display dialog \"$1\" buttons {\"知道了\"} default button 1 with icon stop" >/dev/null
}

show_info() {
  osascript -e "display dialog \"$1\" buttons {\"知道了\"} default button 1 with icon note" >/dev/null
}

run_wails() {
  if command -v wails >/dev/null 2>&1; then
    wails "$@"
    return
  fi
  return 1
}

build_go_app_bundle() {
  local package_dir="$1"
  local binary_name="$2"
  local app_name="$3"
  local display_name="$4"
  local ldflags="$5"
  local app_dir="$package_dir/build/bin/$app_name.app"
  local binary_path="$app_dir/Contents/MacOS/$binary_name"

  if ! command -v go >/dev/null 2>&1; then
    show_error "未找到 Go。请先在打包设备上安装 Go 构建环境，或换到已配置好的打包设备。"
    exit 1
  fi

  rm -rf "$app_dir"
  mkdir -p "$app_dir/Contents/MacOS" "$app_dir/Contents/Resources"
  CGO_ENABLED=1 \
  CGO_LDFLAGS="${CGO_LDFLAGS:-} -framework UniformTypeIdentifiers -mmacosx-version-min=10.13" \
  GOOS=darwin \
  GOARCH="$(go env GOARCH)" \
  go build -buildvcs=false -tags "desktop,wv2runtime.download,production" -ldflags "$ldflags" -o "$binary_path" .
  chmod +x "$binary_path"
  cat > "$app_dir/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>zh_CN</string>
  <key>CFBundleExecutable</key>
  <string>$binary_name</string>
  <key>CFBundleIdentifier</key>
  <string>com.ccnexus.$binary_name</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>$display_name</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>1.0.0</string>
  <key>CFBundleVersion</key>
  <string>1.0.0</string>
  <key>LSMinimumSystemVersion</key>
  <string>10.13</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
PLIST
}

build_go_windows_exe() {
  local package_dir="$1"
  local output_name="$2"
  local ldflags="$3"
  local output_dir="$package_dir/build/bin/windows"
  local output_path="$output_dir/$output_name"

  if ! command -v go >/dev/null 2>&1; then
    show_error "未找到 Go。请先在打包设备上安装 Go 构建环境，或换到已配置好的打包设备。"
    exit 1
  fi

  mkdir -p "$output_dir"
  cd "$package_dir"
  CGO_ENABLED=0 \
  GOOS=windows \
  GOARCH=amd64 \
  go build -buildvcs=false -tags "desktop,wv2runtime.download,production" -ldflags "$ldflags" -o "$output_path" .
}

build_with_wails_or_go() {
  local package_dir="$1"
  local binary_name="$2"
  local app_name="$3"
  local display_name="$4"
  local ldflags="$5"

  cd "$package_dir"
  if CGO_LDFLAGS="${CGO_LDFLAGS:-} -framework UniformTypeIdentifiers -mmacosx-version-min=10.13" run_wails build -clean -ldflags "$ldflags"; then
    return
  fi
  build_go_app_bundle "$package_dir" "$binary_name" "$app_name" "$display_name" "$ldflags"
}

mkdir -p "$OUT_DIR"

if [ -n "${CCNEXUS_LICENSE_PUBLIC_KEY:-}" ]; then
  PUBLIC_KEY="$(printf '%s' "$CCNEXUS_LICENSE_PUBLIC_KEY" | tr -d '\r\n\t ')"
elif [ ! -f "$PUBLIC_KEY_FILE" ]; then
  if [ "${CCNEXUS_PRO_BUILD_NO_OPEN:-}" != "1" ]; then
    open "$OUT_DIR"
    show_info "未找到在线授权公钥。请先启动 cmd/license-server 生成公钥，或设置 CCNEXUS_LICENSE_PUBLIC_KEY/CCNEXUS_LICENSE_PUBLIC_KEY_FILE 后再次打包。"
  else
    echo "license generator output: $OUT_DIR"
    echo "public key missing: $PUBLIC_KEY_FILE"
  fi
  exit 0
else
  PUBLIC_KEY="$(tr -d '\r\n\t ' < "$PUBLIC_KEY_FILE")"
fi

if [ -z "$PUBLIC_KEY" ]; then
  if [ "${CCNEXUS_PRO_BUILD_NO_OPEN:-}" != "1" ]; then
    open "$OUT_DIR"
    show_info "在线授权公钥为空。请检查 CCNEXUS_LICENSE_PUBLIC_KEY 或 $PUBLIC_KEY_FILE 后再次打包。"
  else
    echo "license generator output: $OUT_DIR"
    echo "public key is empty: $PUBLIC_KEY_FILE"
  fi
  exit 0
fi

if command -v npm >/dev/null 2>&1; then
  cd "$ROOT_DIR/cmd/desktop/frontend"
  npm install
  npm run build
else
  show_error "未找到 npm。请先在打包设备上安装 Node.js，或换到已配置好的打包设备。"
  exit 1
fi

build_with_wails_or_go "$ROOT_DIR/cmd/desktop" "ccNexus" "ccNexus" "ccNexus" "-w -s -X github.com/lich0821/ccNexus/internal/onlinelicense.AppPublicKey=$PUBLIC_KEY"

CUSTOMER_APP="$ROOT_DIR/cmd/desktop/build/bin/ccNexus.app"
if [ ! -d "$CUSTOMER_APP" ]; then
  osascript -e 'display dialog "未找到 ccNexus.app，构建失败。" buttons {"知道了"} default button 1 with icon stop' >/dev/null
  exit 1
fi

xattr -cr "$CUSTOMER_APP" || true
codesign --force --deep --sign - "$CUSTOMER_APP"
ditto -c -k --norsrc --keepParent "$CUSTOMER_APP" "$OUT_DIR/ccNexus-Pro-mac.zip"
build_go_windows_exe "$ROOT_DIR/cmd/desktop" "ccNexus.exe" "-w -s -X github.com/lich0821/ccNexus/internal/onlinelicense.AppPublicKey=$PUBLIC_KEY"
ditto -c -k --norsrc "$ROOT_DIR/cmd/desktop/build/bin/windows/ccNexus.exe" "$OUT_DIR/ccNexus-Pro-windows.zip"

if [ "${CCNEXUS_PRO_BUILD_NO_OPEN:-}" != "1" ]; then
  open "$OUT_DIR"
  osascript -e 'display notification "客户包已生成到 dist/pro。" with title "ccNexus Pro 打包完成"'
else
  echo "ccNexus Pro build output: $OUT_DIR"
fi
