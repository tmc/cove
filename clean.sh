#!/bin/bash
# Clean our VM directory (./vm), not ~/VM.bundle
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
rm -f "$SCRIPT_DIR/vm/aux.img" "$SCRIPT_DIR/vm/hw.model" "$SCRIPT_DIR/vm/machine.id"
echo "Cleaned: aux.img, hw.model, machine.id from $SCRIPT_DIR/vm/"
