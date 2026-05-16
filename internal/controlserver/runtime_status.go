// Runtime status records reported by the network bridge for VNC and
// the kernel debug stub. The types live in this package so the bridge
// (extracted next) can hold them without crossing the package-main
// boundary.
package controlserver

// VNCStatus reports the configured VNC runtime state.
type VNCStatus struct {
	Enabled           bool   `json:"enabled"`
	Port              uint16 `json:"port,omitempty"`
	Endpoint          string `json:"endpoint,omitempty"`
	PasswordProtected bool   `json:"password_protected,omitempty"`
	ServiceName       string `json:"service_name,omitempty"`
	Description       string `json:"description,omitempty"`
	State             string `json:"state,omitempty"`
}

// DebugStubStatus reports the configured debug stub runtime state.
type DebugStubStatus struct {
	Enabled     bool   `json:"enabled"`
	Kind        string `json:"kind,omitempty"`
	Port        uint16 `json:"port,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Connect     string `json:"connect,omitempty"`
	ListenAll   bool   `json:"listen_all,omitempty"`
	State       string `json:"state,omitempty"`
	Description string `json:"description,omitempty"`
}
