package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestShellWindowsTerminalRouting(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want []string
		env  map[string]string
	}{
		{name: "default", args: []string{"win"}, want: []string{"cmd.exe"}},
		{name: "explicit", args: []string{"-interactive", "win", "--", "powershell.exe", "-NoLogo"}, want: []string{"powershell.exe", "-NoLogo"}},
		{name: "environment", args: []string{"-interactive", "-env", "COVE_TEST=value", "win", "--", "cmd.exe"}, want: []string{"cmd.exe"}, env: map[string]string{"COVE_TEST": "value"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			oldVM, oldStdin := vmName, os.Stdin
			vmName = ""
			input, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			os.Stdin = input
			defer func() { vmName, os.Stdin = oldVM, oldStdin; input.Close() }()
			dir := filepath.Join(vmconfig.BaseDir(), "win.covevm")
			if err := os.MkdirAll(filepath.Join(dir, "qemu"), 0755); err != nil {
				t.Fatal(err)
			}
			for name, data := range map[string]string{"windows.qcow2": "disk", "qemu/metadata.json": `{"backend":"qemu-hvf"}`} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			dir, _ = vmconfig.ExistingPath("win")
			socket := GetControlSocketPathForVM(dir)
			if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			requests := make(chan map[string]any, 1)
			failures := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					failures <- err
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				var request map[string]any
				if err := json.NewDecoder(conn).Decode(&request); err != nil {
					failures <- err
					return
				}
				requests <- request
				_, err = fmt.Fprintln(conn, `{"success":true,"data":"{\"attached\":true,\"exec_id\":\"test\",\"stdin\":false}"}`)
				if err == nil {
					_, err = fmt.Fprintln(conn, `{"success":true,"data":"{\"done\":true,\"exitCode\":0}"}`)
				}
				failures <- err
			}()
			if err := shellCommand(tt.args); err != nil {
				t.Fatal(err)
			}
			if err := <-failures; err != nil {
				t.Fatal(err)
			}
			request := <-requests
			if request["type"] != "agent-exec-attach" || request["tty"] != true {
				t.Fatalf("request = %v", request)
			}
			args := request["args"].([]any)
			got := make([]string, len(args))
			for i := range args {
				got[i] = args[i].(string)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("args = %v, want %v", got, tt.want)
			}
			if _, ok := request["user"]; ok {
				t.Fatalf("unexpected user switching: %v", request)
			}
			for key, value := range tt.env {
				if request["env"].(map[string]any)[key] != value {
					t.Fatalf("env = %v", request["env"])
				}
			}
		})
	}
}

func TestShellWindowsExplicitCommandPreservesOneShot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVM := vmName
	vmName = ""
	defer func() { vmName = oldVM }()
	dir := filepath.Join(vmconfig.BaseDir(), "win.covevm")
	if err := os.MkdirAll(filepath.Join(dir, "qemu"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "metadata.json"), []byte(`{"backend":"qemu-hvf"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	err := shellCommand([]string{"-env", "A=B", "win", "--", "whoami"})
	if err == nil || !strings.Contains(err.Error(), "one-shot commands do not support") {
		t.Fatalf("error = %v", err)
	}
}
