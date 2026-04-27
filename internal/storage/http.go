package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
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
	// AllowPrivateHosts permits loopback, link-local, private, and other
	// non-public upstream addresses. Keep false for public deployments.
	AllowPrivateHosts bool
}

// NewHTTPOpener constructs an HTTPOpener with sane timeouts.
func NewHTTPOpener(allowedHosts []string, requestTimeout time.Duration, maxBytes int64) *HTTPOpener {
	if requestTimeout == 0 {
		requestTimeout = 30 * time.Second
	}
	h := &HTTPOpener{
		AllowedHosts: allowedHosts,
		UserAgent:    "triplet/0.1 (+https://github.com/libops/triplet)",
		MaxBytes:     maxBytes,
	}
	h.Client = &http.Client{Timeout: requestTimeout}
	return h
}

// Open fetches the URL named by identifier and returns a seekable view of the
// bytes. When upstream supports Range requests, the returned reader performs
// demand-driven byte-range GETs. Otherwise, upstream bytes are spooled into a
// temporary file so libvips can seek without requiring the whole source to sit
// in memory. Bound by MaxBytes to prevent runaway downloads.
//
// Wrapping HTTPOpener in a [Caching] decorator is strongly recommended: this
// implementation does not cache fetches itself.
func (h *HTTPOpener) Open(ctx context.Context, identifier string) (io.ReadSeekCloser, Meta, error) {
	target, err := h.parseTarget(identifier)
	if err != nil {
		return nil, Meta{}, err
	}
	if rc, meta, ok, err := h.openRange(ctx, target); ok || err != nil {
		return rc, meta, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("http source: %w", err)
	}
	req.Header.Set("User-Agent", h.UserAgent)
	req.Header.Set("Accept", "image/*")

	resp, err := h.client().Do(req)
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

	meta := httpMeta(resp.Header, n)
	return &tempFileReadSeekCloser{File: tmp, path: tmpName}, meta, nil
}

// Meta implements MetaReader.
func (h *HTTPOpener) Meta(ctx context.Context, identifier string) (Meta, error) {
	target, err := h.parseTarget(identifier)
	if err != nil {
		return Meta{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
	if err != nil {
		return Meta{}, fmt.Errorf("http source: %w", err)
	}
	req.Header.Set("User-Agent", h.UserAgent)
	req.Header.Set("Accept", "image/*")

	resp, err := h.client().Do(req)
	if err != nil {
		return Meta{}, fmt.Errorf("http source head %q: %w", target.Redacted(), err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		size := int64(0)
		if resp.ContentLength > 0 {
			size = resp.ContentLength
		}
		if h.MaxBytes > 0 && size > h.MaxBytes {
			return Meta{}, fmt.Errorf("http source %q: response exceeds max_bytes %d", target.Redacted(), h.MaxBytes)
		}
		return httpMeta(resp.Header, size), nil
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		rc, meta, ok, err := h.openRange(ctx, target)
		if err != nil {
			return Meta{}, err
		}
		if ok {
			_ = rc.Close()
			return meta, nil
		}
		return Meta{}, fmt.Errorf("http source %q: metadata unavailable", target.Redacted())
	case http.StatusNotFound:
		return Meta{}, fmt.Errorf("%w: upstream 404", ErrNotFound)
	default:
		return Meta{}, fmt.Errorf("http source %q: upstream status %d", target.Redacted(), resp.StatusCode)
	}
}

func (h *HTTPOpener) parseTarget(identifier string) (*url.URL, error) {
	if identifier == "" {
		return nil, fmt.Errorf("%w: empty identifier", ErrNotFound)
	}
	target, err := url.Parse(identifier)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("%w: identifier must be an http(s) URL", ErrNotFound)
	}
	if !h.hostAllowed(target.Hostname()) {
		return nil, fmt.Errorf("%w: host %q not in allow-list", ErrNotFound, target.Hostname())
	}
	return target, nil
}

func (h *HTTPOpener) openRange(ctx context.Context, target *url.URL) (io.ReadSeekCloser, Meta, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, Meta{}, false, fmt.Errorf("http source: %w", err)
	}
	req.Header.Set("User-Agent", h.UserAgent)
	req.Header.Set("Accept", "image/*")
	req.Header.Set("Range", "bytes=0-0")

	resp, err := h.client().Do(req)
	if err != nil {
		return nil, Meta{}, false, fmt.Errorf("http source range fetch %q: %w", target.Redacted(), err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK:
		return nil, Meta{}, false, nil
	case http.StatusNotFound:
		return nil, Meta{}, true, fmt.Errorf("%w: upstream 404", ErrNotFound)
	default:
		return nil, Meta{}, true, fmt.Errorf("http source %q: upstream status %d", target.Redacted(), resp.StatusCode)
	}
	size, ok := parseContentRangeSize(resp.Header.Get("Content-Range"))
	if !ok {
		return nil, Meta{}, false, nil
	}
	if h.MaxBytes > 0 && size > h.MaxBytes {
		return nil, Meta{}, true, fmt.Errorf("http source %q: response exceeds max_bytes %d", target.Redacted(), h.MaxBytes)
	}
	meta := Meta{
		ContentType: resp.Header.Get("Content-Type"),
		Size:        size,
		Version:     httpVersion(resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), size),
	}
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		meta.ModTime = t
	}
	return &httpRangeReadSeekCloser{
		ctx:    ctx,
		client: h.client(),
		target: target.String(),
		ua:     h.UserAgent,
		accept: "image/*",
		size:   size,
	}, meta, true, nil
}

func (h *HTTPOpener) client() *http.Client {
	if h.Client == nil {
		return &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: h.checkRedirect,
			Transport:     h.transport(),
		}
	}
	c := *h.Client
	c.CheckRedirect = h.checkRedirect
	if c.Transport == nil {
		c.Transport = h.transport()
	}
	return &c
}

func (h *HTTPOpener) transport() http.RoundTripper {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	if !h.AllowPrivateHosts {
		base.DialContext = h.dialPublicContext
	}
	return base
}

func (h *HTTPOpener) dialPublicContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, addr := range ips {
		if privateAddressBlocked(addr.IP) {
			return nil, fmt.Errorf("%w: host %q resolves to non-public address %s", ErrNotFound, host, addr.IP)
		}
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
}

func (h *HTTPOpener) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return http.ErrUseLastResponse
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("%w: redirect scheme %q not allowed", ErrNotFound, req.URL.Scheme)
	}
	if !h.hostAllowed(req.URL.Hostname()) {
		return fmt.Errorf("%w: redirect host %q not in allow-list", ErrNotFound, req.URL.Hostname())
	}
	return nil
}

func httpVersion(etag, lastModified string, size int64) string {
	etag = strings.TrimSpace(etag)
	if etag != "" {
		return "http:etag:" + etag
	}
	lastModified = strings.TrimSpace(lastModified)
	if lastModified != "" {
		return "http:last-modified:" + lastModified + ":" + strconv.FormatInt(size, 10)
	}
	return ""
}

func httpMeta(header http.Header, size int64) Meta {
	meta := Meta{
		ContentType: header.Get("Content-Type"),
		Size:        size,
		Version:     httpVersion(header.Get("ETag"), header.Get("Last-Modified"), size),
	}
	if t, err := http.ParseTime(header.Get("Last-Modified")); err == nil {
		meta.ModTime = t
	}
	return meta
}

func parseContentRangeSize(v string) (int64, bool) {
	slash := strings.LastIndexByte(v, '/')
	if slash < 0 || slash == len(v)-1 {
		return 0, false
	}
	if v[slash+1:] == "*" {
		return 0, false
	}
	n, err := strconv.ParseInt(v[slash+1:], 10, 64)
	return n, err == nil && n >= 0
}

func (h *HTTPOpener) hostAllowed(host string) bool {
	if len(h.AllowedHosts) == 0 {
		return false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if slices.Contains(h.AllowedHosts, "*") {
		return true
	}
	for _, allowed := range h.AllowedHosts {
		if strings.ToLower(strings.TrimSpace(allowed)) == host {
			return true
		}
	}
	return false
}

func privateAddressBlocked(ip net.IP) bool {
	return ip == nil ||
		ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast()
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

type httpRangeReadSeekCloser struct {
	ctx    context.Context
	client *http.Client
	target string
	ua     string
	accept string
	size   int64
	off    int64
	closed bool
}

func (r *httpRangeReadSeekCloser) Read(p []byte) (int, error) {
	if r.closed {
		return 0, errors.New("http range reader: closed")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.off >= r.size {
		return 0, io.EOF
	}
	if max := r.size - r.off; int64(len(p)) > max {
		p = p[:int(max)]
	}
	end := r.off + int64(len(p)) - 1
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.target, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", r.ua)
	req.Header.Set("Accept", r.accept)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.off, end))
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("http range reader: upstream status %d", resp.StatusCode)
	}
	n, err := io.ReadFull(resp.Body, p)
	r.off += int64(n)
	if err == io.ErrUnexpectedEOF {
		return n, io.EOF
	}
	return n, err
}

func (r *httpRangeReadSeekCloser) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.off + offset
	case io.SeekEnd:
		abs = r.size + offset
	default:
		return 0, errors.New("seek: invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("seek: negative position")
	}
	r.off = abs
	return abs, nil
}

func (r *httpRangeReadSeekCloser) Close() error {
	r.closed = true
	return nil
}
