package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

// HTTPOpener resolves identifiers as URL-encoded HTTP(S) URLs.
//
// Identifier convention matches Cantaloupe's HttpSource with
// BasicLookupStrategy: the IIIF identifier is the URL-encoded source URL,
// e.g. /iiif/3/{urlencoded-https-url}/info.json.
//
// AllowedHosts gates which upstream hosts may be fetched. An empty list
// means "fetch nothing"; "*" means "any host" (only set this for closed
// internal deployments).
type HTTPOpener struct {
	Client       *http.Client
	AllowedHosts []string
	UserAgent    string
	MaxBytes     int64
}

// NewHTTPOpener constructs an HTTPOpener with sane timeouts.
func NewHTTPOpener(allowedHosts []string, requestTimeout time.Duration, maxBytes int64) *HTTPOpener {
	if requestTimeout == 0 {
		requestTimeout = 30 * time.Second
	}
	return &HTTPOpener{
		Client:       &http.Client{Timeout: requestTimeout},
		AllowedHosts: allowedHosts,
		UserAgent:    "triplet/0.1 (+https://github.com/libops/triplet)",
		MaxBytes:     maxBytes,
	}
}

// Open fetches the URL named by identifier and returns a seekable view of
// the bytes. Upstream bytes are spooled into a temporary file so libvips can
// seek without requiring the whole source to sit in memory. Bound by MaxBytes
// to prevent runaway downloads.
//
// Wrapping HTTPOpener in a [Caching] decorator is strongly recommended: this
// implementation does not cache fetches itself.
func (h *HTTPOpener) Open(ctx context.Context, identifier string) (io.ReadSeekCloser, Meta, error) {
	if identifier == "" {
		return nil, Meta{}, fmt.Errorf("%w: empty identifier", ErrNotFound)
	}
	target, err := url.Parse(identifier)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, Meta{}, fmt.Errorf("%w: identifier must be an http(s) URL", ErrNotFound)
	}
	if !h.hostAllowed(target.Host) {
		return nil, Meta{}, fmt.Errorf("%w: host %q not in allow-list", ErrNotFound, target.Host)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("http source: %w", err)
	}
	req.Header.Set("User-Agent", h.UserAgent)
	req.Header.Set("Accept", "image/*")

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("http source fetch %q: %w", target.Redacted(), err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, Meta{}, fmt.Errorf("%w: upstream 404", ErrNotFound)
	default:
		return nil, Meta{}, fmt.Errorf("http source %q: upstream status %d", target.Redacted(), resp.StatusCode)
	}

	var reader io.Reader = resp.Body
	if h.MaxBytes > 0 {
		reader = io.LimitReader(resp.Body, h.MaxBytes+1)
	}
	tmp, err := os.CreateTemp("", "triplet-http-source-*")
	if err != nil {
		return nil, Meta{}, fmt.Errorf("http source temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	n, err := io.Copy(tmp, reader)
	if err != nil {
		cleanup()
		return nil, Meta{}, fmt.Errorf("http source read: %w", err)
	}
	if h.MaxBytes > 0 && n > h.MaxBytes {
		cleanup()
		return nil, Meta{}, fmt.Errorf("http source %q: response exceeds max_bytes %d", target.Redacted(), h.MaxBytes)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, Meta{}, fmt.Errorf("http source rewind: %w", err)
	}

	meta := Meta{
		ContentType: resp.Header.Get("Content-Type"),
		Size:        n,
	}
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		meta.ModTime = t
	}
	return &tempFileReadSeekCloser{File: tmp, path: tmpName}, meta, nil
}

func (h *HTTPOpener) hostAllowed(host string) bool {
	if len(h.AllowedHosts) == 0 {
		return false
	}
	hostOnly := host
	if i := strings.IndexByte(host, ':'); i >= 0 {
		hostOnly = host[:i]
	}
	if slices.Contains(h.AllowedHosts, "*") {
		return true
	}
	return slices.Contains(h.AllowedHosts, hostOnly)
}

// seekableBytes is a tiny io.ReadSeeker over a []byte without pulling in
// bytes.Reader (the std lib type would be fine; this exists only so the
// closer type is local to the package and trivially debuggable).
type seekableBytes struct {
	b   []byte
	off int64
}

func newSeekableBytes(b []byte) *seekableBytes { return &seekableBytes{b: b} }

func (s *seekableBytes) Read(p []byte) (int, error) {
	if s.off >= int64(len(s.b)) {
		return 0, io.EOF
	}
	n := copy(p, s.b[s.off:])
	s.off += int64(n)
	return n, nil
}

func (s *seekableBytes) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = s.off + offset
	case io.SeekEnd:
		abs = int64(len(s.b)) + offset
	default:
		return 0, errors.New("seek: invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("seek: negative position")
	}
	s.off = abs
	return abs, nil
}

type bytesReadSeekCloser struct{ r *seekableBytes }

func (b *bytesReadSeekCloser) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *bytesReadSeekCloser) Seek(off int64, whence int) (int64, error) {
	return b.r.Seek(off, whence)
}
func (b *bytesReadSeekCloser) Close() error { return nil }

type tempFileReadSeekCloser struct {
	*os.File
	path string
}

func (t *tempFileReadSeekCloser) Close() error {
	err := t.File.Close()
	if remErr := os.Remove(t.path); err == nil {
		err = remErr
	}
	return err
}
