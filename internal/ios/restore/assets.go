package restore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tmc/apple/x/iosrestore"
)

const (
	maxAssetBody    = 8 << 20
	maxAssetHeader  = 64 << 10
	maxAssetCache   = 32 << 20
	maxCachedAssets = 64
)

// Assets serves restored's HTTP assets and streamed-image keys. Use one per
// restore attempt. The zero value uses http.DefaultClient. Configure Client
// before use; it must honor request contexts. Concurrent handlers are supported.
// Delivered HTTP 200 GET responses are cached by URL within a 32 MiB, 64-entry budget. Key POSTs
// are never cached. Request and response bodies are bounded to 8 MiB, leaving
// room for XML encoding in the restored service's 16 MiB frames.
type Assets struct {
	Client *http.Client
	mu     sync.Mutex
	cache  map[string]*assetResponse
	bytes  int
}

type assetResponse struct {
	body    []byte
	headers map[string]any
	status  int
}

// Handle fetches an HTTP response and sends its body, headers and status to the
// selected service. URLAsset uses GET and binary plist; StreamedImageDecryptionKey
// uses POST and XML plist. Non-200 statuses are forwarded. HTTP requests have a
// two-minute timeout bounded by ctx. There are no application-level retries.
// The caller owns conn, serializes writes and supplies its context deadlines.
func (a *Assets) Handle(ctx context.Context, conn net.Conn, message map[string]any) error {
	if a == nil || conn == nil {
		return fmt.Errorf("asset handler and connection are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	kind, _ := message["DataType"].(string)
	method := ""
	switch kind {
	case "URLAsset":
		method = http.MethodGet
	case "StreamedImageDecryptionKey":
		method = http.MethodPost
	default:
		return fmt.Errorf("unsupported asset data type %q", kind)
	}
	args, ok := message["Arguments"].(map[string]any)
	if !ok || args["RequestMethod"] != method {
		return fmt.Errorf("asset request requires %s Arguments", method)
	}
	address, ok := args["RequestURL"].(string)
	u, err := url.Parse(address)
	if !ok || err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("asset request requires an HTTP or HTTPS URL without credentials or fragment")
	}
	var response *assetResponse
	if method == http.MethodGet {
		a.mu.Lock()
		response = a.cache[address]
		a.mu.Unlock()
	}
	if response == nil {
		response, err = a.fetch(ctx, method, address, args)
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	reply := map[string]any{"ResponseBody": response.body, "ResponseBodyDone": true, "ResponseHeaders": response.headers, "ResponseStatus": response.status}
	if kind == "URLAsset" {
		if err := iosrestore.SendBinary(conn, reply); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if response.status == http.StatusOK {
			a.remember(address, response)
		}
		return nil
	}
	return iosrestore.Send(conn, reply)
}

func (a *Assets) fetch(ctx context.Context, method, address string, args map[string]any) (*assetResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var body []byte
	var headers map[string]any
	if method == http.MethodPost {
		var ok bool
		body, ok = args["RequestBody"].([]byte)
		if !ok || len(body) > maxAssetBody {
			return nil, fmt.Errorf("asset RequestBody must be data of at most 8 MiB")
		}
		headers, ok = args["RequestAdditionalHeaders"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("asset RequestAdditionalHeaders is not a dictionary")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("invalid asset HTTP request")
	}
	headerSize := 0
	for name, value := range headers {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("asset request header is not a string")
		}
		headerSize += len(name) + len(text)
		if headerSize > maxAssetHeader {
			return nil, fmt.Errorf("asset request headers exceed 64 KiB")
		}
		if strings.EqualFold(name, "Host") {
			req.Host = text
		} else {
			req.Header.Set(name, text)
		}
	}
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		var u *url.Error
		if errors.As(err, &u) {
			err = u.Err // Do not include signed URLs in diagnostics.
		}
		return nil, fmt.Errorf("request restore asset: %w", err)
	}
	defer res.Body.Close()
	result := &assetResponse{status: res.StatusCode, headers: make(map[string]any)}
	headerSize = 0
	for name, values := range res.Header {
		value := strings.Join(values, ", ")
		headerSize += len(name) + len(value)
		if headerSize > maxAssetHeader {
			return nil, fmt.Errorf("asset response headers exceed 64 KiB")
		}
		result.headers[name] = value
	}
	result.body, err = io.ReadAll(io.LimitReader(res.Body, maxAssetBody+1))
	if err != nil {
		return nil, fmt.Errorf("read restore asset: %w", err)
	}
	if len(result.body) > maxAssetBody {
		return nil, fmt.Errorf("asset response body exceeds 8 MiB")
	}
	return result, ctx.Err()
}

func (a *Assets) remember(address string, response *assetResponse) {
	cost := len(address) + len(response.body)
	for name, value := range response.headers {
		cost += len(name) + len(value.(string))
	}
	if cost > maxAssetCache {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cache[address] != nil {
		return
	}
	if a.cache == nil || a.bytes+cost > maxAssetCache || len(a.cache) >= maxCachedAssets {
		a.cache = make(map[string]*assetResponse)
		a.bytes = 0
	}
	a.cache[address] = response
	a.bytes += cost
}

// Serve routes a nested AEA URLAsset request. An explicit DataPort is dialed
// on the same selected device, watched for cancellation, and closed on return.
// Without a DataPort, control is borrowed; its caller must hold exclusive use
// for the entire call. Pass nil control when that cannot be guaranteed.
func (a *Assets) Serve(ctx context.Context, control net.Conn, dial func(context.Context, uint16) (net.Conn, error), message map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil {
		return fmt.Errorf("asset handler is required")
	}
	if message["MsgType"] != "URLAsset" {
		return fmt.Errorf("nested asset request is not URLAsset")
	}
	port, err := restoreDataPort(message, "URLAsset")
	if err != nil {
		return err
	}
	message = maps.Clone(message)
	message["DataType"] = "URLAsset"
	if port == 0 {
		if control == nil {
			return fmt.Errorf("nested asset requires a data port or exclusively owned control connection")
		}
		return a.Handle(ctx, control, message)
	}
	if dial == nil {
		return fmt.Errorf("nested asset requires a device-bound dialer")
	}
	return serveRestoreData(ctx, dial, port, message, a.Handle)
}
