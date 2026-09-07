package restore

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tmc/apple/x/iosrestore"
)

func assetMessage(kind, address string) map[string]any {
	method := "GET"
	if kind == "StreamedImageDecryptionKey" {
		method = "POST"
	}
	return map[string]any{"DataType": kind, "Arguments": map[string]any{
		"RequestMethod": method, "RequestURL": address,
		"RequestBody": []byte("key request"), "RequestAdditionalHeaders": map[string]any{"X-Test": "forwarded"},
	}}
}

func assetExchange(t *testing.T, assets *Assets, message map[string]any) map[string]any {
	t.Helper()
	host, peer := net.Pipe()
	defer host.Close()
	defer peer.Close()
	host.SetDeadline(time.Now().Add(3 * time.Second))
	peer.SetDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() {
		done <- (&SessionData{Assets: assets}).Handle(context.Background(), host, message)
	}()
	r := bufio.NewReader(peer)
	header, err := r.Peek(12)
	if err != nil {
		t.Fatal(err)
	}
	wantBinary := message["DataType"] == "URLAsset"
	if (string(header[4:12]) == "bplist00") != wantBinary {
		t.Fatal("incorrect asset plist format")
	}
	response, err := iosrestore.Receive(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return response
}

func TestAssetsHTTP(t *testing.T) {
	for _, tt := range []struct {
		kind   string
		status int
	}{
		{"URLAsset", 200}, {"URLAsset", 404}, {"StreamedImageDecryptionKey", 200}, {"StreamedImageDecryptionKey", 503},
	} {
		t.Run(fmt.Sprintf("%s/%d", tt.kind, tt.status), func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if tt.kind == "URLAsset" {
					if r.Method != "GET" || r.Header.Get("X-Test") != "" {
						t.Error("incorrect GET request")
					}
				} else {
					body, _ := io.ReadAll(r.Body)
					if r.Method != "POST" || string(body) != "key request" || r.Header.Get("X-Test") != "forwarded" {
						t.Error("incorrect POST request")
					}
				}
				w.Header().Add("X-Reply", "one")
				w.Header().Add("X-Reply", "two")
				w.WriteHeader(tt.status)
				io.WriteString(w, "asset response")
			}))
			defer server.Close()
			assets := &Assets{Client: server.Client()}
			for range 2 {
				response := assetExchange(t, assets, assetMessage(tt.kind, server.URL))
				if string(response["ResponseBody"].([]byte)) != "asset response" || response["ResponseBodyDone"] != true || response["ResponseStatus"] != int64(tt.status) || response["ResponseHeaders"].(map[string]any)["X-Reply"] != "one, two" {
					t.Fatal(response)
				}
			}
			want := int64(1)
			if tt.kind == "StreamedImageDecryptionKey" || tt.status != http.StatusOK {
				want = 2
			}
			if requests.Load() != want {
				t.Fatalf("requests = %d, want %d", requests.Load(), want)
			}
		})
	}
}

func TestAssetsInvalidRequests(t *testing.T) {
	for _, tt := range []struct {
		name, field string
		value       any
	}{
		{"method", "RequestMethod", "DELETE"},
		{"URL type", "RequestURL", true},
		{"scheme", "RequestURL", "file:///private/key"},
		{"credentials", "RequestURL", "https://user:password@example.com"},
		{"headers", "RequestAdditionalHeaders", "not a dictionary"},
		{"header value", "RequestAdditionalHeaders", map[string]any{"X-Test": true}},
		{"body", "RequestBody", "not data"},
		{"body size", "RequestBody", make([]byte, maxAssetBody+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			message := assetMessage("StreamedImageDecryptionKey", "https://example.invalid")
			message["Arguments"].(map[string]any)[tt.field] = tt.value
			a := &Assets{Client: &http.Client{Transport: assetTransport(func(*http.Request) (*http.Response, error) {
				t.Error("sent invalid request")
				return nil, fmt.Errorf("unexpected request")
			})}}
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			if err := a.Handle(context.Background(), host, message); err == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
}

type assetTransport func(*http.Request) (*http.Response, error)

func (f assetTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAssetsLimitsAndClosure(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(fmt.Sprint(headers), func(t *testing.T) {
			body := &objectReader{Reader: io.LimitReader(zeroReader{}, maxAssetBody+1)}
			header := make(http.Header)
			if headers {
				header.Set("X-Large", strings.Repeat("x", maxAssetHeader+1))
			}
			a := &Assets{Client: &http.Client{Transport: assetTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: header, Body: body}, nil
			})}}
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			if err := a.Handle(context.Background(), host, assetMessage("URLAsset", "https://example.invalid")); err == nil {
				t.Fatal("accepted oversized response")
			}
			if !body.closed {
				t.Fatal("response body not closed")
			}
			if len(a.cache) != 0 {
				t.Fatal("cached failed response")
			}
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestAssetsCancelHTTP(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	a := &Assets{Client: server.Client()}
	host, peer := net.Pipe()
	defer host.Close()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Handle(ctx, host, assetMessage("URLAsset", server.URL)) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP cancellation did not finish")
	}
	if len(a.cache) != 0 {
		t.Fatal("cached partial response")
	}
}

func TestAssetsCacheBudget(t *testing.T) {
	var a Assets
	for i := range maxCachedAssets + 1 {
		a.remember(fmt.Sprint(i), &assetResponse{body: []byte("asset")})
	}
	if len(a.cache) != 1 || a.bytes > maxAssetCache {
		t.Fatal("cache entry budget not enforced")
	}
	for i := range 5 {
		a.remember(fmt.Sprintf("large%d", i), &assetResponse{body: make([]byte, maxAssetBody)})
	}
	if a.bytes > maxAssetCache {
		t.Fatal("cache byte budget not enforced")
	}
}

func TestAssetsNestedRouting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "asset") }))
	defer server.Close()
	for _, dedicated := range []bool{false, true} {
		t.Run(fmt.Sprint(dedicated), func(t *testing.T) {
			a := &Assets{Client: server.Client()}
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			host.SetDeadline(time.Now().Add(3 * time.Second))
			peer.SetDeadline(time.Now().Add(3 * time.Second))
			message := assetMessage("URLAsset", server.URL)
			delete(message, "DataType")
			message["MsgType"] = "URLAsset"
			var control net.Conn = host
			if dedicated {
				message["DataPort"] = uint64(62002)
				control = nil
			}
			done := make(chan error, 1)
			go func() {
				done <- a.Serve(context.Background(), control, func(ctx context.Context, port uint16) (net.Conn, error) {
					if !dedicated || port != 62002 {
						return nil, fmt.Errorf("unexpected dial")
					}
					return host, nil
				}, message)
			}()
			response, err := iosrestore.Receive(peer)
			if err != nil || string(response["ResponseBody"].([]byte)) != "asset" {
				t.Fatalf("response = %v, error = %v", response, err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if _, changed := message["DataType"]; changed {
				t.Fatal("mutated nested request")
			}
			if dedicated {
				var b [1]byte
				if _, err := peer.Read(b[:]); err != io.EOF {
					t.Fatalf("dedicated connection not closed: %v", err)
				}
			} else {
				go func() { done <- iosrestore.Send(host, map[string]any{"StillOpen": true}) }()
				m, err := iosrestore.Receive(peer)
				if err != nil || m["StillOpen"] != true {
					t.Fatal("borrowed control was closed")
				}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestAssetsRedactURL(t *testing.T) {
	a := &Assets{Client: &http.Client{Transport: assetTransport(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("connection failed") })}}
	host, peer := net.Pipe()
	defer host.Close()
	defer peer.Close()
	err := a.Handle(context.Background(), host, assetMessage("URLAsset", "https://example.invalid/?secret=token"))
	if err == nil || strings.Contains(err.Error(), "token") {
		t.Fatalf("unredacted error: %v", err)
	}
}

func TestAssetsFailedDelivery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "asset") }))
	defer server.Close()
	a := &Assets{Client: server.Client()}
	host, peer := net.Pipe()
	defer host.Close()
	peer.Close()
	if err := a.Handle(context.Background(), host, assetMessage("URLAsset", server.URL)); err == nil {
		t.Fatal("accepted failed delivery")
	}
	if len(a.cache) != 0 {
		t.Fatal("cached an undelivered response")
	}
}

func TestAssetsConcurrentCache(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, "asset")
	}))
	defer server.Close()
	a := &Assets{Client: server.Client()}
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			assetExchange(t, a, assetMessage("URLAsset", server.URL))
		}()
	}
	workers.Wait()
	before := calls.Load()
	assetExchange(t, a, assetMessage("URLAsset", server.URL))
	if calls.Load() != before || len(a.cache) != 1 {
		t.Fatal("concurrent cache result not retained")
	}
}

func TestAssetsAEAHandshake(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "asset key") }))
	defer server.Close()
	a := &Assets{Client: server.Client()}
	host, peer := net.Pipe()
	assetHost, assetPeer := net.Pipe()
	defer host.Close()
	defer peer.Close()
	defer assetHost.Close()
	defer assetPeer.Close()
	for _, conn := range []net.Conn{host, peer, assetHost, assetPeer} {
		conn.SetDeadline(time.Now().Add(3 * time.Second))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	payload := bytes.Repeat([]byte{'x'}, 8192+17)
	copy(payload, "AEA1")
	source := &objectReader{Reader: bytes.NewReader(payload)}
	data := &SessionData{Assets: a,
		Object: func(context.Context, map[string]any, string) (io.ReadCloser, error) { return source, nil },
		NestedAsset: func(ctx context.Context, message map[string]any) error {
			return a.Serve(ctx, nil, func(ctx context.Context, port uint16) (net.Conn, error) {
				if port != 62002 {
					return nil, fmt.Errorf("wrong nested asset port")
				}
				return assetHost, nil
			}, message)
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- data.Handle(ctx, host, map[string]any{"DataType": "SourceBootObjectV4", "Arguments": map[string]any{"ImageName": "OS"}})
	}()
	m, err := iosrestore.Receive(peer)
	if err != nil {
		t.Fatal(err)
	}
	got := m["FileData"].([]byte)
	request := assetMessage("URLAsset", server.URL)
	delete(request, "DataType")
	request["MsgType"], request["DataPort"] = "URLAsset", int64(62002)
	if err := iosrestore.Send(peer, request); err != nil {
		t.Fatal(err)
	}
	m, err = iosrestore.Receive(assetPeer)
	if err != nil || string(m["ResponseBody"].([]byte)) != "asset key" {
		t.Fatalf("asset reply = %v, error = %v", m, err)
	}
	for {
		m, err = iosrestore.Receive(peer)
		if err != nil {
			t.Fatal(err)
		}
		if m["FileDataDone"] == true {
			break
		}
		got = append(got, m["FileData"].([]byte)...)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) || !source.closed {
		t.Fatal("AEA stream was not resumed and closed")
	}
}

func ExampleAssets_Handle() {
	var assets Assets
	err := assets.Handle(context.Background(), nil, nil)
	fmt.Println(err)
	// Output: asset handler and connection are required
}

func ExampleAssets_Serve() {
	var assets Assets
	err := assets.Serve(context.Background(), nil, nil, map[string]any{"MsgType": "URLAsset"})
	fmt.Println(err)
	// Output: nested asset requires a data port or exclusively owned control connection
}
