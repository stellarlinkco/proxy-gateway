package handlers

import (
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

//go:embed frontend/dist/index.html frontend/dist/foo.js frontend/dist/assets/*
var testFrontendFS embed.FS

//go:embed frontend/dist/assets/*
var testFrontendNoIndexFS embed.FS

func TestServeFrontend_SubMissingReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var empty embed.FS
	r := gin.New()
	ServeFrontend(r, empty)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "前端资源未找到") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestServeFrontend_SpaFallbackWithoutIndexReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	ServeFrontend(r, testFrontendNoIndexFS)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/some/route", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "前端资源未找到") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestServeFrontend_ServesIndexStaticAndSpaFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	ServeFrontend(r, testFrontendFS)

	// index
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "test-index") {
			t.Fatalf("unexpected index: %s", w.Body.String())
		}
	}

	// static assets
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("assets status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// api path 404 json
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("api status=%d body=%s", w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp["error"] != "API endpoint not found" {
			t.Fatalf("unexpected resp: %+v", resp)
		}
	}

	// file exists under dist root, served with content-type
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/foo.js", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("file status=%d body=%s", w.Code, w.Body.String())
		}
		ct := w.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "application/javascript") {
			t.Fatalf("content-type=%q", ct)
		}
	}

	// SPA fallback to index.html
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/some/route", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("spa status=%d body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "test-index") {
			t.Fatalf("unexpected spa body: %s", w.Body.String())
		}
	}

	if !isAPIPath("/v1/messages") {
		t.Fatalf("expected isAPIPath true")
	}
	if isAPIPath("/not-api") {
		t.Fatalf("expected isAPIPath false")
	}

	if getContentType("") != "text/html; charset=utf-8" {
		t.Fatalf("unexpected empty content-type")
	}
	if !strings.HasPrefix(getContentType("x.css"), "text/css") {
		t.Fatalf("unexpected css content-type")
	}
	if getContentType("x.bin") != "application/octet-stream" {
		t.Fatalf("unexpected default content-type")
	}
	if !strings.Contains(getErrorPage(), "<!DOCTYPE html>") {
		t.Fatalf("unexpected error page")
	}
}

func TestGetContentType_AllKnownExtensions(t *testing.T) {
	cases := map[string]string{
		"a.html":             "text/html; charset=utf-8",
		"a.css":              "text/css; charset=utf-8",
		"a.js":               "application/javascript; charset=utf-8",
		"a.json":             "application/json; charset=utf-8",
		"a.png":              "image/png",
		"a.jpg":              "image/jpeg",
		"a.jpeg":             "image/jpeg",
		"a.gif":              "image/gif",
		"a.svg":              "image/svg+xml",
		"a.ico":              "image/x-icon",
		"a.woff":             "font/woff",
		"a.woff2":            "font/woff2",
		"a.ttf":              "font/ttf",
		"a.eot":              "application/vnd.ms-fontobject",
		"dir/a.js":           "application/javascript; charset=utf-8",
		"dir.with.dot/a.css": "text/css; charset=utf-8",
		"noext":              "application/octet-stream",
		"dir/noext":          "application/octet-stream",
		"dir.with.dot/noext": "application/octet-stream",
	}

	for path, want := range cases {
		if got := getContentType(path); got != want {
			t.Fatalf("getContentType(%q)=%q, want %q", path, got, want)
		}
	}
}
