package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalURLFallbackTriesLocalFileBeforeHTTP(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "derivatives", "service", "node", "193595"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "derivatives", "service", "node", "193595", "456524-service.jp2"), []byte("local"), 0o600); err != nil {
		t.Fatal(err)
	}
	var httpHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpHits++
		_, _ = w.Write([]byte("remote"))
	}))
	defer srv.Close()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	httpOp := NewHTTPOpener([]string{"127.0.0.1"}, 0, 0)
	httpOp.AllowPrivateHosts = true
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix: srv.URL + "/system/files",
			File:   fileOp,
		}},
		Fallback: httpOp,
	}

	rc, meta, err := op.Open(context.Background(), srv.URL+"/system/files/derivatives/service/node/193595/456524-service.jp2")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "local" {
		t.Fatalf("body = %q", body)
	}
	if meta.Size != int64(len("local")) {
		t.Fatalf("size = %d", meta.Size)
	}
	if httpHits != 0 {
		t.Fatalf("http hits = %d", httpHits)
	}
}

func TestLocalURLFallbackUsesHTTPWhenLocalMissing(t *testing.T) {
	root := t.TempDir()
	var httpHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpHits++
		_, _ = w.Write([]byte("remote"))
	}))
	defer srv.Close()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	httpOp := NewHTTPOpener([]string{"127.0.0.1"}, 0, 0)
	httpOp.AllowPrivateHosts = true
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix: srv.URL + "/system/files",
			File:   fileOp,
		}},
		Fallback: httpOp,
	}

	rc, _, err := op.Open(context.Background(), srv.URL+"/system/files/missing.jp2")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "remote" {
		t.Fatalf("body = %q", body)
	}
	if httpHits == 0 {
		t.Fatal("expected HTTP fallback")
	}
}

func TestLocalURLFallbackUsesAuthenticatedHTTPFallbackForProtectedMiss(t *testing.T) {
	root := t.TempDir()
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte("remote"))
	}))
	defer srv.Close()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	plainHTTP := NewHTTPOpener([]string{"127.0.0.1"}, 0, 0)
	plainHTTP.AllowPrivateHosts = true
	authHTTP := NewHTTPOpener([]string{"127.0.0.1"}, 0, 0)
	authHTTP.AllowPrivateHosts = true
	authHTTP.ForwardAuthHeaders = true
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix:    srv.URL + "/system/files",
			File:      fileOp,
			AuthProbe: true,
		}},
		Fallback:     plainHTTP,
		AuthFallback: authHTTP,
	}
	ctx := ContextWithAuthHeaders(context.Background(), http.Header{
		"Cookie": []string{"SESS=abc"},
	})
	rc, _, err := op.Open(ctx, srv.URL+"/system/files/missing.jp2")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "remote" {
		t.Fatalf("body = %q", body)
	}
	if gotCookie != "SESS=abc" {
		t.Fatalf("cookie = %q", gotCookie)
	}
}

func TestLocalURLFallbackSupportsMultipleRoots(t *testing.T) {
	systemRoot := t.TempDir()
	fedoraRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(systemRoot, "node"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fedoraRoot, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(systemRoot, "node", "system.jp2"), []byte("system"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fedoraRoot, "objects", "fedora.jp2"), []byte("fedora"), 0o600); err != nil {
		t.Fatal(err)
	}
	systemOp, err := NewFileOpener(systemRoot)
	if err != nil {
		t.Fatal(err)
	}
	fedoraOp, err := NewFileOpener(fedoraRoot)
	if err != nil {
		t.Fatal(err)
	}
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{
			{Prefix: "/system/files", File: systemOp},
			{Prefix: "/fedora", File: fedoraOp},
		},
		Fallback: errOpener{},
	}

	tests := []struct {
		identifier string
		want       string
	}{
		{"/system/files/node/system.jp2", "system"},
		{"/fedora/objects/fedora.jp2", "fedora"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			rc, _, err := op.Open(context.Background(), tt.identifier)
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			body, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != tt.want {
				t.Fatalf("body = %q", body)
			}
		})
	}
}

func TestLocalURLFallbackPathOnlyPrefixDoesNotMatchURLIdentifier(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "node"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node", "private.jp2"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix: "/system/files",
			File:   fileOp,
		}},
		Fallback: errOpener{},
	}

	_, _, err = op.Open(context.Background(), "https://attacker.example/system/files/node/private.jp2")
	if err == nil || err.Error() != "fallback should not be called" {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalURLFallbackAuthProbeAnonymousSucceedsAndCaches(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "private.jp2"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		if r.Method != http.MethodHead {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Fatalf("cookie = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix:                srv.URL + "/system/files",
			File:                  fileOp,
			AuthProbe:             true,
			AuthAnonymousCacheTTL: time.Minute,
		}},
		Fallback:     errOpener{},
		AuthFallback: testAuthHTTP(t, srv),
	}
	for i := 0; i < 2; i++ {
		rc, _, err := op.Open(context.Background(), srv.URL+"/system/files/private.jp2")
		if err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("probes = %d", got)
	}
}

func TestLocalURLFallbackAuthProbeFallsBackToCredentialedCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "private.jp2"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		if got := r.Header.Get("Cookie"); got != "SESS=abc" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix:                    srv.URL + "/system/files",
			File:                      fileOp,
			AuthProbe:                 true,
			AuthAnonymousCacheTTL:     time.Minute,
			AuthAuthenticatedCacheTTL: time.Minute,
		}},
		Fallback:     errOpener{},
		AuthFallback: testAuthHTTP(t, srv),
	}
	ctx := ContextWithAuthHeaders(context.Background(), http.Header{
		"Cookie": []string{"SESS=abc"},
	})
	for i := 0; i < 2; i++ {
		rc, _, err := op.Open(ctx, srv.URL+"/system/files/private.jp2")
		if err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
	}
	if got := probes.Load(); got != 2 {
		t.Fatalf("probes = %d", got)
	}
}

func TestLocalURLFallbackAuthProbeCoalescesConcurrentRequests(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "private.jp2"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	releaseProbe := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		probes.Add(1)
		<-releaseProbe
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix:                srv.URL + "/system/files",
			File:                  fileOp,
			AuthProbe:             true,
			AuthAnonymousCacheTTL: time.Minute,
		}},
		Fallback:     errOpener{},
		AuthFallback: testAuthHTTP(t, srv),
	}
	start := make(chan struct{})
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rc, _, err := op.Open(context.Background(), srv.URL+"/system/files/private.jp2")
			if err == nil {
				_ = rc.Close()
			}
			errs <- err
		}()
	}
	close(start)
	deadline := time.After(time.Second)
	for probes.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for auth probe")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(releaseProbe)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("probes = %d", got)
	}
}

func TestLocalURLFallbackAuthProbeDeniesLocalFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "private.jp2"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix:    srv.URL + "/system/files",
			File:      fileOp,
			AuthProbe: true,
		}},
		Fallback:     errOpener{},
		AuthFallback: testAuthHTTP(t, srv),
	}
	_, _, err = op.Open(context.Background(), srv.URL+"/system/files/private.jp2")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalURLFallbackAuthProbeUsesRangeGetWhenHeadUnsupported(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "private.jp2"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gotRange = r.Header.Get("Range")
		w.WriteHeader(http.StatusPartialContent)
	}))
	defer srv.Close()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix:    srv.URL + "/system/files",
			File:      fileOp,
			AuthProbe: true,
		}},
		Fallback:     errOpener{},
		AuthFallback: testAuthHTTP(t, srv),
	}
	rc, _, err := op.Open(context.Background(), srv.URL+"/system/files/private.jp2")
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if gotRange != "bytes=0-0" {
		t.Fatalf("range = %q", gotRange)
	}
}

func TestLocalURLFallbackAuthProbeUsesHTTPPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "private.jp2"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	denied := NewHTTPOpener([]string{"example.org"}, 0, 0)
	denied.AllowPrivateHosts = true
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix:    srv.URL + "/system/files",
			File:      fileOp,
			AuthProbe: true,
		}},
		Fallback:     errOpener{},
		AuthFallback: denied,
	}
	_, _, err = op.Open(context.Background(), srv.URL+"/system/files/private.jp2")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalURLFallbackMetaUsesAuthenticatedHTTPMetadataFallback(t *testing.T) {
	root := t.TempDir()
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		if r.Header.Get("Cookie") != "SESS=abc" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Length", "6")
	}))
	defer srv.Close()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	authHTTP := NewHTTPOpener([]string{"127.0.0.1"}, 0, 0)
	authHTTP.AllowPrivateHosts = true
	authHTTP.ForwardAuthHeaders = true
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix:    srv.URL + "/system/files",
			File:      fileOp,
			AuthProbe: true,
		}},
		Fallback:     errOpener{},
		AuthFallback: authHTTP,
	}
	ctx := ContextWithAuthHeaders(context.Background(), http.Header{
		"Cookie": []string{"SESS=abc"},
	})
	meta, err := op.Meta(ctx, srv.URL+"/system/files/missing.jp2")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodHead {
		t.Fatalf("method = %q", gotMethod)
	}
	if meta.Size != 6 {
		t.Fatalf("size = %d", meta.Size)
	}
}

func TestLocalURLFallbackAuthCacheEvictsWhenFull(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.jp2", "b.jp2"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var probes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		probes.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix:              srv.URL + "/system/files",
			File:                fileOp,
			AuthProbe:           true,
			AuthCacheMaxEntries: 1,
		}},
		Fallback:     errOpener{},
		AuthFallback: testAuthHTTP(t, srv),
	}
	for _, name := range []string{"a.jp2", "b.jp2", "a.jp2"} {
		rc, _, err := op.Open(context.Background(), srv.URL+"/system/files/"+name)
		if err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
	}
	if got := probes.Load(); got != 3 {
		t.Fatalf("probes = %d", got)
	}
}

func TestLocalURLFallbackOCFL(t *testing.T) {
	root := t.TempDir()
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix: "https://repo.example.edu/system/files",
			File:   fileOp,
			OCFL:   true,
		}},
		Fallback: errOpener{},
	}
	fedoraPath := "derivatives/service/node/193595/456524-service.jp2"
	objectDir := ocflDir(root, "info:fedora/"+fedoraPath)
	if err := os.MkdirAll(filepath.Join(objectDir, "extensions", "0005-mutable-head", "head"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(objectDir, "v1", "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(objectDir, "v1", "content", "file.jp2"), []byte("ocfl"), 0o600); err != nil {
		t.Fatal(err)
	}
	inventory := `{
		"head": "v1",
		"versions": {
			"v1": {
				"state": {
					"abc123": ["456524-service.jp2"]
				}
			}
		},
		"manifest": {
			"abc123": ["v1/content/file.jp2"]
		}
	}`
	if err := os.WriteFile(filepath.Join(objectDir, "extensions", "0005-mutable-head", "head", "inventory.json"), []byte(inventory), 0o600); err != nil {
		t.Fatal(err)
	}

	rc, _, err := op.Open(context.Background(), "https://repo.example.edu/system/files/"+fedoraPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ocfl" {
		t.Fatalf("body = %q", body)
	}
}

func TestLocalURLFallbackOCFLRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.jp2"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileOp, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	op := &LocalURLFallback{
		Mappings: []LocalURLMapping{{
			Prefix: "https://repo.example.edu/system/files",
			File:   fileOp,
			OCFL:   true,
		}},
		Fallback: errOpener{},
	}
	fedoraPath := "derivatives/service/node/193595/456524-service.jp2"
	objectDir := ocflDir(root, "info:fedora/"+fedoraPath)
	if err := os.MkdirAll(filepath.Join(objectDir, "extensions", "0005-mutable-head", "head"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(objectDir, "v1", "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.jp2"), filepath.Join(objectDir, "v1", "content", "file.jp2")); err != nil {
		t.Fatal(err)
	}
	inventory := `{
		"head": "v1",
		"versions": {
			"v1": {
				"state": {
					"abc123": ["456524-service.jp2"]
				}
			}
		},
		"manifest": {
			"abc123": ["v1/content/file.jp2"]
		}
	}`
	if err := os.WriteFile(filepath.Join(objectDir, "extensions", "0005-mutable-head", "head", "inventory.json"), []byte(inventory), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err = op.Open(context.Background(), "https://repo.example.edu/system/files/"+fedoraPath)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v", err)
	}
}

type errOpener struct{}

func (errOpener) Open(context.Context, string) (io.ReadSeekCloser, Meta, error) {
	return nil, Meta{}, errors.New("fallback should not be called")
}

func testAuthHTTP(t *testing.T, srv *httptest.Server) *HTTPOpener {
	t.Helper()
	op := NewHTTPOpener([]string{"127.0.0.1"}, 0, 0)
	op.AllowPrivateHosts = true
	op.ForwardAuthHeaders = true
	return op
}
