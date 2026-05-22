package cove

import "embed"

//go:embed vzscripts/*.vzscript vzscripts/windows/*.vzscript
var VZScripts embed.FS

//go:embed templates/vz-provision.sh.tmpl
var ProvisionScriptTmpl string

//go:embed templates/vz-autologin.sh.tmpl
var AutoLoginScriptTmpl string

//go:embed templates/com.tmc.cove.provision.plist
var ProvisionLaunchDaemonPlist string

//go:embed templates/com.tmc.cove.autologin.plist
var AutoLoginLaunchDaemonPlist string

//go:embed templates/com.tmc.cove.vz-agent.plist
var AgentLaunchDaemonPlist string

//go:embed templates/com.tmc.cove.vz-agent-user.plist
var AgentLaunchAgentPlist string

//go:embed templates/com.cove.daemon.plist.tmpl
var CovedPlistTemplate string
