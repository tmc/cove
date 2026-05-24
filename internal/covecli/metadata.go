package covecli

type Dispatch int

const (
	DispatchPreUI Dispatch = iota
	DispatchEarly
	DispatchLate
)

type Info struct {
	Name              string   `json:"name"`
	Aliases           []string `json:"aliases,omitempty"`
	Summary           string   `json:"summary"`
	Dispatch          string   `json:"dispatch"`
	Outputs           []string `json:"outputs"`
	SafeForDiscovery  bool     `json:"safe_for_discovery"`
	MutatesState      bool     `json:"mutates_state"`
	RequiresRunningVM bool     `json:"requires_running_vm"`
	MayBootVM         bool     `json:"may_boot_vm"`
}

func DispatchName(dispatch Dispatch) string {
	switch dispatch {
	case DispatchPreUI:
		return "pre-ui"
	case DispatchEarly:
		return "early"
	case DispatchLate:
		return "late"
	default:
		return "unknown"
	}
}

func SafeForDiscovery(name string) bool {
	return !MutatesState(name) && !RequiresRunningVM(name) && !MayBootVM(name)
}

func MutatesState(name string) bool {
	switch name {
	case "action", "agent-sandbox", "agent-upgrade", "bench", "build", "clean", "clone", "compact", "config", "daemon", "disk-detach", "disk-snapshot", "export", "fleet", "fork", "forward", "gc", "helper", "image", "import", "inject", "inject-agent", "install", "network", "pin", "pit", "policy", "provision", "provision-agent", "push", "quota", "rename", "rm", "rosetta", "run", "serve", "shared-folder", "sip", "snapshot", "softreset", "storage", "store", "template", "trace", "uiscript", "unpin", "up", "verify", "vm", "vzscript":
		return true
	default:
		return false
	}
}

func RequiresRunningVM(name string) bool {
	switch name {
	case "agent-upgrade", "cp", "ctl", "logs", "shell", "status", "trace", "vzscript":
		return true
	default:
		return false
	}
}

func MayBootVM(name string) bool {
	switch name {
	case "agent-sandbox", "build", "install", "run", "up", "action":
		return true
	default:
		return false
	}
}

func OutputHints(name string) []string {
	switch name {
	case "action", "commands", "daemon", "diff", "recording", "runner", "runs", "security", "storage", "trace", "vm":
		return []string{"text", "json"}
	case "ctl":
		return []string{"text", "json", "binary"}
	case "serve":
		return []string{"text", "http", "mcp"}
	default:
		return []string{"text"}
	}
}
