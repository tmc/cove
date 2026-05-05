// Package anthropicadapter runs Anthropic computer-use loops against cove.
package anthropicadapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	BetaHeader   = "computer-use-2025-11-24"
	ComputerTool = "computer_20251124"
)

type Adapter struct {
	Client    AnthropicClient
	Control   ControlClient
	VMID      string
	Model     string
	MaxSteps  int
	MaxTokens int
	Log       io.Writer
}

type AnthropicClient interface {
	CreateMessage(context.Context, MessageRequest) (MessageResponse, error)
}

type ControlClient interface {
	ScreenSize(context.Context) (int, int, error)
	ScreenshotPNG(context.Context) ([]byte, error)
	Click(context.Context, int, int) error
	TypeText(context.Context, string) error
	Key(context.Context, uint16, uint) error
	Scroll(context.Context, int) error
	CursorPosition(context.Context) (int, int, error)
}

type MessageRequest struct {
	Model     string
	MaxTokens int
	Betas     []string
	Tools     []Tool
	Messages  []Message
}

type MessageResponse struct {
	Content []Block
}

type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

type Block struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	Content   any            `json:"content,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

type Tool struct {
	Type            string `json:"type"`
	Name            string `json:"name"`
	DisplayWidthPx  int    `json:"display_width_px,omitempty"`
	DisplayHeightPx int    `json:"display_height_px,omitempty"`
	DisplayNumber   int    `json:"display_number,omitempty"`
}

type TranscriptEntry struct {
	Time      time.Time `json:"time"`
	Type      string    `json:"type"`
	VMID      string    `json:"vm_id,omitempty"`
	ToolUseID string    `json:"tool_use_id,omitempty"`
	Name      string    `json:"name,omitempty"`
	Action    string    `json:"action,omitempty"`
	Text      string    `json:"text,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type SDKClient struct {
	client anthropic.Client
}

func NewSDKClient(opts ...option.RequestOption) *SDKClient {
	return &SDKClient{client: anthropic.NewClient(opts...)}
}

func (c *SDKClient) CreateMessage(ctx context.Context, req MessageRequest) (MessageResponse, error) {
	var out struct {
		Content []Block `json:"content"`
	}
	body := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"messages":   req.Messages,
		"tools":      req.Tools,
	}
	if err := c.client.Post(ctx, "/v1/messages", body, &out,
		option.WithHeader("anthropic-beta", strings.Join(req.Betas, ","))); err != nil {
		return MessageResponse{}, err
	}
	return MessageResponse{Content: out.Content}, nil
}

func (a *Adapter) Run(ctx context.Context, prompt string) ([]TranscriptEntry, error) {
	if a.Client == nil {
		a.Client = NewSDKClient()
	}
	if a.Control == nil {
		return nil, errors.New("anthropic adapter: control client required")
	}
	model := strings.TrimSpace(a.Model)
	if model == "" {
		model = "claude-opus-4-7"
	}
	maxSteps := a.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 20
	}
	maxTokens := a.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	width, height, err := a.Control.ScreenSize(ctx)
	if err != nil || width <= 0 || height <= 0 {
		width, height = 1024, 768
	}
	messages := []Message{{Role: "user", Content: []Block{{Type: "text", Text: prompt}}}}
	var transcript []TranscriptEntry
	a.record(&transcript, TranscriptEntry{Type: "request", VMID: a.VMID, Text: prompt})
	for step := 1; step <= maxSteps; step++ {
		resp, err := a.Client.CreateMessage(ctx, MessageRequest{
			Model:     model,
			MaxTokens: maxTokens,
			Betas:     []string{BetaHeader},
			Tools: []Tool{{
				Type:            ComputerTool,
				Name:            "computer",
				DisplayWidthPx:  width,
				DisplayHeightPx: height,
				DisplayNumber:   1,
			}},
			Messages: messages,
		})
		if err != nil {
			return transcript, fmt.Errorf("anthropic adapter: messages create: %w", err)
		}
		messages = append(messages, Message{Role: "assistant", Content: resp.Content})
		toolUses := toolUseBlocks(resp.Content)
		if len(toolUses) == 0 {
			final := finalText(resp.Content)
			a.record(&transcript, TranscriptEntry{Type: "response", VMID: a.VMID, Text: final})
			return transcript, nil
		}
		var results []Block
		for _, toolUse := range toolUses {
			result := a.dispatch(ctx, toolUse)
			results = append(results, result)
			entry := TranscriptEntry{
				Type:      "tool_result",
				VMID:      a.VMID,
				ToolUseID: toolUse.ID,
				Name:      toolUse.Name,
				Action:    actionName(toolUse),
			}
			if result.IsError {
				entry.Error = fmt.Sprint(result.Content)
			}
			a.record(&transcript, entry)
		}
		messages = append(messages, Message{Role: "user", Content: results})
	}
	return transcript, fmt.Errorf("anthropic adapter: max steps exceeded: %d", maxSteps)
}

func (a *Adapter) dispatch(ctx context.Context, toolUse Block) Block {
	result := Block{Type: "tool_result", ToolUseID: toolUse.ID}
	content, err := a.dispatchContent(ctx, toolUse)
	if err != nil {
		result.IsError = true
		result.Content = err.Error()
		return result
	}
	result.Content = content
	return result
}

func (a *Adapter) dispatchContent(ctx context.Context, toolUse Block) (any, error) {
	if toolUse.Name != "computer" {
		return nil, fmt.Errorf("unsupported tool %q", toolUse.Name)
	}
	action := actionName(toolUse)
	switch action {
	case "screenshot":
		png, err := a.Control.ScreenshotPNG(ctx)
		if err != nil {
			return nil, err
		}
		return []map[string]any{{
			"type": "image",
			"source": map[string]string{
				"type":       "base64",
				"media_type": "image/png",
				"data":       base64.StdEncoding.EncodeToString(png),
			},
		}}, nil
	case "left_click", "click":
		x, y, err := coordinates(toolUse.Input)
		if err != nil {
			return nil, err
		}
		return "clicked", a.Control.Click(ctx, x, y)
	case "type":
		text, _ := toolUse.Input["text"].(string)
		return "typed", a.Control.TypeText(ctx, text)
	case "key", "keypress":
		code, mods, err := keyCode(toolUse.Input)
		if err != nil {
			return nil, err
		}
		return "pressed", a.Control.Key(ctx, code, mods)
	case "scroll":
		delta := intNumber(toolUse.Input["scroll_y"])
		if delta == 0 {
			delta = intNumber(toolUse.Input["scroll_amount"])
		}
		return "scrolled", a.Control.Scroll(ctx, delta)
	case "cursor_position":
		x, y, err := a.Control.CursorPosition(ctx)
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("cursor_position: %d,%d", x, y), nil
	default:
		return nil, fmt.Errorf("unsupported computer action %q", action)
	}
}

func (a *Adapter) record(transcript *[]TranscriptEntry, entry TranscriptEntry) {
	entry.Time = time.Now().UTC()
	*transcript = append(*transcript, entry)
	if a.Log == nil {
		return
	}
	data, err := json.Marshal(entry)
	if err == nil {
		fmt.Fprintln(a.Log, string(data))
	}
}

func toolUseBlocks(blocks []Block) []Block {
	var out []Block
	for _, b := range blocks {
		if b.Type == "tool_use" {
			out = append(out, b)
		}
	}
	return out
}

func finalText(blocks []Block) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func actionName(block Block) string {
	action, _ := block.Input["action"].(string)
	return action
}

func coordinates(input map[string]any) (int, int, error) {
	if raw, ok := input["coordinate"]; ok {
		list, ok := raw.([]any)
		if !ok || len(list) != 2 {
			return 0, 0, errors.New("coordinate must be [x, y]")
		}
		return intNumber(list[0]), intNumber(list[1]), nil
	}
	return intNumber(input["x"]), intNumber(input["y"]), nil
}

func intNumber(v any) int {
	switch v := v.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}

func keyCode(input map[string]any) (uint16, uint, error) {
	value, _ := input["key"].(string)
	if value == "" {
		value, _ = input["text"].(string)
	}
	var mods uint
	var key string
	for _, part := range strings.Fields(strings.ReplaceAll(strings.ToLower(value), "+", " ")) {
		switch part {
		case "shift":
			mods |= 1 << 17
		case "ctrl", "control":
			mods |= 1 << 18
		case "alt", "option":
			mods |= 1 << 19
		case "cmd", "command", "meta":
			mods |= 1 << 20
		default:
			key = part
		}
	}
	code, ok := keyCodes[key]
	if !ok {
		return 0, 0, fmt.Errorf("unsupported key %q", key)
	}
	return code, mods, nil
}

var keyCodes = map[string]uint16{
	"a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7,
	"c": 8, "v": 9, "b": 11, "q": 12, "w": 13, "e": 14, "r": 15,
	"y": 16, "t": 17, "o": 31, "u": 32, "i": 34, "p": 35, "l": 37,
	"j": 38, "k": 40, "n": 45, "m": 46, ".": 47, "enter": 36,
	"return": 36, "tab": 48, "space": 49, "backspace": 51, "delete": 51,
	"escape": 53, "esc": 53, "pageup": 116, "pagedown": 121, "left": 123,
	"right": 124, "down": 125, "up": 126,
}
