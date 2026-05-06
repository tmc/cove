package fleet

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestTransferImageStreamsSourceToDestination(t *testing.T) {
	runner := &recordCommandRunner{payload: "tarball"}
	src := Remote{Host: "src.local"}
	dst := Remote{Host: "dst.local"}
	if err := TransferImage(context.Background(), "agentkit/base:latest", src, dst, runner); err != nil {
		t.Fatalf("TransferImage: %v", err)
	}
	got := runner.callsSnapshot()
	if len(got) != 2 {
		t.Fatalf("calls = %d, want 2: %#v", len(got), got)
	}
	if !runner.sawCall(src, []string{"image", "push", "agentkit/base:latest", "-"}) {
		t.Fatalf("missing source push call: %#v", got)
	}
	if !runner.sawCall(dst, []string{"image", "load", "-"}) {
		t.Fatalf("missing destination load call: %#v", got)
	}
	if runner.loaded != "tarball" {
		t.Fatalf("loaded payload = %q, want tarball", runner.loaded)
	}
}

func TestTransferImageReportsEndpointErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		errs map[string]error
		want string
	}{
		{name: "source", errs: map[string]error{"src.local": errors.New("boom")}, want: "source image push"},
		{name: "destination", errs: map[string]error{"dst.local": errors.New("boom")}, want: "destination image load"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &recordCommandRunner{payload: "tarball", errs: tc.errs}
			err := TransferImage(context.Background(), "base:latest", Remote{Host: "src.local"}, Remote{Host: "dst.local"}, runner)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("TransferImage error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTransferImageRejectsInvalidInput(t *testing.T) {
	if err := TransferImage(context.Background(), "", Remote{}, Remote{}, &recordCommandRunner{}); err == nil {
		t.Fatal("TransferImage empty ref succeeded")
	}
	if err := TransferImage(context.Background(), "base:latest", Remote{}, Remote{}, nil); err == nil {
		t.Fatal("TransferImage nil runner succeeded")
	}
}

type commandCall struct {
	remote Remote
	args   []string
}

type recordCommandRunner struct {
	mu      sync.Mutex
	payload string
	loaded  string
	errs    map[string]error
	calls   []commandCall
}

func (r *recordCommandRunner) Run(ctx context.Context, remote Remote, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	r.mu.Lock()
	r.calls = append(r.calls, commandCall{remote: remote, args: append([]string(nil), args...)})
	err := error(nil)
	if r.errs != nil {
		err = r.errs[remote.Host]
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if len(args) >= 2 && args[0] == "image" && args[1] == "push" {
		_, err := io.Copy(stdout, strings.NewReader(r.payload))
		return err
	}
	if len(args) >= 2 && args[0] == "image" && args[1] == "load" {
		var b bytes.Buffer
		if _, err := io.Copy(&b, stdin); err != nil {
			return err
		}
		r.mu.Lock()
		r.loaded = b.String()
		r.mu.Unlock()
		return nil
	}
	return nil
}

func (r *recordCommandRunner) callsSnapshot() []commandCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]commandCall(nil), r.calls...)
}

func (r *recordCommandRunner) sawCall(remote Remote, args []string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.calls {
		if call.remote.Host == remote.Host && reflect.DeepEqual(call.args, args) {
			return true
		}
	}
	return false
}
