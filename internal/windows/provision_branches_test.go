package windows

import (
	"strings"
	"testing"
)

func TestGenerateAutounattendXMLOmitsOOBEAndAutoLogonWhenDisabled(t *testing.T) {
	// Setting LocalAdmin alone defeats the "all-zero" defaulting in
	// withDefaults, so OOBEBypass and AutoLogon stay false. The rendered
	// XML must then omit the <OOBE> and <AutoLogon> blocks.
	xml := GenerateAutounattendXML(ProvisionConfig{
		Username:   "alice",
		Password:   "hunter2",
		Hostname:   "win-host",
		LocalAdmin: true,
	})

	tests := []struct {
		name    string
		marker  string
		present bool
	}{
		{name: "oobe block absent", marker: "<OOBE>", present: false},
		{name: "hide eula absent", marker: "<HideEULAPage>", present: false},
		{name: "hide online accounts absent", marker: "<HideOnlineAccountScreens>", present: false},
		{name: "autologon block absent", marker: "<AutoLogon>", present: false},
		{name: "logon count absent", marker: "<LogonCount>", present: false},
		// Local admin still wired up to confirm the rest of the XML rendered.
		{name: "local admin group present", marker: "<Group>Administrators</Group>", present: true},
		{name: "username preserved", marker: "alice", present: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			has := strings.Contains(xml, tt.marker)
			if has != tt.present {
				t.Fatalf("Contains(%q) = %v, want %v", tt.marker, has, tt.present)
			}
		})
	}
}

func TestGenerateAutounattendXMLOOBEOnlyOmitsAutoLogon(t *testing.T) {
	// Only OOBEBypass set: <OOBE> block present, <AutoLogon> absent.
	xml := GenerateAutounattendXML(ProvisionConfig{OOBEBypass: true})
	if !strings.Contains(xml, "<HideOnlineAccountScreens>true</HideOnlineAccountScreens>") {
		t.Fatal("OOBE block missing despite OOBEBypass=true")
	}
	if strings.Contains(xml, "<AutoLogon>") {
		t.Fatal("AutoLogon block present despite AutoLogon=false")
	}
}

func TestGenerateAutounattendXMLAutoLogonOnlyOmitsOOBE(t *testing.T) {
	// Only AutoLogon set: <AutoLogon> block present, <OOBE> absent.
	xml := GenerateAutounattendXML(ProvisionConfig{AutoLogon: true, Username: "u", Password: "p"})
	if !strings.Contains(xml, "<AutoLogon>") {
		t.Fatal("AutoLogon block missing despite AutoLogon=true")
	}
	if strings.Contains(xml, "<HideEULAPage>") {
		t.Fatal("OOBE block present despite OOBEBypass=false")
	}
}
