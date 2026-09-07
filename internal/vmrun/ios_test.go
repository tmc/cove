package vmrun

import "testing"

func TestIOSPlan(t *testing.T) {
	rc := validRunConfig(GuestIOS)
	p, err := Plan(rc, validHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	if p.OS != GuestIOS || !p.Audio.HostInput || !p.Audio.HostOutput || len(p.Storage) != 1 || p.Rosetta {
		t.Fatalf("unexpected ios plan: %+v", p)
	}
	if GuestIOS.String() != "ios" {
		t.Fatal("missing ios label")
	}
}

func TestIOSConflictingOptions(t *testing.T) {
	tests := []struct {
		name string
		set  func(*RunConfig)
	}{
		{"mac installer", func(c *RunConfig) { c.IPSWPath = "phone.ipsw" }},
		{"generic install", func(c *RunConfig) { c.InstallVM = true }},
		{"iso", func(c *RunConfig) { c.ISOPath = "install.iso" }},
		{"kernel", func(c *RunConfig) { c.KernelPath = "kernel" }},
		{"recovery and dfu", func(c *RunConfig) { c.RecoveryMode = true; c.ForceDFU = true }},
		{"rosetta", func(c *RunConfig) { c.EnableRosetta = true }},
		{"virtio clipboard", func(c *RunConfig) { c.EnableClipboard = true }},
		{"virtio shared directory", func(c *RunConfig) { c.Volumes = []VolumeMount{{HostPath: "/tmp"}} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := validRunConfig(GuestIOS)
			tt.set(&rc)
			if err := rc.Validate(); err == nil {
				t.Fatal("accepted incompatible ios option")
			}
		})
	}
}
