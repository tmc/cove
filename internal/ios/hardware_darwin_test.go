//go:build darwin

package ios

import (
	"testing"

	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
)

func TestCheckMethod(t *testing.T) {
	class := objc.GetClass("NSObject")
	if class == 0 {
		t.Fatal("NSObject unavailable")
	}
	m := objectivec.Class_getInstanceMethod(class, objectivec.SEL(objc.Sel("description")))
	tests := []struct {
		name   string
		method objectivec.Method
		result string
		args   []string
		valid  bool
	}{
		{"object result", m, "@", []string{"@", ":"}, true},
		{"missing", 0, "@", []string{"@", ":"}, false},
		{"wrong result", m, "v", []string{"@", ":"}, false},
		{"wrong argument count", m, "@", []string{"@"}, false},
		{"wrong argument type", m, "@", []string{"I", ":"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkMethod(tt.method, tt.result, tt.args)
			if (err == nil) != tt.valid {
				t.Fatalf("checkMethod = %v, want valid %v", err, tt.valid)
			}
		})
	}
}

func TestProbeHardwareModel(t *testing.T) {
	class := objc.GetClass("_VZMacHardwareModelDescriptor")
	if class != 0 {
		for _, selector := range []string{"setPlatformVersion:", "setBoardID:", "setISA:"} {
			m := objectivec.Class_getInstanceMethod(class, objectivec.SEL(objc.Sel(selector)))
			if m != 0 {
				t.Logf("%s: %s", selector, objc.GoString(objectivec.Method_getTypeEncoding(m)))
			}
		}
	}

	if err := probeHardwareModel(); err != nil {
		t.Logf("host cannot use pinned descriptor ABI: %v", err)
		return
	}
	t.Log("descriptor signatures match; no hardware model or VM was instantiated")
}
