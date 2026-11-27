package probe

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDetectStripForRootMountedHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><script>window.CONFIG={base_path:'/'}</script><script src="/assets/app.js"></script>`))
	}))
	defer srv.Close()
	got, err := Detect(srv.URL, "openlist", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != "strip" {
		t.Errorf("根挂载 HTML 应推荐 strip，got %s: %s", got.Mode, got.Reason)
	}
	if got.Confidence != "high" {
		t.Errorf("应有高置信，got %s", got.Confidence)
	}
}

func TestDetectPassThroughForSelfBasedRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		http.Redirect(w, r, "/openlist/", http.StatusFound)
	}))
	defer srv.Close()
	got, err := Detect(srv.URL, "openlist", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != "pass-through" {
		t.Errorf("自配 base path 的 302 应推荐 pass-through，got %s: %s", got.Mode, got.Reason)
	}
}

func TestDetectPassThroughForInlineBasePath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script>window.APP={base_path:'/openlist'}</script></body></html>`))
	}))
	defer srv.Close()
	got, err := Detect(srv.URL, "openlist", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != "pass-through" {
		t.Errorf("内联非根 base_path 应推荐 pass-through，got %s: %s", got.Mode, got.Reason)
	}
}

func TestDetectIsolatedForJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	got, err := Detect(srv.URL, "api", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != "isolated" {
		t.Errorf("非 HTML 应推荐 isolated，got %s: %s", got.Mode, got.Reason)
	}
}

func TestDetectSignalsNotEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<base href="/"><script src="/assets/a.js"></script>`))
	}))
	defer srv.Close()
	got, err := Detect(srv.URL, "app", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Signals) == 0 || strings.TrimSpace(got.Reason) == "" {
		t.Errorf("应给出信号与结论: %+v", got)
	}
}
