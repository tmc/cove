package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/x/vzkit/disk"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

type commandDispatch int

const (
	commandDispatchPreUI commandDispatch = iota
	commandDispatchEarly
	commandDispatchLate
)

type commandSpec struct {
	Name     string
	Aliases  []string
	Summary  string
	Dispatch commandDispatch
	Run      func(env commandEnv, name string, args []string) int
}

var commandRegistry = []commandSpec{
	{Name: "action", Summary: "Preflight helpers for private GitHub Actions runner images", Dispatch: commandDispatchPreUI, Run: runActionCommand},
	{Name: "agent-sandbox", Summary: "Run a computer-use provider loop in a fresh VM fork", Dispatch: commandDispatchEarly, Run: runAgentSandboxCommand},
	{Name: "agent-upgrade", Aliases: []string{"upgrade-agent"}, Summary: "Live-upgrade vz-agent in a running VM", Dispatch: commandDispatchEarly, Run: runAgentUpgradeCommand},
	{Name: "bench", Summary: "Normalize benchmark evidence into reports and run metrics", Dispatch: commandDispatchEarly, Run: runBenchCommand},
	{Name: "build", Summary: "Chain vzscript steps into a cache-keyed VM image", Dispatch: commandDispatchEarly, Run: runBuildCommand},
	{Name: "clean", Summary: "Remove VM files", Dispatch: commandDispatchLate, Run: runCleanCommand},
	{Name: "clone", Summary: "Clone a VM", Dispatch: commandDispatchLate, Run: runCloneCommand},
	{Name: "commands", Summary: "Print machine-readable command inventory", Dispatch: commandDispatchEarly},
	{Name: "compact", Summary: "Zero guest free space for smaller pushes", Dispatch: commandDispatchEarly, Run: runCompactCommand},
	{Name: "config", Summary: "Export/import framework config snapshots", Dispatch: commandDispatchLate, Run: runVMSubcommand},
	{Name: "cp", Summary: "Copy files between host and guest", Dispatch: commandDispatchEarly, Run: runCpCommand},
	{Name: "ctl", Summary: "Control running VM via socket", Dispatch: commandDispatchEarly, Run: runCtlCommand},
	{Name: "daemon", Summary: "Manage the cove background coordinator", Dispatch: commandDispatchEarly, Run: runDaemonCommand},
	{Name: "diff", Summary: "Compare local image disk layer metadata", Dispatch: commandDispatchEarly, Run: runDiffCommand},
	{Name: "disk-detach", Summary: "Detach VM disk if stuck", Dispatch: commandDispatchEarly, Run: runDiskDetachCommand},
	{Name: "disk-snapshot", Summary: "Manage disk-level snapshots", Dispatch: commandDispatchLate, Run: runDiskSnapshotCommand},
	{Name: "export", Summary: "Export VM to tarball", Dispatch: commandDispatchLate, Run: runVMSubcommand},
	{Name: "fleet", Summary: "Register and use remote cove hosts", Dispatch: commandDispatchEarly, Run: runFleetCommandSpec},
	{Name: "fork", Summary: "CoW-fork a VM with a fresh identity", Dispatch: commandDispatchEarly, Run: runForkCommand},
	{Name: "forward", Summary: "Forward host TCP to guest TCP", Dispatch: commandDispatchEarly, Run: runForwardCommand},
	{Name: "gc", Summary: "Delete old disposable VM clones", Dispatch: commandDispatchEarly, Run: runGCCommand},
	{Name: "helper", Summary: "Manage the privileged helper", Dispatch: commandDispatchLate, Run: runHelperCommandSpec},
	{Name: "image", Summary: "Local VM image store", Dispatch: commandDispatchEarly, Run: runImageCommand},
	{Name: "import", Summary: "Import VM from tarball", Dispatch: commandDispatchLate, Run: runVMSubcommand},
	{Name: "inject", Summary: "Deprecated alias for provision", Dispatch: commandDispatchEarly, Run: runProvisionCommand},
	{Name: "inject-agent", Summary: "Deprecated alias for provision-agent", Dispatch: commandDispatchEarly, Run: runProvisionAgentCommand},
	{Name: "install", Summary: "Install OS", Dispatch: commandDispatchLate, Run: runInstallCommand},
	{Name: "list", Aliases: []string{"ls"}, Summary: "List available VMs and templates", Dispatch: commandDispatchLate, Run: runListCommand},
	{Name: "logs", Summary: "Show guest logs from a running VM", Dispatch: commandDispatchEarly, Run: runLogsCommand},
	{Name: "network", Summary: "Network configuration", Dispatch: commandDispatchLate, Run: runNetworkCommandSpec},
	{Name: "pin", Summary: "Pin an object so storage budget eviction skips it", Dispatch: commandDispatchEarly, Run: runPinCommand},
	{Name: "pins", Summary: "List pinned objects", Dispatch: commandDispatchEarly, Run: runPinsCommand},
	{Name: "pit", Summary: "Experimental point-in-time save, restore, run, and swap", Dispatch: commandDispatchLate, Run: runPITCommandSpec},
	{Name: "policy", Summary: "VM lifecycle policy", Dispatch: commandDispatchEarly, Run: runPolicyCommand},
	{Name: "provision", Summary: "Write provisioning files into VM disk", Dispatch: commandDispatchEarly, Run: runProvisionCommand},
	{Name: "provision-agent", Summary: "Provision vz-agent daemon", Dispatch: commandDispatchEarly, Run: runProvisionAgentCommand},
	{Name: "pull", Summary: "Validate an OCI pull plan", Dispatch: commandDispatchEarly, Run: runPullCommand},
	{Name: "push", Summary: "Plan a VM disk OCI push", Dispatch: commandDispatchEarly, Run: runPushCommand},
	{Name: "quota", Summary: "Show or set per-VM resource quotas", Dispatch: commandDispatchEarly, Run: runQuotaCommand},
	{Name: "recording", Aliases: []string{"recordings"}, Summary: "List and export run recording artifacts", Dispatch: commandDispatchEarly, Run: runRecordingCommand},
	{Name: "rename", Summary: "Rename a VM", Dispatch: commandDispatchLate, Run: runVMSubcommand},
	{Name: "rm", Aliases: []string{"remove", "destroy"}, Summary: "Delete a VM", Dispatch: commandDispatchLate, Run: runVMDeleteAliasCommand},
	{Name: "rosetta", Summary: "Rosetta 2 for Linux VMs", Dispatch: commandDispatchLate, Run: runRosettaCommandSpec},
	{Name: "run", Summary: "Run a VM", Dispatch: commandDispatchLate, Run: runRunCommand},
	{Name: "runner", Summary: "Generate hosted-runner workflow scaffolding", Dispatch: commandDispatchEarly, Run: runRunnerCommand},
	{Name: "runs", Summary: "Inspect local run metrics and artifacts", Dispatch: commandDispatchEarly, Run: runRunsCommand},
	{Name: "secret", Summary: "Debug secret resolver", Dispatch: commandDispatchEarly, Run: runSecretCommand},
	{Name: "security", Summary: "Inspect host-containment policy", Dispatch: commandDispatchEarly, Run: runSecurityCommand},
	{Name: "serve", Summary: "Multi-VM HTTP gateway", Dispatch: commandDispatchEarly, Run: runServeCommandSpec},
	{Name: "shared-folder", Aliases: []string{"shared-folders"}, Summary: "Manage shared folders", Dispatch: commandDispatchEarly, Run: runSharedFolderCommand},
	{Name: "shell", Summary: "Open a Docker-shaped exec session", Dispatch: commandDispatchEarly, Run: runShellCommand},
	{Name: "sip", Summary: "SIP management", Dispatch: commandDispatchEarly, Run: runSIPCommand},
	{Name: "snapshot", Summary: "Manage VM state snapshots", Dispatch: commandDispatchLate, Run: runSnapshotCommandSpec},
	{Name: "softreset", Summary: "Run destructive soft-reset probe matrix", Dispatch: commandDispatchEarly, Run: runSoftresetCommand},
	{Name: "status", Summary: "Show VM status", Dispatch: commandDispatchEarly, Run: runStatusCommand},
	{Name: "storage", Summary: "Inspect cove disk usage under ~/.vz/", Dispatch: commandDispatchEarly, Run: runStorageCommand},
	{Name: "store", Summary: "Manage the local OCI blob store", Dispatch: commandDispatchEarly, Run: runStoreCommand},
	{Name: "support", Summary: "Create support diagnostics bundles", Dispatch: commandDispatchEarly, Run: runSupportCommandSpec},
	{Name: "template", Summary: "Manage VM templates", Dispatch: commandDispatchLate, Run: runTemplateCommand},
	{Name: "trace", Aliases: []string{"traces"}, Summary: "Manage eslogger guest traces", Dispatch: commandDispatchEarly, Run: runTraceCommand},
	{Name: "uiscript", Summary: "Deprecated alias for vzscript", Dispatch: commandDispatchEarly, Run: runUIScriptCommand},
	{Name: "unpin", Summary: "Remove a storage pin", Dispatch: commandDispatchEarly, Run: runUnpinCommand},
	{Name: "up", Summary: "Install + provision + boot in one command", Dispatch: commandDispatchEarly, Run: runUpCommand},
	{Name: "verify", Aliases: []string{"doctor"}, Summary: "Verify provisioning files in VM disk", Dispatch: commandDispatchEarly, Run: runVerifyCommand},
	{Name: "version", Summary: "Print version information", Dispatch: commandDispatchEarly, Run: runVersionCommand},
	{Name: "vm", Summary: "Manage VMs", Dispatch: commandDispatchLate, Run: runVMCommandSpec},
	{Name: "vzscript", Summary: "Run guest-agent and UI automation scripts", Dispatch: commandDispatchEarly, Run: runVZScriptCommand},
}

func lookupCommand(name string) (*commandSpec, bool) {
	for i := range commandRegistry {
		spec := &commandRegistry[i]
		if name == spec.Name {
			return spec, true
		}
		for _, alias := range spec.Aliases {
			if name == alias {
				return spec, true
			}
		}
	}
	return nil, false
}

func commandNames() []string {
	var names []string
	for _, spec := range commandRegistry {
		names = append(names, spec.Name)
		names = append(names, spec.Aliases...)
	}
	names = append(names, "help")
	return names
}

func runRegisteredCommand(env commandEnv, spec *commandSpec, name string, args []string) int {
	if spec == nil || spec.Run == nil {
		if spec != nil && spec.Name == "commands" {
			return runCommandsCommand(env, name, args)
		}
		return 2
	}
	return spec.Run(env, name, args)
}

func commandError(env commandEnv, err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(env.Stderr, "error: %v\n", err)
	return 1
}

func commandUsageError(env commandEnv, err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(env.Stderr, "error: %v\n", err)
	return 2
}

func runActionCommand(_ commandEnv, _ string, args []string) int {
	return handleActionCommand(args)
}

func runAgentSandboxCommand(env commandEnv, _ string, args []string) int {
	if len(args) == 0 {
		return commandUsageError(env, handleAgentSandboxCommand(args))
	}
	return commandError(env, handleAgentSandboxCommand(args))
}
func runAgentUpgradeCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleAgentUpgradeCommand(args))
}
func runBenchCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleBenchCommand(args))
}
func runBuildCommand(env commandEnv, _ string, args []string) int {
	if len(args) == 0 {
		return commandUsageError(env, handleBuild(args))
	}
	return commandError(env, handleBuild(args))
}
func runCompactCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleCompact(args))
}
func runCpCommand(env commandEnv, _ string, args []string) int {
	err := handleCpCommand(args)
	if err != nil && strings.HasPrefix(err.Error(), "usage: cove cp ") {
		printCpUsage(env.Stderr)
		return commandUsageError(env, err)
	}
	return commandError(env, err)
}
func runCtlCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, ctlCommand(args))
}
func runDaemonCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, daemonCommand(args))
}
func runDiffCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, diffCommand(args))
}
func runFleetCommandSpec(env commandEnv, _ string, args []string) int {
	if len(args) == 0 {
		return commandUsageError(env, handleFleetCommand(args))
	}
	return commandError(env, handleFleetCommand(args))
}
func runForwardCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, forwardCommand(args))
}
func runGCCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleGCCommand(args))
}
func runImageCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleImageCommand(args))
}
func runLogsCommand(env commandEnv, _ string, args []string) int {
	err := logsCommand(args)
	if err != nil && strings.HasPrefix(err.Error(), "usage: cove logs ") {
		printLogsUsage(env.Stderr)
		return commandUsageError(env, err)
	}
	return commandError(env, err)
}
func runPolicyCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handlePolicyCommand(args))
}
func runPullCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handlePull(args))
}
func runPinCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handlePinCommand(args))
}
func runPinsCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handlePinsCommand(args))
}
func runPushCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handlePush(args))
}
func runRecordingCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleRecordingCommand(args))
}
func runQuotaCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleQuotaCommand(args))
}
func runUnpinCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleUnpinCommand(args))
}
func runRunsCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleRunsCommand(args))
}
func runSecretCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleSecretCommand(args))
}
func runShellCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, shellCommand(args))
}
func runSIPCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleSIPCommand(args))
}
func runSoftresetCommand(env commandEnv, _ string, args []string) int {
	if len(args) == 0 {
		return commandUsageError(env, softresetCommand(args))
	}
	return commandError(env, softresetCommand(args))
}
func runStorageCommand(env commandEnv, _ string, args []string) int {
	if len(args) == 0 {
		return commandUsageError(env, handleStorageCommand(args))
	}
	return commandError(env, handleStorageCommand(args))
}
func runStoreCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleStoreCommand(args))
}
func runSupportCommandSpec(env commandEnv, _ string, args []string) int {
	if len(args) == 0 {
		return commandUsageError(env, handleSupportCommand(args))
	}
	return commandError(env, handleSupportCommand(args))
}
func runUpCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleUp(args))
}
func runVerifyCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleVerify(args))
}
func runVZScriptCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, vzscriptCommand(args))
}

func runCleanCommand(env commandEnv, _ string, _ []string) int { return commandError(env, cleanVM()) }
func runCloneCommand(_ commandEnv, _ string, args []string) int {
	handleClone(args)
	return 0
}
func runDiskSnapshotCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleDiskSnapshotCommand(args))
}
func runHelperCommandSpec(env commandEnv, _ string, args []string) int {
	return commandError(env, runHelperCmd(args))
}
func runListCommand(env commandEnv, _ string, _ []string) int {
	if err := handleListTo(env.Stdout); err != nil {
		return commandError(env, err)
	}
	return 0
}
func runNetworkCommandSpec(_ commandEnv, _ string, args []string) int {
	handleNetworkCommand(args)
	return 0
}
func runPITCommandSpec(env commandEnv, _ string, args []string) int {
	return commandError(env, handlePITCommand(args))
}
func runRosettaCommandSpec(env commandEnv, _ string, args []string) int {
	return commandError(env, handleRosettaCommand(args))
}
func runRunCommand(_ commandEnv, _ string, _ []string) int {
	handleRun()
	return 0
}
func runSnapshotCommandSpec(_ commandEnv, _ string, args []string) int {
	handleSnapshotCommand(args)
	return 0
}
func runStatusCommand(env commandEnv, _ string, args []string) int {
	err := statusCommand(args...)
	if err != nil && strings.HasPrefix(err.Error(), "usage: cove status") {
		printStatusUsage(env.Stderr)
		return commandUsageError(env, err)
	}
	return commandError(env, err)
}
func runTemplateCommand(_ commandEnv, _ string, args []string) int {
	handleTemplate(args)
	return 0
}
func runTraceCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleTraceCommand(args))
}
func runVMCommandSpec(_ commandEnv, _ string, args []string) int {
	handleVMCommand(args)
	return 0
}

func runVersionCommand(env commandEnv, _ string, _ []string) int {
	fmt.Fprintln(env.Stdout, versionInfo())
	return 0
}

func runProvisionCommand(env commandEnv, name string, args []string) int {
	if name == "inject" {
		fmt.Fprintf(env.Stderr, "note: 'inject' has been renamed to 'provision'\n")
	}
	return commandError(env, handleProvision(args))
}

func runProvisionAgentCommand(env commandEnv, name string, _ []string) int {
	if name == "inject-agent" {
		fmt.Fprintf(env.Stderr, "note: 'inject-agent' has been renamed to 'provision-agent'\n")
	}
	return commandError(env, provisionAgent())
}

func runSharedFolderCommand(env commandEnv, _ string, args []string) int {
	if sharedFolderCommandBlocked(args) {
		fmt.Fprintf(env.Stderr, "error: -sandbox-level %s does not allow shared-folder mutations\n", sandboxLevel)
		return 1
	}
	return commandError(env, handleVMSharedFolderCommand(args))
}

func runDiskDetachCommand(env commandEnv, _ string, _ []string) int {
	diskFile := filepath.Join(vmDir, "disk.img")
	if err := disk.EnsureDetached(diskFile); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	if verbose {
		fmt.Fprintln(env.Stdout, "Disk detached successfully.")
	}
	return 0
}

func runUIScriptCommand(env commandEnv, _ string, _ []string) int {
	fmt.Fprintf(env.Stderr, "warning: the 'uiscript' command has been merged into 'vzscript'.\nUse 'cove vzscript' instead.\n")
	return 0
}

func runServeCommandSpec(env commandEnv, _ string, args []string) int {
	return commandError(env, runServeCmd(args))
}

func runForkCommand(_ commandEnv, _ string, args []string) int {
	handleFork(args)
	return 0
}

func runInstallCommand(env commandEnv, _ string, _ []string) int {
	installVM = true
	var err error
	if windowsMode {
		err = installWindowsVM()
	} else if linuxMode {
		err = handleLinuxInstall()
	} else {
		err = installMacOSLikeVZ(context.Background())
	}
	if errors.Is(err, errRestartVM) {
		if err := runMacOSVM(); err != nil {
			fmt.Fprintf(env.Stderr, "error: %v\n", err)
			return 1
		}
	} else if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	if installVZScripts != "" {
		if err := runPostInstallVZScripts(installVZScripts); err != nil {
			fmt.Fprintf(env.Stderr, "error: running vzscripts: %v\n", err)
			return 1
		}
	}
	return 0
}

func runVMDeleteAliasCommand(_ commandEnv, _ string, args []string) int {
	handleVMCommand(append([]string{"delete"}, args...))
	return 0
}

func runVMSubcommand(_ commandEnv, name string, args []string) int {
	handleVMCommand(append([]string{name}, args...))
	return 0
}

func runLegacyInstallFlag() int {
	fmt.Fprintf(os.Stderr, "warning: -install flag is deprecated, use 'cove install' command instead\n")
	return runInstallCommand(newCommandEnv(), "install", nil)
}

func runLegacyRunFlag() int {
	fmt.Fprintf(os.Stderr, "warning: -run flag is deprecated, use 'cove run' command instead\n")
	handleRun()
	return 0
}

func rerunVMDirForPostCommand(cmd string, args []string) int {
	cmdArgs := append([]string{cmd}, args...)
	if vmName == "" || subcommandSkipsVMDir(cmdArgs) {
		if cmd == "run" && vmName != "" && !argsContainFlag(args, "fork-from") {
			dir, err := requireExistingRunVMDir(vmName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			vmDir = dir
			applyVMConfig(vmDir)
		}
		return 0
	}
	var err error
	vmDir, err = vmconfig.EnsureDir(vmName, vmDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
