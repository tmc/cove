//go:build darwin

package ios

import (
	"bytes"
	"fmt"

	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/exp/research"
)

// probeHardwareModel checks the Objective-C signatures used to construct the
// research hardware model. It does not instantiate a VM or prove host permission.
func probeHardwareModel() error { return research.ProbeHardwareModel() }

func checkMethod(m objectivec.Method, result string, args []string) error {
	if m == 0 {
		return fmt.Errorf("selector unavailable")
	}
	if got := objectivec.Method_getNumberOfArguments(m); got != uint32(len(args)) {
		return fmt.Errorf("argument count %d, want %d", got, len(args))
	}
	var buf [256]byte
	objectivec.Method_getReturnType(m, &buf[0], uintptr(len(buf)))
	if got := encodingString(buf[:]); got != result {
		return fmt.Errorf("return encoding %q, want %q", got, result)
	}
	for i, want := range args {
		clear(buf[:])
		objectivec.Method_getArgumentType(m, uint32(i), &buf[0], uintptr(len(buf)))
		if got := encodingString(buf[:]); got != want {
			return fmt.Errorf("argument %d encoding %q, want %q", i, got, want)
		}
	}
	return nil
}

func encodingString(buf []byte) string {
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		return string(buf[:i])
	}
	return string(buf)
}

// newHardwareModel constructs the PV=3, board 0x90, ISA 2 research model.
// Call on the runtime's main thread inside an autorelease pool. The caller owns
// the returned reference and must Release it. Signature checks precede private
// calls; successful construction still does not qualify a firmware or VM graph.
func newHardwareModel() (vz.VZMacHardwareModel, error) {
	return research.NewHardwareModel(research.HardwareDescriptor{PlatformVersion: 3, BoardID: 0x90, ISA: 2})
}
