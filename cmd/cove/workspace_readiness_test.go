package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceReadinessBudgetPlan(t *testing.T) {
	for _, tt := range []struct {
		name  string
		flags []string
		want  time.Duration
	}{
		{"default", nil, 2 * time.Minute},
		{"explicit", []string{"-readiness-timeout", "5m"}, 5 * time.Minute},
		{"maximum", []string{"-readiness-timeout", "30m"}, 30 * time.Minute},
	} {
		t.Run(tt.name, func(t *testing.T) {
			base := workspaceTestOptions(t)
			args := []string{"-vm", base.VM, "-source", base.Source, "-output", base.Output, "-timeout", "90s"}
			o, _, err := parseWorkspaceOptions(append(args, tt.flags...), io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			p, err := planGoWorkspace(o)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := writeWorkspacePlan(&buf, p, true); err != nil {
				t.Fatal(err)
			}
			var decoded workspacePlan
			if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.ReadinessTimeoutSeconds != tt.want.Seconds() || decoded.TaskTimeoutSeconds != 90 {
				t.Fatalf("budgets: readiness %v, task %v", decoded.ReadinessTimeoutSeconds, decoded.TaskTimeoutSeconds)
			}
			buf.Reset()
			if err := writeWorkspacePlan(&buf, p, false); err != nil || !strings.Contains(buf.String(), "Readiness timeout: "+tt.want.String()) {
				t.Fatalf("human plan %q, error %v", buf.String(), err)
			}
		})
	}
}

func TestWorkspaceReadinessBudgetRefusals(t *testing.T) {
	for _, duration := range []time.Duration{0, -time.Second, 30*time.Minute + time.Nanosecond} {
		o := workspaceTestOptions(t)
		o.ReadinessTimeout = duration
		_, err := planGoWorkspace(o)
		if err == nil || !strings.Contains(err.Error(), "readiness timeout") {
			t.Fatalf("budget %s: %v", duration, err)
		}
	}
}

func TestWorkspaceReadinessBudgetStopsAdmission(t *testing.T) {
	for _, tt := range []struct {
		name           string
		parentCancel   bool
		parentDeadline bool
		lateSuccess    bool
		want           error
	}{
		{"expiry", false, false, false, context.DeadlineExceeded},
		{"late-success", false, false, true, context.DeadlineExceeded},
		{"parent-cancel", true, false, false, context.Canceled},
		{"parent-deadline", false, true, false, context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := workspaceTestOptions(t)
			o.ReadinessTimeout = 20 * time.Millisecond
			if tt.parentCancel || tt.parentDeadline {
				o.ReadinessTimeout = 5 * time.Minute
			}
			p, err := planGoWorkspace(o)
			if err != nil {
				t.Fatal(err)
			}
			var steps []string
			d := workspaceTestDeps(t, &steps)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.parentDeadline {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, time.Second)
				defer deadlineCancel()
			}
			d.Ready = func(readyCtx context.Context, _ workspacePlan, _ string) ([]byte, error) {
				deadline, ok := readyCtx.Deadline()
				if !ok || time.Until(deadline) > o.ReadinessTimeout {
					t.Fatal("readiness context lacks selected budget")
				}
				if tt.parentDeadline {
					parentDeadline, _ := ctx.Deadline()
					if !deadline.Equal(parentDeadline) {
						t.Fatal("readiness did not inherit earlier parent deadline")
					}
				}
				if tt.parentCancel {
					cancel()
				}
				<-readyCtx.Done()
				if tt.lateSuccess {
					return []byte(`{"root":true,"user":true}`), nil
				}
				return nil, readyCtx.Err()
			}
			receipt, err := openGoWorkspace(ctx, o, p, d, commandEnv{Stdout: io.Discard, Stderr: io.Discard})
			if !errors.Is(err, tt.want) || receipt.FailedStep != "readiness" || receipt.Disposition != "retained" {
				t.Fatalf("receipt %+v, error %v", receipt, err)
			}
			for _, step := range steps {
				if step == "task" || step == "go" || step == "mount" {
					t.Fatalf("admission proceeded to %s", step)
				}
			}
		})
	}
}
