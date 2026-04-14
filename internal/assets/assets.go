// Package assets embeds static resources for cove.
//
// The app icon is maintained as a vz.icon bundle (macOS Icon Composer format)
// containing SVG layers, and converted to vz.icns via go:generate.
package assets

import (
	_ "embed"
	"os"
)

//go:generate ./generate_icns.sh

//go:embed vz.icns
var Icon []byte

// WriteIconToTemp writes the embedded icon to a temporary file and returns its path.
// The caller should defer os.Remove on the returned path.
func WriteIconToTemp() (string, error) {
	f, err := os.CreateTemp("", "cove-icon-*.icns")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(Icon); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}
