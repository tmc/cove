package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/x/vzkit/disk"
	"github.com/tmc/cove/internal/covecli"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmrun"
)

var commandRegistry = []covecli.Spec{
	{Name: "action", Summary: "Preflight helpers for private GitHub Actions runner images", Dispatch: covecli.DispatchPreUI, Run: runActionCommand},
	{Name: "agent-sandbox", Summary: "Run a computer-use provider loop in a fresh VM fork", Dispatch: covecli.DispatchEarly, Run: runAgentSandboxCommand},
	{Name: "agent-upgrade", Aliases: []string{"upgrade-agent"}, Summary: "Live-upgrade vz-agent in a running VM", Dispatch: covecli.DispatchEarly, Run: runAgentUpgradeCommand},
	{Name: "bench", Summary: "Normalize benchmark evidence into reports and run metrics", Dispatch: covecli.DispatchEarly, Run: runBenchCommand},
	{Name: "build", Summary: "Chain vzscript steps into a cache-keyed VM image", Dispatch: covecli.DispatchEarly, Run: runBuildCommand},
	{Name: "clean", Summary: "Remove VM files", Dispatch: covecli.DispatchLate, Run: runCleanCommand},
	{Name: "clone", Summary: "Clone a VM", Dispatch: covecli.DispatchLate, Run: runCloneCommand},
	{Name: "commands", Summary: "Print machine-readable command inventory", Dispatch: covecli.DispatchEarly},
	{Name: "compact", Summary: "Zero guest free space for smaller pushes", Dispatch: covecli.DispatchEarly, Run: runCompactCommand},
	{Name: "config", Summary: "Export/import framework config snapshots", Dispatch: covecli.DispatchLate, Run: runVMSubcommand},
	{Name: "cp", Summary: "Copy files between host and guest", Dispatch: covecli.DispatchEarly, Run: runCpCommand},
	{Name: "ctl", Summary: "Control running VM via socket", Dispatch: covecli.DispatchEarly, Run: runCtlCommand},
	{Name: "daemon", Summary: "Manage the cove background coordinator", Dispatch: covecli.DispatchEarly, Run: runDaemonCommand},
	{Name: "diff", Summary: "Compare local image disk layer metadata", Dispatch: covecli.DispatchEarly, Run: runDiffCommand},
	{Name: "disk", Summary: "Resize stopped VM disk images", Dispatch: covecli.DispatchEarly, Run: runDiskCommand},
	{Name: "disk-detach", Summary: "Detach VM disk if stuck", Dispatch: covecli.DispatchEarly, Run: runDiskDetachCommand},
	{Name: "disk-snapshot", Summary: "Manage disk-level snapshots", Dispatch: covecli.DispatchLate, Run: runDiskSnapshotCommand},
	{Name: "export", Summary: "Export VM to tarball", Dispatch: covecli.DispatchLate, Run: runVMSubcommand},
	{Name: "exec", Summary: "Run a command in a running VM", Dispatch: covecli.DispatchEarly, Run: runExecCommand},
	{Name: "fleet", Summary: "Register and use remote cove hosts", Dispatch: covecli.DispatchEarly, Run: runFleetCommandSpec},
	{Name: "fork", Summary: "CoW-fork a VM with a fresh identity", Dispatch: covecli.DispatchEarly, Run: runForkCommand},
	{Name: "forward", Summary: "Forward host TCP to guest TCP", Dispatch: covecli.DispatchEarly, Run: runForwardCommand},
	{Name: "first-run", Summary: "Show first-run onboarding steps", Dispatch: covecli.DispatchEarly, Run: runFirstRunCommand},
	{Name: "gc", Summary: "Delete old disposable VM clones", Dispatch: covecli.DispatchEarly, Run: runGCCommand},
	{Name: "gui", Summary: "Control VM GUI window state", Dispatch: covecli.DispatchEarly, Run: runControlAliasCommand},
	{Name: "helper", Summary: "Manage the privileged helper", Dispatch: covecli.DispatchLate, Run: runHelperCommandSpec},
	{Name: "image", Summary: "Local VM image store", Dispatch: covecli.DispatchEarly, Run: runImageCommand},
	{Name: "import", Summary: "Import VM from tarball", Dispatch: covecli.DispatchLate, Run: runVMSubcommand},
	{Name: "inject", Summary: "Deprecated alias for provision", Dispatch: covecli.DispatchEarly, Run: runProvisionCommand},
	{Name: "inject-agent", Summary: "Deprecated alias for provision-agent", Dispatch: covecli.DispatchEarly, Run: runProvisionAgentCommand},
	{Name: "install", Summary: "Install OS", Dispatch: covecli.DispatchLate, Run: runInstallCommand},
	{Name: "list", Aliases: []string{"ls"}, Summary: "List available VMs and templates", Dispatch: covecli.DispatchLate, Run: runListCommand},
	{Name: "logs", Summary: "Show guest logs from a running VM", Dispatch: covecli.DispatchEarly, Run: runLogsCommand},
	{Name: "network", Summary: "Network configuration", Dispatch: covecli.DispatchLate, Run: runNetworkCommandSpec},
	{Name: "9p", Summary: "Serve read-only VM metadata over 9p", Dispatch: covecli.DispatchEarly, Run: runNinePCommand},
	{Name: "pin", Summary: "Pin an object so storage budget eviction skips it", Dispatch: covecli.DispatchEarly, Run: runPinCommand},
	{Name: "pins", Summary: "List pinned objects", Dispatch: covecli.DispatchEarly, Run: runPinsCommand},
	{Name: "pit", Summary: "Experimental point-in-time save, restore, run, and swap", Dispatch: covecli.DispatchLate, Run: runPITCommandSpec},
	{Name: "policy", Summary: "VM lifecycle policy", Dispatch: covecli.DispatchEarly, Run: runPolicyCommand},
	{Name: "provision", Summary: "Write provisioning files into VM disk", Dispatch: covecli.DispatchEarly, Run: runProvisionCommand},
	{Name: "provision-agent", Summary: "Provision vz-agent daemon", Dispatch: covecli.DispatchEarly, Run: runProvisionAgentCommand},
	{Name: "pull", Summary: "Validate an OCI pull plan", Dispatch: covecli.DispatchEarly, Run: runPullCommand},
	{Name: "push", Summary: "Plan a VM disk OCI push", Dispatch: covecli.DispatchEarly, Run: runPushCommand},
	{Name: "quota", Summary: "Show or set per-VM resource quotas", Dispatch: covecli.DispatchEarly, Run: runQuotaCommand},
	{Name: "recording", Aliases: []string{"recordings"}, Summary: "List and export run recording artifacts", Dispatch: covecli.DispatchEarly, Run: runRecordingCommand},
	{Name: "rename", Summary: "Rename a VM", Dispatch: covecli.DispatchLate, Run: runVMSubcommand},
	{Name: "rm", Aliases: []string{"remove", "destroy"}, Summary: "Delete a VM", Dispatch: covecli.DispatchLate, Run: runVMDeleteAliasCommand},
	{Name: "rosetta", Summary: "Rosetta 2 for Linux VMs", Dispatch: covecli.DispatchLate, Run: runRosettaCommandSpec},
	{Name: "run", Summary: "Run a VM", Dispatch: covecli.DispatchLate, Run: runRunCommand},
	{Name: "runner", Summary: "Generate hosted-runner workflow scaffolding", Dispatch: covecli.DispatchEarly, Run: runRunnerCommand},
	{Name: "runs", Summary: "Inspect local run metrics and artifacts", Dispatch: covecli.DispatchEarly, Run: runRunsCommand},
	{Name: "secret", Summary: "Debug secret resolver", Dispatch: covecli.DispatchEarly, Run: runSecretCommand},
	{Name: "security", Summary: "Inspect host-containment policy", Dispatch: covecli.DispatchEarly, Run: runSecurityCommand},
	{Name: "serve", Summary: "Multi-VM HTTP gateway", Dispatch: covecli.DispatchEarly, Run: runServeCommandSpec},
	{Name: "shared-folder", Aliases: []string{"shared-folders"}, Summary: "Manage shared folders", Dispatch: covecli.DispatchEarly, Run: runSharedFolderCommand},
	{Name: "shell", Summary: "Open a Docker-shaped exec session", Dispatch: covecli.DispatchEarly, Run: runShellCommand},
	{Name: "sip", Summary: "SIP management", Dispatch: covecli.DispatchEarly, Run: runSIPCommand},
	{Name: "snapshot", Summary: "Manage VM state snapshots", Dispatch: covecli.DispatchLate, Run: runSnapshotCommandSpec},
	{Name: "softreset", Summary: "Run destructive soft-reset probe matrix", Dispatch: covecli.DispatchEarly, Run: runSoftresetCommand},
	{Name: "status", Summary: "Show VM status", Dispatch: covecli.DispatchEarly, Run: runStatusCommand},
	{Name: "storage", Summary: "Inspect cove disk usage under ~/.vz/", Dispatch: covecli.DispatchEarly, Run: runStorageCommand},
	{Name: "store", Summary: "Manage the local OCI blob store", Dispatch: covecli.DispatchEarly, Run: runStoreCommand},
	{Name: "support", Summary: "Create support diagnostics bundles", Dispatch: covecli.DispatchEarly, Run: runSupportCommandSpec},
	{Name: "support-bundle", Summary: "Create a redacted support bundle", Dispatch: covecli.DispatchEarly, Run: runSupportBundleAliasCommand},
	{Name: "template", Summary: "Manage VM templates", Dispatch: covecli.DispatchLate, Run: runTemplateCommand},
	{Name: "trace", Aliases: []string{"traces"}, Summary: "Manage eslogger guest traces", Dispatch: covecli.DispatchEarly, Run: runTraceCommand},
	{Name: "uiscript", Summary: "Deprecated alias for vzscript", Dispatch: covecli.DispatchEarly, Run: runUIScriptCommand},
	{Name: "unpin", Summary: "Remove a storage pin", Dispatch: covecli.DispatchEarly, Run: runUnpinCommand},
	{Name: "up", Summary: "Install + provision + boot in one command", Dispatch: covecli.DispatchEarly, Run: runUpCommand},
	{Name: "verify", Aliases: []string{"doctor"}, Summary: "Verify provisioning files in VM disk", Dispatch: covecli.DispatchEarly, Run: runVerifyCommand},
	{Name: "version", Summary: "Print version information", Dispatch: covecli.DispatchEarly, Run: runVersionCommand},
	{Name: "vnc", Summary: "Inspect private VNC server state", Dispatch: covecli.DispatchEarly, Run: runControlAliasCommand},
	{Name: "vm", Summary: "Manage VMs", Dispatch: covecli.DispatchLate, Run: runVMCommandSpec},
	{Name: "vzscript", Summary: "Run guest-agent and UI automation scripts", Dispatch: covecli.DispatchEarly, Run: runVZScriptCommand},
}

func lookupCommand(name string) (*covecli.Spec, bool) {
	return covecli.Lookup(commandRegistry, name)
}

func commandNames() []string {
	return covecli.Names(commandRegistry)
}

func runRegisteredCommand(env commandEnv, spec *covecli.Spec, name string, args []string) int {
	env = env.WithDefaultIO()
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
	return commandError(env, handleBenchCommand(env, args))
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
func runControlAliasCommand(env commandEnv, name string, args []string) int {
	return commandError(env, ctlCommand(controlAliasArgs(name, args)))
}
func runDaemonCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, daemonCommand(env, args))
}
func runDiffCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, diffCommand(args))
}
func runFleetCommandSpec(env commandEnv, _ string, args []string) int {
	if len(args) == 0 {
		return commandUsageError(env, handleFleetCommand(env, args))
	}
	return commandError(env, handleFleetCommand(env, args))
}
func runForwardCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, forwardCommand(args))
}
func runGCCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleGCCommand(args))
}
func runImageCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleImageCommand(env, args))
}
func runLogsCommand(env commandEnv, _ string, args []string) int {
	err := logsCommand(env, args)
	if err != nil && strings.HasPrefix(err.Error(), "usage: cove logs ") {
		printLogsUsage(env.Stderr)
		return commandUsageError(env, err)
	}
	return commandError(env, err)
}
func runPolicyCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handlePolicyCommand(env, args))
}
func runPullCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handlePull(env, args))
}
func runPinCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handlePinCommand(env, args))
}
func runPinsCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handlePinsCommand(env, args))
}
func runPushCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handlePush(args))
}
func runRecordingCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleRecordingCommand(env, args))
}
func runQuotaCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleQuotaCommand(env, args))
}
func runUnpinCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleUnpinCommand(env, args))
}
func runRunsCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleRunsCommand(env, args))
}
func runSecretCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleSecretCommand(env, args))
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
	return commandError(env, handleStoreCommand(env, args))
}
func runFirstRunCommand(env commandEnv, _ string, _ []string) int {
	printFirstRunUsage(env.Stdout)
	return 0
}
func runSupportCommandSpec(env commandEnv, _ string, args []string) int {
	if len(args) == 0 {
		return commandUsageError(env, handleSupportCommand(env, args))
	}
	return commandError(env, handleSupportCommand(env, args))
}
func runSupportBundleAliasCommand(env commandEnv, _ string, args []string) int {
	fmt.Fprintln(env.Stderr, "note: support-bundle is an alias for 'cove support bundle'")
	return commandError(env, runSupportBundle(env, args))
}
func runUpCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleUp(env, args))
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
func runRunCommand(env commandEnv, _ string, _ []string) int {
	return commandError(env, handleRun(env))
}
func runSnapshotCommandSpec(env commandEnv, _ string, args []string) int {
	return commandError(env, handleSnapshotCommand(env, args))
}
func runStatusCommand(env commandEnv, _ string, args []string) int {
	err := statusCommand(env, args...)
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
	return commandError(env, handleTraceCommand(env, args))
}
func runVMCommandSpec(_ commandEnv, _ string, args []string) int {
	handleVMCommand(args)
	return 0
}

func runVersionCommand(env commandEnv, _ string, _ []string) int {
	covecli.PrintVersion(env.Stdout, versionInfo())
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
		err = installWindowsVM(env.Stderr)
	} else if linuxMode {
		err = handleLinuxInstall(env.Stderr)
	} else {
		opts := currentRuntimeOptions()
		err = installMacOSLikeVZWithProvision(context.Background(), env.Stderr, macOSInstallProvisionFromRuntimeOptions(opts), opts.IPSWPath, opts.vmrunRunConfig(vmrun.GuestMacOS), opts.vmrunHostConfig())
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

func runLegacyInstallFlag(env commandEnv) int {
	fmt.Fprintf(env.Stderr, "warning: -install flag is deprecated, use 'cove install' command instead\n")
	return runInstallCommand(env, "install", nil)
}

func runLegacyRunFlag(env commandEnv) int {
	fmt.Fprintf(env.Stderr, "warning: -run flag is deprecated, use 'cove run' command instead\n")
	return commandError(env, handleRun(env))
}

func rerunVMDirForPostCommand(env commandEnv, cmd string, args []string) int {
	cmdArgs := append([]string{cmd}, args...)
	if vmName == "" || subcommandSkipsVMDir(cmdArgs) {
		if cmd == "run" && vmName != "" && !argsContainFlag(args, "fork-from") {
			dir, err := requireExistingRunVMDir(vmName)
			if err != nil {
				fmt.Fprintf(env.Stderr, "error: %v\n", err)
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
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
