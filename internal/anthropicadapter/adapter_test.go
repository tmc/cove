package anthropicadapter

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestAdapterRunsScreenshotClickScreenshotLoop(t *testing.T) {
	client := &fakeAnthropic{responses: []MessageResponse{
		{Content: []Block{{Type: "tool_use", ID: "u1", Name: "computer", Input: map[string]any{"action": "screenshot"}}}},
		{Content: []Block{{Type: "tool_use", ID: "u2", Name: "computer", Input: map[string]any{"action": "left_click", "coordinate": []any{float64(7), float64(9)}}}}},
		{Content: []Block{{Type: "tool_use", ID: "u3", Name: "computer", Input: map[string]any{"action": "screenshot"}}}},
		{Content: []Block{{Type: "text", Text: "done"}}},
	}}
	control := &fakeControl{}
	var log bytes.Buffer
	transcript, err := (&Adapter{
		Client:   client,
		Control:  control,
		VMID:     "vm",
		Model:    "claude-opus-4-7",
		MaxSteps: 5,
		Log:      &log,
	}).Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if got := control.screenshots; got != 2 {
		t.Fatalf("screenshots = %d, want 2", got)
	}
	if got := control.clicks; !reflect.DeepEqual(got, [][2]int{{7, 9}}) {
		t.Fatalf("clicks = %v", got)
	}
	if len(client.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(client.requests))
	}
	if client.requests[0].Betas[0] != BetaHeader {
		t.Fatalf("beta = %q", client.requests[0].Betas[0])
	}
	if client.requests[0].Tools[0].Type != ComputerTool {
		t.Fatalf("tool = %q", client.requests[0].Tools[0].Type)
	}
	if !strings.Contains(log.String(), `"type":"tool_result"`) {
		t.Fatalf("log missing tool result: %s", log.String())
	}
	if got := transcript[len(transcript)-1].Text; got != "done" {
		t.Fatalf("final transcript text = %q", got)
	}
}

func TestAdapterReturnsToolErrorsToModel(t *testing.T) {
	client := &fakeAnthropic{responses: []MessageResponse{
		{Content: []Block{{Type: "tool_use", ID: "u1", Name: "computer", Input: map[string]any{"action": "bogus"}}}},
		{Content: []Block{{Type: "text", Text: "recovered"}}},
	}}
	_, err := (&Adapter{Client: client, Control: &fakeControl{}, MaxSteps: 3}).Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	result := client.requests[1].Messages[len(client.requests[1].Messages)-1].Content[0]
	if !result.IsError {
		t.Fatalf("tool result IsError = false")
	}
	if !strings.Contains(result.Content.(string), "unsupported computer action") {
		t.Fatalf("tool result content = %v", result.Content)
	}
}

func TestAdapterDispatchContentActions(t *testing.T) {
	tests := []struct {
		name    string
		input   map[string]any
		want    string
		wantErr string
	}{
		{name: "type", input: map[string]any{"action": "type", "text": "hi"}, want: "typed"},
		{name: "key", input: map[string]any{"action": "key", "key": "Return"}, want: "pressed"},
		{name: "scroll y", input: map[string]any{"action": "scroll", "scroll_y": float64(3)}, want: "scrolled"},
		{name: "scroll amount fallback", input: map[string]any{"action": "scroll", "scroll_amount": float64(2)}, want: "scrolled"},
		{name: "cursor", input: map[string]any{"action": "cursor_position"}, want: "cursor_position: 0,0"},
		{name: "unsupported action", input: map[string]any{"action": "wat"}, wantErr: "unsupported computer action"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adapter{Control: &fakeControl{}}
			got, err := a.dispatchContent(context.Background(), Block{Name: "computer", Input: tt.input})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("dispatchContent: %v", err)
			}
			if s, _ := got.(string); s != tt.want {
				t.Fatalf("got = %v, want %q", got, tt.want)
			}
		})
	}
}

func TestAdapterDispatchContentRejectsNonComputerTool(t *testing.T) {
	a := &Adapter{Control: &fakeControl{}}
	_, err := a.dispatchContent(context.Background(), Block{Name: "shell"})
	if err == nil || !strings.Contains(err.Error(), `unsupported tool "shell"`) {
		t.Fatalf("err = %v, want unsupported tool", err)
	}
}

type fakeAnthropic struct {
	requests  []MessageRequest
	responses []MessageResponse
}

func (f *fakeAnthropic) CreateMessage(_ context.Context, req MessageRequest) (MessageResponse, error) {
	f.requests = append(f.requests, req)
	out := f.responses[0]
	f.responses = f.responses[1:]
	return out, nil
}

type fakeControl struct {
	screenshots int
	clicks      [][2]int
}

func (f *fakeControl) ScreenSize(context.Context) (int, int, error) { return 1024, 768, nil }
func (f *fakeControl) ScreenshotPNG(context.Context) ([]byte, error) {
	f.screenshots++
	return []byte("png"), nil
}
func (f *fakeControl) Click(_ context.Context, x, y int) error {
	f.clicks = append(f.clicks, [2]int{x, y})
	return nil
}
func (f *fakeControl) TypeText(context.Context, string) error           { return nil }
func (f *fakeControl) Key(context.Context, uint16, uint) error          { return nil }
func (f *fakeControl) Scroll(context.Context, int) error                { return nil }
func (f *fakeControl) CursorPosition(context.Context) (int, int, error) { return 0, 0, nil }
