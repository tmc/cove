package main

import "testing"

func TestValidateNewVMOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    newVMOptions
		wantErr bool
	}{
		{
			name: "install only",
			opts: newVMOptions{
				Name: "macos",
			},
		},
		{
			name: "provisioned admin user",
			opts: newVMOptions{
				Name:              "macos",
				ProvisionUser:     "builder",
				ProvisionPassword: "secret123",
				ProvisionAdmin:    true,
			},
		},
		{
			name: "missing password",
			opts: newVMOptions{
				Name:          "macos",
				ProvisionUser: "builder",
			},
			wantErr: true,
		},
		{
			name: "missing username",
			opts: newVMOptions{
				Name:              "macos",
				ProvisionPassword: "secret123",
			},
			wantErr: true,
		},
		{
			name: "invalid username",
			opts: newVMOptions{
				Name:              "macos",
				ProvisionUser:     "root",
				ProvisionPassword: "secret123",
			},
			wantErr: true,
		},
		{
			name: "invalid vm name",
			opts: newVMOptions{
				Name: "bad/name",
			},
			wantErr: true,
		},
		{
			name: "valid post-install recipe",
			opts: newVMOptions{
				Name:               "macos",
				PostInstallRecipes: "homebrew",
			},
		},
		{
			name: "invalid post-install recipe",
			opts: newVMOptions{
				Name:               "macos",
				PostInstallRecipes: "does-not-exist",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNewVMOptions(tt.opts)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateNewVMOptions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunButtonTitle(t *testing.T) {
	tests := []struct {
		name string
		vm   *VMInfo
		want string
	}{
		{
			name: "no selection",
			want: "Run",
		},
		{
			name: "stopped vm",
			vm: &VMInfo{
				State: "stopped",
			},
			want: "Run",
		},
		{
			name: "suspended vm",
			vm: &VMInfo{
				State: "suspended",
			},
			want: "Resume",
		},
		{
			name: "running vm",
			vm: &VMInfo{
				State: "running",
			},
			want: "Running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runButtonTitle(tt.vm); got != tt.want {
				t.Fatalf("runButtonTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectorStateText(t *testing.T) {
	tests := []struct {
		name string
		vm   VMInfo
		want string
	}{
		{
			name: "running",
			vm:   VMInfo{State: "running"},
			want: "Running",
		},
		{
			name: "suspended",
			vm:   VMInfo{State: "suspended"},
			want: "Suspended",
		},
		{
			name: "stopped",
			vm:   VMInfo{State: "stopped"},
			want: "Stopped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectorStateText(tt.vm); got != tt.want {
				t.Fatalf("selectorStateText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectorVMCountText(t *testing.T) {
	tests := []struct {
		name  string
		count int
		want  string
	}{
		{
			name:  "zero",
			count: 0,
			want:  "0 VMs",
		},
		{
			name:  "one",
			count: 1,
			want:  "1 VM",
		},
		{
			name:  "many",
			count: 5,
			want:  "5 VMs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectorVMCountText(tt.count); got != tt.want {
				t.Fatalf("selectorVMCountText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectorRowTitle(t *testing.T) {
	tests := []struct {
		name     string
		vm       VMInfo
		activeVM string
		want     string
	}{
		{
			name: "inactive",
			vm:   VMInfo{Name: "default"},
			want: "default",
		},
		{
			name:     "active",
			vm:       VMInfo{Name: "default"},
			activeVM: "default",
			want:     "default *",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectorRowTitle(tt.vm, tt.activeVM); got != tt.want {
				t.Fatalf("selectorRowTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCanOpenVZScriptRunner(t *testing.T) {
	tests := []struct {
		name string
		vm   *VMInfo
		want bool
	}{
		{
			name: "no selection",
			want: false,
		},
		{
			name: "stopped vm",
			vm:   &VMInfo{State: "stopped"},
			want: false,
		},
		{
			name: "suspended vm",
			vm:   &VMInfo{State: "suspended"},
			want: false,
		},
		{
			name: "running vm",
			vm:   &VMInfo{State: "running"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canOpenVZScriptRunner(tt.vm); got != tt.want {
				t.Fatalf("canOpenVZScriptRunner() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInitialSelectionRow(t *testing.T) {
	tests := []struct {
		name     string
		vms      []VMInfo
		activeVM string
		want     int
	}{
		{
			name: "empty",
			want: -1,
		},
		{
			name: "first row fallback",
			vms: []VMInfo{
				{Name: "default"},
				{Name: "macos-2"},
			},
			want: 0,
		},
		{
			name: "active vm selected",
			vms: []VMInfo{
				{Name: "default"},
				{Name: "macos-2"},
			},
			activeVM: "macos-2",
			want:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &VMSelector{
				vms:      tt.vms,
				activeVM: tt.activeVM,
			}
			if got := s.initialSelectionRow(); got != tt.want {
				t.Fatalf("initialSelectionRow() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSelectorAutoresizingMaskConstants(t *testing.T) {
	if selectorViewMinX != 1 {
		t.Fatalf("selectorViewMinX = %d, want 1", selectorViewMinX)
	}
	if selectorViewWidth != 2 {
		t.Fatalf("selectorViewWidth = %d, want 2", selectorViewWidth)
	}
	if selectorViewHeight != 16 {
		t.Fatalf("selectorViewHeight = %d, want 16", selectorViewHeight)
	}
}
