package handler

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/libops/triplet/internal/cors"
	pstore "github.com/libops/triplet/internal/iiif/presentation/v3/store"
)

const (
	testPrefix     = "/presentation/v3"
	testPublicBase = "https://iiif.example.org"
	testWriteToken = "test-token"
)

type presentationTestServer struct {
	server *httptest.Server
	store  *pstore.FileStore
}

func newPresentationTestServer(t *testing.T, writeEnabled bool, allowedOrigins []string, seed map[string][]byte) presentationTestServer {
	t.Helper()
	st, err := pstore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for key, body := range seed {
		if _, err := st.Put(t.Context(), key, body, pstore.Preconditions{IfNoneMatch: "*"}); err != nil {
			t.Fatalf("seed %q: %v", key, err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(
		testPrefix,
		testPublicBase,
		st,
		cors.New(allowedOrigins, "ETag, Last-Modified, Content-Length, Location"),
		writeEnabled,
		testWriteToken,
		logger,
	)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return presentationTestServer{server: srv, store: st}
}

func publicID(key string) string {
	return testPublicBase + testPrefix + "/" + key
}

func manifestBody(key string) []byte {
	return []byte(fmt.Sprintf(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":%q,"type":"Manifest","label":{"en":["Manifest"]},"items":[]}`, publicID(key)))
}

func canvasBody(key string) []byte {
	return []byte(fmt.Sprintf(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":%q,"type":"Canvas","width":100,"height":200,"items":[]}`, publicID(key)))
}

func annotationPageBody(key, value string) []byte {
	return []byte(fmt.Sprintf(`{"@context":["http://iiif.io/api/extension/text-granularity/context.json","http://iiif.io/api/presentation/3/context.json"],"id":%q,"type":"AnnotationPage","items":[{"id":%q,"type":"Annotation","motivation":"supplementing","body":{"type":"TextualBody","value":%q},"target":"https://iiif.example.org/canvas/1#xywh=1,2,3,4","textGranularity":"line"}]}`, publicID(key), publicID(key+"/items/1"), value))
}

func annotationBody(key string) []byte {
	return []byte(fmt.Sprintf(`{"@context":["http://iiif.io/api/extension/text-granularity/context.json","http://iiif.io/api/presentation/3/context.json"],"id":%q,"type":"Annotation","target":"https://iiif.example.org/canvas/1","textGranularity":"token"}`, publicID(key)))
}

func collectionBody(key string) []byte {
	return []byte(fmt.Sprintf(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":%q,"type":"Collection","label":{"en":["Collection"]},"items":[]}`, publicID(key)))
}

func annotationCollectionBody(key string) []byte {
	return []byte(fmt.Sprintf(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":%q,"type":"AnnotationCollection"}`, publicID(key)))
}

func authorizedPUT(t *testing.T, endpoint string, body []byte, ifMatch, ifNoneMatch string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testWriteToken)
	req.Header.Set("Content-Type", "application/ld+json")
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestResourceGetHeadAndConditionalRequests(t *testing.T) {
	key := "items/item-1/manifest"
	body := manifestBody(key)
	testServer := newPresentationTestServer(t, false, []string{"https://viewer.example.org"}, map[string][]byte{key: body})
	endpoint := testServer.server.URL + testPrefix + "/" + key

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://viewer.example.org")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !bytes.Equal(got, body) {
		t.Fatalf("GET status/body = %d, %q", resp.StatusCode, got)
	}
	if got := resp.Header.Get("Content-Type"); got != documentMediaType {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Fatalf("Content-Length = %q", got)
	}
	etag := resp.Header.Get("ETag")
	lastModified := resp.Header.Get("Last-Modified")
	if etag == "" || lastModified == "" {
		t.Fatalf("missing validators: ETag=%q Last-Modified=%q", etag, lastModified)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://viewer.example.org" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := resp.Header.Get("Access-Control-Expose-Headers"); got != "ETag, Last-Modified, Content-Length, Location" {
		t.Fatalf("Access-Control-Expose-Headers = %q", got)
	}

	headReq, _ := http.NewRequest(http.MethodHead, endpoint, nil)
	headResp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		t.Fatal(err)
	}
	headBody, _ := io.ReadAll(headResp.Body)
	_ = headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK || len(headBody) != 0 || headResp.Header.Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatalf("HEAD status/body/length = %d, %q, %q", headResp.StatusCode, headBody, headResp.Header.Get("Content-Length"))
	}

	for name, header := range map[string]string{"strong ETag": etag, "weak ETag": "W/" + etag} {
		t.Run(name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
			req.Header.Set("If-None-Match", header)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNotModified {
				t.Fatalf("status = %d", resp.StatusCode)
			}
		})
	}
	modifiedReq, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	modifiedReq.Header.Set("If-Modified-Since", lastModified)
	modifiedResp, err := http.DefaultClient.Do(modifiedReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = modifiedResp.Body.Close()
	if modifiedResp.StatusCode != http.StatusNotModified {
		t.Fatalf("If-Modified-Since status = %d", modifiedResp.StatusCode)
	}
}

func TestConditionalPutSupportsGenericResourceTypes(t *testing.T) {
	testServer := newPresentationTestServer(t, true, nil, nil)
	tests := []struct {
		name string
		key  string
		body []byte
	}{
		{name: "manifest", key: "items/1/manifest"},
		{name: "canvas", key: "items/1/canvas/1"},
		{name: "annotation page", key: "items/1/canvas/1/annotations"},
		{name: "annotation", key: "items/1/canvas/1/annotations/items/1"},
		{name: "collection", key: "collections/1"},
		{name: "annotation collection", key: "annotation-collections/1"},
	}
	tests[0].body = manifestBody(tests[0].key)
	tests[1].body = canvasBody(tests[1].key)
	tests[2].body = annotationPageBody(tests[2].key, "hello")
	tests[3].body = annotationBody(tests[3].key)
	tests[4].body = collectionBody(tests[4].key)
	tests[5].body = annotationCollectionBody(tests[5].key)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint := testServer.server.URL + testPrefix + "/" + test.key
			resp := authorizedPUT(t, endpoint, test.body, "", "*")
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("PUT status = %d", resp.StatusCode)
			}
			if got := resp.Header.Get("Location"); got != publicID(test.key) {
				t.Fatalf("Location = %q", got)
			}
			getResp, err := http.Get(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(getResp.Body)
			_ = getResp.Body.Close()
			if getResp.StatusCode != http.StatusOK || !bytes.Equal(got, test.body) {
				t.Fatalf("GET status/body = %d, %q", getResp.StatusCode, got)
			}
		})
	}
}

func TestReplaceAndDeleteUseStrongPreconditions(t *testing.T) {
	key := "items/1/canvas/1/annotations"
	first := annotationPageBody(key, "first")
	testServer := newPresentationTestServer(t, true, nil, map[string][]byte{key: first})
	endpoint := testServer.server.URL + testPrefix + "/" + key
	getResp, err := http.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	firstETag := getResp.Header.Get("ETag")
	_ = getResp.Body.Close()

	second := annotationPageBody(key, "second")
	updateResp := authorizedPUT(t, endpoint, second, firstETag, "")
	_ = updateResp.Body.Close()
	if updateResp.StatusCode != http.StatusNoContent {
		t.Fatalf("update status = %d", updateResp.StatusCode)
	}
	secondETag := updateResp.Header.Get("ETag")
	if secondETag == "" || secondETag == firstETag {
		t.Fatalf("updated ETag = %q", secondETag)
	}
	staleResp := authorizedPUT(t, endpoint, first, firstETag, "")
	_ = staleResp.Body.Close()
	if staleResp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale PUT status = %d", staleResp.StatusCode)
	}

	deleteReq, _ := http.NewRequest(http.MethodDelete, endpoint, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+testWriteToken)
	deleteReq.Header.Set("If-Match", firstETag)
	deleteResp, err := http.DefaultClient.Do(deleteReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale DELETE status = %d", deleteResp.StatusCode)
	}

	deleteReq, _ = http.NewRequest(http.MethodDelete, endpoint, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+testWriteToken)
	deleteReq.Header.Set("If-Match", secondETag)
	deleteResp, err = http.DefaultClient.Do(deleteReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("matched DELETE status = %d", deleteResp.StatusCode)
	}
	missingResp, err := http.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_ = missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted GET status = %d", missingResp.StatusCode)
	}
}

func TestPutPreconditionContract(t *testing.T) {
	existingKey := "items/1/manifest"
	existingBody := manifestBody(existingKey)
	testServer := newPresentationTestServer(t, true, nil, map[string][]byte{existingKey: existingBody})
	tests := []struct {
		name        string
		key         string
		ifMatch     string
		ifNoneMatch string
		want        int
	}{
		{name: "condition required", key: "items/2/manifest", want: http.StatusPreconditionRequired},
		{name: "create existing", key: existingKey, ifNoneMatch: "*", want: http.StatusPreconditionFailed},
		{name: "replace missing wildcard", key: "items/2/manifest", ifMatch: "*", want: http.StatusPreconditionFailed},
		{name: "both conditions", key: "items/2/manifest", ifMatch: "*", ifNoneMatch: "*", want: http.StatusBadRequest},
		{name: "non wildcard If-None-Match", key: "items/2/manifest", ifNoneMatch: `"etag"`, want: http.StatusBadRequest},
		{name: "weak If-Match", key: existingKey, ifMatch: `W/"etag"`, want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp := authorizedPUT(t, testServer.server.URL+testPrefix+"/"+test.key, manifestBody(test.key), test.ifMatch, test.ifNoneMatch)
			_ = resp.Body.Close()
			if resp.StatusCode != test.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, test.want)
			}
		})
	}
}

func TestWriteAuthenticationMediaTypeValidationAndLimit(t *testing.T) {
	testServer := newPresentationTestServer(t, true, nil, nil)
	key := "items/1/manifest"
	endpoint := testServer.server.URL + testPrefix + "/" + key
	body := manifestBody(key)

	unauthorized, _ := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(body))
	unauthorized.Header.Set("Content-Type", "application/json")
	unauthorized.Header.Set("If-None-Match", "*")
	resp, err := http.DefaultClient.Do(unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthorized response = %d, %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	badMedia, _ := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(body))
	badMedia.Header.Set("Authorization", "Bearer "+testWriteToken)
	badMedia.Header.Set("Content-Type", "text/plain")
	badMedia.Header.Set("If-None-Match", "*")
	resp, err = http.DefaultClient.Do(badMedia)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("bad media status = %d", resp.StatusCode)
	}

	tooLarge, _ := http.NewRequest(http.MethodPut, endpoint, strings.NewReader(strings.Repeat("x", maxWriteBodyBytes+1)))
	tooLarge.Header.Set("Authorization", "Bearer "+testWriteToken)
	tooLarge.Header.Set("Content-Type", "application/json")
	tooLarge.Header.Set("If-None-Match", "*")
	resp, err = http.DefaultClient.Do(tooLarge)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("large body status = %d", resp.StatusCode)
	}
}

func TestPutRejectsInvalidResourceAndIDMismatch(t *testing.T) {
	testServer := newPresentationTestServer(t, true, nil, nil)
	key := "items/1/manifest"
	endpoint := testServer.server.URL + testPrefix + "/" + key
	tests := []struct {
		name string
		body []byte
		want int
	}{
		{name: "malformed", body: []byte(`{`), want: http.StatusBadRequest},
		{name: "unsupported type", body: []byte(fmt.Sprintf(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":%q,"type":"Sequence","items":[]}`, publicID(key))), want: http.StatusBadRequest},
		{name: "ID mismatch", body: manifestBody("other/manifest"), want: http.StatusConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp := authorizedPUT(t, endpoint, test.body, "", "*")
			_ = resp.Body.Close()
			if resp.StatusCode != test.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, test.want)
			}
		})
	}
}

func TestDeleteContract(t *testing.T) {
	key := "items/1/manifest"
	testServer := newPresentationTestServer(t, true, nil, map[string][]byte{key: manifestBody(key)})
	endpoint := testServer.server.URL + testPrefix + "/" + key

	req, _ := http.NewRequest(http.MethodDelete, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+testWriteToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status = %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodDelete, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+testWriteToken)
	req.Header.Set("If-Match", "*")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("wildcard delete status = %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodDelete, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+testWriteToken)
	req.Header.Set("If-Match", "*")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("missing delete status = %d", resp.StatusCode)
	}
}

func TestCORSOptionsAndMethodAllow(t *testing.T) {
	testServer := newPresentationTestServer(t, true, []string{"https://editor.example.org"}, nil)
	endpoint := testServer.server.URL + testPrefix + "/items/1/manifest"
	req, _ := http.NewRequest(http.MethodOptions, endpoint, nil)
	req.Header.Set("Origin", "https://editor.example.org")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got != "GET, HEAD, PUT, DELETE, OPTIONS" {
		t.Fatalf("Access-Control-Allow-Methods = %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type, If-Match, If-None-Match" {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}

	patchReq, _ := http.NewRequest(http.MethodPatch, endpoint, nil)
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusMethodNotAllowed || !strings.Contains(patchResp.Header.Get("Allow"), "DELETE") {
		t.Fatalf("PATCH response = %d, Allow %q", patchResp.StatusCode, patchResp.Header.Get("Allow"))
	}
}

func TestRejectsNonCanonicalAndQueryResourcePaths(t *testing.T) {
	testServer := newPresentationTestServer(t, false, nil, nil)
	for _, rawURL := range []string{
		testServer.server.URL + testPrefix + "/items/%69tem/manifest",
		testServer.server.URL + testPrefix + "/items/item%2Fmanifest",
		testServer.server.URL + testPrefix + "/items/item/manifest?view=1",
	} {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("GET %q status = %d", rawURL, resp.StatusCode)
		}
	}
}

func TestInvalidStoredResourcesFailClosed(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{name: "invalid JSON", body: []byte(`{`)},
		{name: "mismatched ID", body: manifestBody("other/manifest")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := "items/1/manifest"
			testServer := newPresentationTestServer(t, false, nil, map[string][]byte{key: test.body})
			resp, err := http.Get(testServer.server.URL + testPrefix + "/" + key)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status = %d", resp.StatusCode)
			}
		})
	}
}

func TestNotFoundAndWritesDisabled(t *testing.T) {
	testServer := newPresentationTestServer(t, false, nil, nil)
	endpoint := testServer.server.URL + testPrefix + "/items/1/manifest"
	resp, err := http.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET status = %d", resp.StatusCode)
	}
	resp = authorizedPUT(t, endpoint, manifestBody("items/1/manifest"), "", "*")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("disabled PUT status = %d", resp.StatusCode)
	}
}
