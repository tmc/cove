package main

import (
	"bytes"
	"text/template"

	coveassets "github.com/tmc/cove"
)

var (
	provisionLaunchDaemonPlist = coveassets.ProvisionLaunchDaemonPlist
	autoLoginLaunchDaemonPlist = coveassets.AutoLoginLaunchDaemonPlist
	agentLaunchDaemonPlist     = coveassets.AgentLaunchDaemonPlist
	agentLaunchAgentPlist      = coveassets.AgentLaunchAgentPlist
)

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

var provisionScriptTemplate = template.Must(template.New("vz-provision.sh").Parse(coveassets.ProvisionScriptTmpl))
var autoLoginScriptTemplate = template.Must(template.New("vz-autologin.sh").Parse(coveassets.AutoLoginScriptTmpl))

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
