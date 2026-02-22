#!/bin/bash
# Generate vz.icns from vz.icon bundle (Icon Composer format).
# Requires: rsvg-convert (librsvg), sips, iconutil
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ICON_DIR="$SCRIPT_DIR/vz.icon/Assets"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Render SVG layers to 1024x1024 PNGs
rsvg-convert -w 1024 -h 1024 "$ICON_DIR/background.svg" -o "$WORK/bg.png"
rsvg-convert -w 1024 -h 1024 "$ICON_DIR/symbol.svg" -o "$WORK/sym.png"

# Composite layers (symbol over background)
# Use Python since ImageMagick may not be installed
python3 -c "
from PIL import Image
bg = Image.open('$WORK/bg.png').convert('RGBA')
sym = Image.open('$WORK/sym.png').convert('RGBA')
bg.paste(sym, (0, 0), sym)
bg.save('$WORK/icon_1024.png')
" 2>/dev/null || {
    # Fallback: just use background + try sips
    cp "$WORK/bg.png" "$WORK/icon_1024.png"
    echo "warning: PIL not available, using background only (install Pillow for compositing)"
}

# Generate iconset with all required sizes
ICONSET="$WORK/vz.iconset"
mkdir -p "$ICONSET"
for size in 16 32 128 256 512; do
    sips -z "$size" "$size" "$WORK/icon_1024.png" --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
    doubled=$((size * 2))
    sips -z "$doubled" "$doubled" "$WORK/icon_1024.png" --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
done

# Convert to icns
iconutil -c icns "$ICONSET" -o "$SCRIPT_DIR/vz.icns"
echo "Generated vz.icns from vz.icon bundle"
