package main

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed templates/vz-provision.sh.tmpl
var provisionScriptTmpl string

//go:embed templates/vz-autologin.sh.tmpl
var autoLoginScriptTmpl string

//go:embed templates/com.github.tmc.vz-macos.provision.plist
var provisionLaunchDaemonPlist string

//go:embed templates/com.github.tmc.vz-macos.autologin.plist
var autoLoginLaunchDaemonPlist string

//go:embed templates/com.github.tmc.vz-macos.vz-agent.plist
var agentLaunchDaemonPlistEmbed string

//go:embed templates/com.github.tmc.vz-macos.vz-agent-user.plist
var agentLaunchAgentPlistEmbed string

// provisionTemplateData holds the values substituted into vz-provision.sh.tmpl.
type provisionTemplateData struct {
	Username          string // shell-escaped
	Password          string // shell-escaped
	Fullname          string // shell-escaped
	Admin             string // "true" or "false"
	BootstrapRecovery string // "true" or "false"
	InstallXcodeCLI   string // "true" or "false"
	EnableSSHD        string // "true" or "false"
}

// autoLoginTemplateData holds the values substituted into vz-autologin.sh.tmpl.
type autoLoginTemplateData struct {
	Username string // shell-escaped
}

var provisionScriptTemplate = template.Must(template.New("vz-provision.sh").Parse(provisionScriptTmpl))
var autoLoginScriptTemplate = template.Must(template.New("vz-autologin.sh").Parse(autoLoginScriptTmpl))

// renderProvisionScript renders the provision script template with the given data.
func renderProvisionScript(data provisionTemplateData) (string, error) {
	var buf bytes.Buffer
	if err := provisionScriptTemplate.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// renderAutoLoginScript renders the autologin repair script template.
func renderAutoLoginScript(data autoLoginTemplateData) (string, error) {
	var buf bytes.Buffer
	if err := autoLoginScriptTemplate.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
