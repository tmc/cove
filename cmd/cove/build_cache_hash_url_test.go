package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHashURLNon200ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, err := hashURL(context.Background(), nil, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want 404", err)
	}
}

func TestHashURLBadRequestReturnsError(t *testing.T) {
	_, err := hashURL(context.Background(), nil, "http://%zz")
	if err == nil || !strings.Contains(err.Error(), "cache-url") {
		t.Fatalf("err = %v, want cache-url", err)
	}
}
