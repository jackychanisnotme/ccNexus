#!/usr/bin/env bash
set -euo pipefail

target_dir="${1:-.}"

if [ ! -d "$target_dir" ]; then
  echo "Target directory not found: $target_dir" >&2
  exit 1
fi

find "$target_dir" -maxdepth 1 -type f \( -name '*.zip' -o -name '*.tar.gz' -o -name '*.dmg' \) -print0 |
  while IFS= read -r -d '' file; do
    if command -v shasum >/dev/null 2>&1; then
      shasum -a 256 "$file" > "$file.sha256"
    else
      sha256sum "$file" > "$file.sha256"
    fi
    echo "Wrote $file.sha256"
  done
