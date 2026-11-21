#!/usr/bin/env bash
set -euo pipefail

LYRICIFY_SOURCE="${1:-}"
if [ -z "$LYRICIFY_SOURCE" ]; then
  echo "Usage: $0 <path-to-Lyricify-Lyrics-Helper>" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CSPROJ="$SCRIPT_DIR/FusionBridge.csproj"

publish_mac() {
  local rid="$1"
  local out_file="$2"
  local pub_dir="$SCRIPT_DIR/bin/publish-$rid"
  dotnet publish "$CSPROJ" -c Release -r "$rid" --self-contained true "-p:LyricifySource=$LYRICIFY_SOURCE" -o "$pub_dir" --nologo
  cp -f "$pub_dir/MySpotify.Lyrics" "$REPO_ROOT/internal/fusion/$out_file"
}

publish_mac osx-arm64 bridge-darwin-arm64.bin
publish_mac osx-x64 bridge-darwin-amd64.bin
