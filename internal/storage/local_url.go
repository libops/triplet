package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/libops/triplet/internal/redact"
)

// LocalURLFallback tries a local filesystem lookup for selected URL
// identifiers before falling back to another opener, usually HTTP.
type LocalURLFallback struct {
	Prefixes []string
	File     *FileOpener
	OCFL     bool
	Mappings []LocalURLMapping
	// AllowedOrigins gates path-only mappings for absolute HTTP identifiers.
	AllowedOrigins []string
	Fallback       Opener
	Logger         *slog.Logger
	// AuthFallback is used for matched auth-probed URL mappings when local
	// lookup misses. It should bypass shared source caches.
	AuthFallback *HTTPOpener
	authMu       sync.Mutex
	auth         map[string]authCacheEntry
	inflight     map[string][]chan error

	authFallbackMetaInflight map[string]*authFallbackMetaCall
	authFallbackOpenInflight map[string]*authFallbackOpenCall
}

// LocalURLMapping maps a URL identifier prefix to a local file source.
type LocalURLMapping struct {
	Prefix              string
	File                *FileOpener
	OCFL                bool
	AuthProbe           bool
	AuthCacheTTL        time.Duration
	AuthCacheMaxEntries int
}

type authCacheEntry struct {
	scope      string
	identifier string
	err        error
	expiresAt  time.Time
}

type authFallbackMetaCall struct {
	done chan struct{}
	meta Meta
	err  error
}

type authFallbackOpenCall struct {
	done       chan struct{}
	meta       Meta
	path       string
	err        error
	refs       int
	doneClosed bool
}

type sharedAuthFallbackFile struct {
	*os.File
	releaseOnce sync.Once
	release     func()
}

func (s *sharedAuthFallbackFile) Close() error {
	err := s.File.Close()
	s.releaseOnce.Do(s.release)
	return err
}

var errAuthProbeHeadUnsupported = errors.New("auth probe head unsupported")

type authCacheTier string

const (
	authCacheTierAnonymous     authCacheTier = "anonymous"
	authCacheTierAuthenticated authCacheTier = "authenticated"
	defaultAuthCacheMaxEntries               = 4096
)

// Open implements Opener.
func (l *LocalURLFallback) Open(ctx context.Context, identifier string) (io.ReadSeekCloser, Meta, error) {
	rc, meta, ok, err := l.openLocal(ctx, identifier)
	if ok || err != nil {
		return rc, meta, err
	}
	l.debug(ctx, "local url mapping miss; using fallback", identifier, "operation", "open")
	return l.Fallback.Open(ctx, identifier)
}

// Meta implements MetaReader when the fallback supports metadata lookups.
func (l *LocalURLFallback) Meta(ctx context.Context, identifier string) (Meta, error) {
	rc, meta, ok, err := l.openLocalMeta(ctx, identifier)
	if err != nil {
		return Meta{}, err
	}
	if ok {
		if rc != nil {
			_ = rc.Close()
		}
		return meta, nil
	}
	metaReader, ok := l.Fallback.(MetaReader)
	if !ok {
		l.debug(ctx, "local url mapping miss; using fallback", identifier, "operation", "meta")
		return Meta{}, fmt.Errorf("metadata unavailable for identifier")
	}
	l.debug(ctx, "local url mapping miss; using fallback", identifier, "operation", "meta")
	return metaReader.Meta(ctx, identifier)
}

func (l *LocalURLFallback) openLocalMeta(ctx context.Context, identifier string) (io.ReadSeekCloser, Meta, bool, error) {
	if l == nil {
		return nil, Meta{}, false, nil
	}
	for _, mapping := range l.localMappings() {
		if mapping.File == nil {
			continue
		}
		path, ok := l.stripLocalURLPrefix(identifier, mapping.Prefix)
		if !ok {
			l.debug(ctx, "local url mapping skipped", identifier, "operation", "meta", "prefix", mapping.Prefix)
			continue
		}
		l.debug(ctx, "local url mapping matched", identifier, "operation", "meta", "prefix", mapping.Prefix, "path", path, "disk_path", mappingDiskPath(mapping, path), "ocfl", mapping.OCFL, "auth_probe", mapping.AuthProbe)
		if mapping.OCFL {
			_, meta, err := l.ocflMeta(mapping, path)
			if errors.Is(err, ErrNotFound) {
				l.debug(ctx, "local url ocfl object missing", identifier, "operation", "meta", "prefix", mapping.Prefix, "path", path, "disk_path", mappingDiskPath(mapping, path))
				if mapping.AuthProbe && l.AuthFallback != nil {
					l.debug(ctx, "local url using authenticated fallback after local miss", identifier, "operation", "meta", "prefix", mapping.Prefix)
					return l.openAuthFallback(ctx, identifier, mapping, true)
				}
				continue
			}
			if err == nil {
				if authErr := l.authorize(ctx, identifier, mapping); authErr != nil {
					l.debug(ctx, "local url auth denied local file", identifier, "operation", "meta", "prefix", mapping.Prefix, "err", authErr)
					return nil, Meta{}, false, authErr
				}
				l.debug(ctx, "local url serving local metadata", identifier, "operation", "meta", "prefix", mapping.Prefix, "path", path)
				return nil, meta, true, nil
			}
			l.debug(ctx, "local url ocfl metadata error", identifier, "operation", "meta", "prefix", mapping.Prefix, "path", path, "err", err)
			return nil, meta, true, err
		}
		meta, err := mapping.File.Meta(ctx, path)
		if errors.Is(err, ErrNotFound) {
			l.debug(ctx, "local url file missing", identifier, "operation", "meta", "prefix", mapping.Prefix, "path", path, "disk_path", mappingDiskPath(mapping, path))
			if mapping.AuthProbe && l.AuthFallback != nil {
				l.debug(ctx, "local url using authenticated fallback after local miss", identifier, "operation", "meta", "prefix", mapping.Prefix)
				return l.openAuthFallback(ctx, identifier, mapping, true)
			}
			continue
		}
		if err == nil {
			if authErr := l.authorize(ctx, identifier, mapping); authErr != nil {
				l.debug(ctx, "local url auth denied local file", identifier, "operation", "meta", "prefix", mapping.Prefix, "err", authErr)
				return nil, Meta{}, false, authErr
			}
			l.debug(ctx, "local url serving local metadata", identifier, "operation", "meta", "prefix", mapping.Prefix, "path", path, "disk_path", mappingDiskPath(mapping, path))
			return nil, meta, true, nil
		}
		l.debug(ctx, "local url file metadata error", identifier, "operation", "meta", "prefix", mapping.Prefix, "path", path, "disk_path", mappingDiskPath(mapping, path), "err", err)
		return nil, meta, true, err
	}
	return nil, Meta{}, false, nil
}

func (l *LocalURLFallback) openLocal(ctx context.Context, identifier string) (io.ReadSeekCloser, Meta, bool, error) {
	if l == nil {
		return nil, Meta{}, false, nil
	}
	for _, mapping := range l.localMappings() {
		if mapping.File == nil {
			continue
		}
		path, ok := l.stripLocalURLPrefix(identifier, mapping.Prefix)
		if !ok {
			l.debug(ctx, "local url mapping skipped", identifier, "operation", "open", "prefix", mapping.Prefix)
			continue
		}
		l.debug(ctx, "local url mapping matched", identifier, "operation", "open", "prefix", mapping.Prefix, "path", path, "disk_path", mappingDiskPath(mapping, path), "ocfl", mapping.OCFL, "auth_probe", mapping.AuthProbe)
		if mapping.OCFL {
			diskPath, meta, err := l.ocflMeta(mapping, path)
			if errors.Is(err, ErrNotFound) {
				l.debug(ctx, "local url ocfl object missing", identifier, "operation", "open", "prefix", mapping.Prefix, "path", path, "disk_path", mappingDiskPath(mapping, path))
				if mapping.AuthProbe && l.AuthFallback != nil {
					l.debug(ctx, "local url using authenticated fallback after local miss", identifier, "operation", "open", "prefix", mapping.Prefix)
					return l.openAuthFallback(ctx, identifier, mapping, false)
				}
				continue
			}
			if err == nil {
				if authErr := l.authorize(ctx, identifier, mapping); authErr != nil {
					l.debug(ctx, "local url auth denied local file", identifier, "operation", "open", "prefix", mapping.Prefix, "err", authErr)
					return nil, Meta{}, false, authErr
				}
				rc, err := os.Open(diskPath)
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						l.debug(ctx, "local url ocfl content missing", identifier, "operation", "open", "prefix", mapping.Prefix, "path", path, "disk_path", diskPath)
						continue
					}
					return nil, Meta{}, true, fmt.Errorf("open ocfl file %q: %w", path, err)
				}
				l.debug(ctx, "local url serving local file", identifier, "operation", "open", "prefix", mapping.Prefix, "path", path, "disk_path", diskPath)
				return rc, meta, true, nil
			}
			l.debug(ctx, "local url ocfl metadata error", identifier, "operation", "open", "prefix", mapping.Prefix, "path", path, "err", err)
			return nil, meta, true, err
		}
		meta, err := mapping.File.Meta(ctx, path)
		if errors.Is(err, ErrNotFound) {
			l.debug(ctx, "local url file missing", identifier, "operation", "open", "prefix", mapping.Prefix, "path", path, "disk_path", mappingDiskPath(mapping, path))
			if mapping.AuthProbe && l.AuthFallback != nil {
				l.debug(ctx, "local url using authenticated fallback after local miss", identifier, "operation", "open", "prefix", mapping.Prefix)
				return l.openAuthFallback(ctx, identifier, mapping, false)
			}
			continue
		}
		if err == nil {
			if authErr := l.authorize(ctx, identifier, mapping); authErr != nil {
				l.debug(ctx, "local url auth denied local file", identifier, "operation", "open", "prefix", mapping.Prefix, "err", authErr)
				return nil, Meta{}, false, authErr
			}
			rc, meta, err := mapping.File.Open(ctx, path)
			if errors.Is(err, ErrNotFound) {
				l.debug(ctx, "local url file disappeared before open", identifier, "operation", "open", "prefix", mapping.Prefix, "path", path, "disk_path", mappingDiskPath(mapping, path))
				continue
			}
			if err == nil {
				l.debug(ctx, "local url serving local file", identifier, "operation", "open", "prefix", mapping.Prefix, "path", path, "disk_path", mappingDiskPath(mapping, path))
			}
			return rc, meta, true, err
		}
		l.debug(ctx, "local url file metadata error", identifier, "operation", "open", "prefix", mapping.Prefix, "path", path, "disk_path", mappingDiskPath(mapping, path), "err", err)
		return nil, meta, true, err
	}
	return nil, Meta{}, false, nil
}

func (l *LocalURLFallback) localMappings() []LocalURLMapping {
	if len(l.Mappings) > 0 {
		return l.Mappings
	}
	mappings := make([]LocalURLMapping, 0, len(l.Prefixes))
	for _, prefix := range l.Prefixes {
		mappings = append(mappings, LocalURLMapping{
			Prefix: prefix,
			File:   l.File,
			OCFL:   l.OCFL,
		})
	}
	return mappings
}

func (l *LocalURLFallback) openAuthFallback(ctx context.Context, identifier string, mapping LocalURLMapping, metaOnly bool) (io.ReadSeekCloser, Meta, bool, error) {
	headers := authHeadersFromContext(ctx)
	if metaOnly {
		meta, err := l.coalescedAuthFallbackMeta(ctx, authFallbackKey("meta", mapping.Prefix, identifier, headers), identifier)
		return nil, meta, true, err
	}
	rc, meta, err := l.coalescedAuthFallbackOpen(ctx, authFallbackKey("open", mapping.Prefix, identifier, headers), identifier)
	return rc, meta, true, err
}

func (l *LocalURLFallback) coalescedAuthFallbackMeta(ctx context.Context, key, identifier string) (Meta, error) {
	call, leader := l.beginAuthFallbackMeta(key)
	if leader {
		meta, err := l.AuthFallback.Meta(context.WithoutCancel(ctx), identifier)
		l.finishAuthFallbackMeta(key, call, meta, err)
		return meta, err
	}
	select {
	case <-call.done:
		return call.meta, call.err
	case <-ctx.Done():
		return Meta{}, ctx.Err()
	}
}

func (l *LocalURLFallback) coalescedAuthFallbackOpen(ctx context.Context, key, identifier string) (io.ReadSeekCloser, Meta, error) {
	call, leader := l.beginAuthFallbackOpen(key)
	if leader {
		path, meta, err := l.fetchAuthFallbackSource(context.WithoutCancel(ctx), identifier)
		l.finishAuthFallbackOpen(key, call, meta, path, err)
		return l.openAuthFallbackResult(call)
	}
	select {
	case <-call.done:
		return l.openAuthFallbackResult(call)
	case <-ctx.Done():
		l.releaseAuthFallbackOpen(call)
		return nil, Meta{}, ctx.Err()
	}
}

func (l *LocalURLFallback) fetchAuthFallbackSource(ctx context.Context, identifier string) (string, Meta, error) {
	rc, meta, err := l.AuthFallback.Open(ctx, identifier)
	if err != nil {
		return "", Meta{}, err
	}
	defer rc.Close()

	tmp, err := os.CreateTemp("", "triplet-auth-fallback-source-*")
	if err != nil {
		return "", Meta{}, fmt.Errorf("auth fallback source temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	n, err := io.Copy(tmp, rc)
	if err != nil {
		cleanup()
		return "", Meta{}, fmt.Errorf("auth fallback source read: %w", err)
	}
	if meta.Size == 0 {
		meta.Size = n
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", Meta{}, fmt.Errorf("auth fallback source close: %w", err)
	}
	return tmpName, meta, nil
}

func (l *LocalURLFallback) openAuthFallbackResult(call *authFallbackOpenCall) (io.ReadSeekCloser, Meta, error) {
	if call.err != nil {
		l.releaseAuthFallbackOpen(call)
		return nil, Meta{}, call.err
	}
	f, err := os.Open(call.path)
	if err != nil {
		l.releaseAuthFallbackOpen(call)
		return nil, Meta{}, fmt.Errorf("auth fallback source open: %w", err)
	}
	return &sharedAuthFallbackFile{
		File:    f,
		release: func() { l.releaseAuthFallbackOpen(call) },
	}, call.meta, nil
}

func (l *LocalURLFallback) stripLocalURLPrefix(identifier, prefix string) (string, bool) {
	if prefix == "" {
		return "", false
	}
	if unescaped, err := url.PathUnescape(prefix); err == nil {
		prefix = unescaped
	}
	prefix = strings.TrimRight(prefix, "/")
	if strings.HasPrefix(prefix, "/") {
		if u, err := url.Parse(identifier); err == nil && u.Scheme != "" && u.Host != "" {
			if !localURLOriginAllowed(u, l.AllowedOrigins) {
				return "", false
			}
			return stripLocalPathPrefix(u.Path, prefix)
		}
		return stripLocalPathPrefix(identifier, prefix)
	}
	return stripLocalPathPrefix(identifier, prefix)
}

func localURLOriginAllowed(u *url.URL, allowedOrigins []string) bool {
	if len(allowedOrigins) == 0 {
		return false
	}
	origin := urlOrigin(u)
	for _, allowed := range allowedOrigins {
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

func stripLocalPathPrefix(identifier, prefix string) (string, bool) {
	if identifier != prefix && !strings.HasPrefix(identifier, prefix+"/") {
		return "", false
	}
	path := strings.TrimPrefix(identifier, prefix)
	path = strings.TrimLeft(path, "/")
	if path == "" {
		return "", false
	}
	return path, true
}

func (l *LocalURLFallback) authorize(ctx context.Context, identifier string, mapping LocalURLMapping) error {
	if !mapping.AuthProbe {
		return nil
	}
	headers := authHeadersFromContext(ctx)

	anonKey := authCacheKey(authCacheTierAnonymous, mapping.Prefix, identifier, nil)
	if err, ok := l.cachedAuth(anonKey); ok {
		if err == nil || !hasAuthHeaders(headers) {
			return err
		}
		return l.authorizeAuthenticated(ctx, identifier, mapping, headers)
	}
	anonErr := l.probeCached(ctx, anonKey, identifier, nil, mapping.anonymousAuthTTL(), mapping.authCacheMaxEntries())
	if anonErr == nil || !hasAuthHeaders(headers) {
		return anonErr
	}
	if !errors.Is(anonErr, ErrForbidden) && !errors.Is(anonErr, ErrNotFound) {
		return anonErr
	}
	return l.authorizeAuthenticated(ctx, identifier, mapping, headers)
}

func (l *LocalURLFallback) authorizeAuthenticated(ctx context.Context, identifier string, mapping LocalURLMapping, headers http.Header) error {
	key := authCacheKey(authCacheTierAuthenticated, mapping.Prefix, identifier, headers)
	if err, ok := l.cachedAuth(key); ok {
		return err
	}
	return l.probeCached(ctx, key, identifier, headers, mapping.authenticatedAuthTTL(), mapping.authCacheMaxEntries())
}

func (l *LocalURLFallback) probeCached(ctx context.Context, key, identifier string, headers http.Header, ttl time.Duration, maxEntries int) error {
	if wait, ok := l.beginAuthProbe(key); ok {
		l.debug(ctx, "local url auth probe waiting on in-flight probe", identifier, "cache_key", redact.Hash(key))
		select {
		case err := <-wait:
			l.debug(ctx, "local url auth probe in-flight result", identifier, "cache_key", redact.Hash(key), "err", err)
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	_, err := l.probe(ctx, identifier, headers)
	cacheable := cacheableAuthProbeResult(err)
	l.debug(ctx, "local url auth probe completed", identifier, "cache_key", redact.Hash(key), "cacheable", cacheable, "ttl", ttl.String(), "err", err)
	if ttl > 0 && cacheable {
		l.storeAuth(key, authCacheScope(key), identifier, err, ttl, maxEntries)
	}
	l.finishAuthProbe(key, err)
	return err
}

func authCacheKey(tier authCacheTier, prefix, identifier string, headers http.Header) string {
	prefixSum := sha256.Sum256([]byte(prefix))
	sum := sha256.New()
	_, _ = sum.Write([]byte(tier))
	_, _ = sum.Write([]byte{0})
	_, _ = sum.Write([]byte(prefix))
	_, _ = sum.Write([]byte{0})
	_, _ = sum.Write([]byte(identifier))
	_, _ = sum.Write([]byte{0})
	for _, name := range []string{"Authorization", "Cookie"} {
		values := append([]string(nil), headers.Values(name)...)
		slices.Sort(values)
		_, _ = sum.Write([]byte(name))
		_, _ = sum.Write([]byte{0})
		for _, value := range values {
			_, _ = sum.Write([]byte(value))
			_, _ = sum.Write([]byte{0})
		}
	}
	return string(tier) + "|" + hex.EncodeToString(prefixSum[:]) + ":" + hex.EncodeToString(sum.Sum(nil))
}

func authFallbackKey(operation, prefix, identifier string, headers http.Header) string {
	tier := authCacheTierAnonymous
	var scopedHeaders http.Header
	if hasAuthHeaders(headers) {
		tier = authCacheTierAuthenticated
		scopedHeaders = headers
	}
	return "auth-fallback|" + operation + "|" + authCacheKey(tier, prefix, identifier, scopedHeaders)
}

func authCacheScope(key string) string {
	if i := strings.IndexByte(key, ':'); i >= 0 {
		return key[:i]
	}
	return key
}

func hasAuthHeaders(headers http.Header) bool {
	return len(headers.Values("Authorization")) > 0 || len(headers.Values("Cookie")) > 0
}

func cacheableAuthProbeResult(err error) bool {
	return err == nil
}

func (m LocalURLMapping) anonymousAuthTTL() time.Duration {
	return m.AuthCacheTTL
}

func (m LocalURLMapping) authenticatedAuthTTL() time.Duration {
	return m.AuthCacheTTL
}

func (m LocalURLMapping) authCacheMaxEntries() int {
	if m.AuthCacheMaxEntries > 0 {
		return m.AuthCacheMaxEntries
	}
	return defaultAuthCacheMaxEntries
}

func (l *LocalURLFallback) cachedAuth(key string) (error, bool) {
	l.authMu.Lock()
	defer l.authMu.Unlock()
	entry, ok := l.auth[key]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(l.auth, key)
		}
		return nil, false
	}
	return entry.err, true
}

func (l *LocalURLFallback) storeAuth(key, scope, identifier string, err error, ttl time.Duration, maxEntries int) {
	l.authMu.Lock()
	defer l.authMu.Unlock()
	if l.auth == nil {
		l.auth = map[string]authCacheEntry{}
	}
	l.evictAuthLocked(scope, maxEntries)
	l.auth[key] = authCacheEntry{scope: scope, identifier: identifier, err: err, expiresAt: time.Now().Add(ttl)}
}

// InvalidateAuth clears cached authorization probe results for identifier.
func (l *LocalURLFallback) InvalidateAuth(_ context.Context, identifier string) error {
	l.authMu.Lock()
	defer l.authMu.Unlock()
	for key, entry := range l.auth {
		if entry.identifier == identifier {
			delete(l.auth, key)
		}
	}
	return nil
}

func (l *LocalURLFallback) evictAuthLocked(scope string, maxEntries int) {
	if maxEntries <= 0 {
		return
	}
	now := time.Now()
	for key, entry := range l.auth {
		if entry.scope == scope && now.After(entry.expiresAt) {
			delete(l.auth, key)
		}
	}
	for authCacheScopeLen(l.auth, scope) >= maxEntries {
		var oldestKey string
		var oldest time.Time
		for key, entry := range l.auth {
			if entry.scope != scope {
				continue
			}
			if oldestKey == "" || entry.expiresAt.Before(oldest) {
				oldestKey = key
				oldest = entry.expiresAt
			}
		}
		if oldestKey == "" {
			return
		}
		delete(l.auth, oldestKey)
	}
}

func authCacheScopeLen(entries map[string]authCacheEntry, scope string) int {
	n := 0
	for _, entry := range entries {
		if entry.scope == scope {
			n++
		}
	}
	return n
}

func (l *LocalURLFallback) beginAuthProbe(key string) (<-chan error, bool) {
	l.authMu.Lock()
	defer l.authMu.Unlock()
	if l.inflight == nil {
		l.inflight = map[string][]chan error{}
	}
	if waiters, ok := l.inflight[key]; ok {
		ch := make(chan error, 1)
		l.inflight[key] = append(waiters, ch)
		return ch, true
	}
	l.inflight[key] = nil
	return nil, false
}

func (l *LocalURLFallback) finishAuthProbe(key string, err error) {
	l.authMu.Lock()
	defer l.authMu.Unlock()
	waiters := l.inflight[key]
	delete(l.inflight, key)
	for _, ch := range waiters {
		ch <- err
		close(ch)
	}
}

func (l *LocalURLFallback) beginAuthFallbackMeta(key string) (*authFallbackMetaCall, bool) {
	l.authMu.Lock()
	defer l.authMu.Unlock()
	if l.authFallbackMetaInflight == nil {
		l.authFallbackMetaInflight = map[string]*authFallbackMetaCall{}
	}
	if call, ok := l.authFallbackMetaInflight[key]; ok {
		return call, false
	}
	call := &authFallbackMetaCall{done: make(chan struct{})}
	l.authFallbackMetaInflight[key] = call
	return call, true
}

func (l *LocalURLFallback) finishAuthFallbackMeta(key string, call *authFallbackMetaCall, meta Meta, err error) {
	l.authMu.Lock()
	defer l.authMu.Unlock()
	call.meta = meta
	call.err = err
	delete(l.authFallbackMetaInflight, key)
	close(call.done)
}

func (l *LocalURLFallback) beginAuthFallbackOpen(key string) (*authFallbackOpenCall, bool) {
	l.authMu.Lock()
	defer l.authMu.Unlock()
	if l.authFallbackOpenInflight == nil {
		l.authFallbackOpenInflight = map[string]*authFallbackOpenCall{}
	}
	if call, ok := l.authFallbackOpenInflight[key]; ok {
		call.refs++
		return call, false
	}
	call := &authFallbackOpenCall{done: make(chan struct{}), refs: 1}
	l.authFallbackOpenInflight[key] = call
	return call, true
}

func (l *LocalURLFallback) finishAuthFallbackOpen(key string, call *authFallbackOpenCall, meta Meta, path string, err error) {
	l.authMu.Lock()
	call.meta = meta
	call.path = path
	call.err = err
	call.doneClosed = true
	delete(l.authFallbackOpenInflight, key)
	removePath := call.refs == 0 && call.path != ""
	l.authMu.Unlock()

	close(call.done)
	if removePath {
		_ = os.Remove(path)
	}
}

func (l *LocalURLFallback) releaseAuthFallbackOpen(call *authFallbackOpenCall) {
	l.authMu.Lock()
	if call.refs > 0 {
		call.refs--
	}
	removePath := call.doneClosed && call.refs == 0 && call.path != ""
	path := call.path
	l.authMu.Unlock()
	if removePath {
		_ = os.Remove(path)
	}
}

func (l *LocalURLFallback) probe(ctx context.Context, identifier string, headers http.Header) (http.Header, error) {
	respHeader, err := l.probeRequest(ctx, http.MethodHead, identifier, headers)
	if err == nil || !errors.Is(err, errAuthProbeHeadUnsupported) {
		return respHeader, err
	}
	return l.probeRequest(ctx, http.MethodGet, identifier, headers)
}

func (l *LocalURLFallback) probeRequest(ctx context.Context, method, identifier string, headers http.Header) (http.Header, error) {
	prober := l.AuthFallback
	if prober == nil {
		return nil, fmt.Errorf("auth probe: authenticated HTTP fallback is required")
	}
	target, err := prober.parseTarget(identifier)
	if err != nil {
		l.debug(ctx, "local url auth probe target rejected", identifier, "method", method, "err", err)
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("auth probe: %w", err)
	}
	for _, name := range []string{"Authorization", "Cookie"} {
		for _, value := range headers.Values(name) {
			req.Header.Add(name, value)
		}
	}
	req.Header.Set("User-Agent", "triplet/0.1 auth-probe")
	if method == http.MethodGet {
		req.Header.Set("Range", "bytes=0-0")
	}
	l.debug(ctx, "local url auth probe request", identifier, "method", method, "url", target.Redacted(), "with_auth", hasAuthHeaders(headers))
	resp, err := prober.client().Do(req)
	if err != nil {
		l.debug(ctx, "local url auth probe request error", identifier, "method", method, "url", target.Redacted(), "err", err)
		return nil, fmt.Errorf("auth probe %q: %w", identifier, err)
	}
	defer resp.Body.Close()
	l.debug(ctx, "local url auth probe response", identifier, "method", method, "url", target.Redacted(), "status", resp.StatusCode)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return resp.Header, nil
	case http.StatusNotFound:
		return resp.Header, fmt.Errorf("%w: auth probe 404", ErrNotFound)
	case http.StatusUnauthorized, http.StatusForbidden:
		return resp.Header, fmt.Errorf("%w: auth probe status %d", ErrForbidden, resp.StatusCode)
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		if method == http.MethodHead {
			return resp.Header, errAuthProbeHeadUnsupported
		}
	}
	return resp.Header, fmt.Errorf("auth probe %q: upstream status %d", identifier, resp.StatusCode)
}

func (l *LocalURLFallback) debug(ctx context.Context, msg, identifier string, args ...any) {
	if l == nil || l.Logger == nil || !l.Logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	args = append(args, "identifier", redact.Identifier(identifier), "identifier_hash", redact.Hash(identifier))
	l.Logger.DebugContext(ctx, msg, args...)
}

func mappingDiskPath(mapping LocalURLMapping, path string) string {
	if mapping.File == nil {
		return ""
	}
	return filepath.Join(mapping.File.Root, filepath.FromSlash(path))
}

func (l *LocalURLFallback) ocflMeta(mapping LocalURLMapping, path string) (string, Meta, error) {
	diskPath, err := l.ocflDiskPath(mapping, path)
	if err != nil {
		return "", Meta{}, err
	}
	info, err := os.Stat(diskPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", Meta{}, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return "", Meta{}, fmt.Errorf("stat ocfl file %q: %w", path, err)
	}
	return diskPath, Meta{
		ContentType: mime.TypeByExtension(strings.ToLower(filepath.Ext(path))),
		Size:        info.Size(),
		ModTime:     info.ModTime(),
		Version:     fileVersion(diskPath, info),
	}, nil
}

func (l *LocalURLFallback) ocflDiskPath(mapping LocalURLMapping, path string) (string, error) {
	path = strings.TrimLeft(path, "/")
	if path == "" || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%w: invalid ocfl identifier", ErrNotFound)
	}
	ocflDir := ocflDir(mapping.File.Root, "info:fedora/"+path)
	inventoryPath := filepath.Join(ocflDir, "extensions", "0005-mutable-head", "head", "inventory.json")
	body, err := os.ReadFile(inventoryPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: ocfl inventory", ErrNotFound)
		}
		return "", fmt.Errorf("read ocfl inventory: %w", err)
	}
	var inv ocflInventory
	if err := json.Unmarshal(body, &inv); err != nil {
		return "", fmt.Errorf("parse ocfl inventory: %w", err)
	}
	state, ok := inv.Versions[inv.Head]
	if !ok {
		return "", fmt.Errorf("%w: ocfl head version missing", ErrNotFound)
	}
	filename := filepath.Base(path)
	for digest, files := range state.State {
		if !slicesContains(files, filename) {
			continue
		}
		manifestFiles := inv.Manifest[digest]
		if len(manifestFiles) == 0 || manifestFiles[0] == "" {
			continue
		}
		diskPath := filepath.Clean(filepath.Join(ocflDir, filepath.FromSlash(manifestFiles[0])))
		rel, err := filepath.Rel(ocflDir, diskPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%w: ocfl manifest path escapes object root", ErrForbidden)
		}
		realOCFLDir, err := filepath.EvalSymlinks(ocflDir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("%w: ocfl object root", ErrNotFound)
			}
			return "", fmt.Errorf("resolve ocfl object root: %w", err)
		}
		realDiskPath, err := filepath.EvalSymlinks(diskPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("%w: ocfl file missing", ErrNotFound)
			}
			return "", fmt.Errorf("resolve ocfl file: %w", err)
		}
		realRel, err := filepath.Rel(realOCFLDir, realDiskPath)
		if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%w: ocfl manifest path escapes object root", ErrForbidden)
		}
		return realDiskPath, nil
	}
	return "", fmt.Errorf("%w: ocfl file missing", ErrNotFound)
}

func ocflDir(root, objectID string) string {
	sum := sha256.Sum256([]byte(objectID))
	digest := hex.EncodeToString(sum[:])
	root = strings.TrimRight(root, string(filepath.Separator))
	path := root
	for i := 0; i < 9; i += 3 {
		path = filepath.Join(path, digest[i:i+3])
	}
	return filepath.Join(path, digest)
}

type ocflInventory struct {
	Head     string                 `json:"head"`
	Versions map[string]ocflVersion `json:"versions"`
	Manifest map[string][]string    `json:"manifest"`
}

type ocflVersion struct {
	State map[string][]string `json:"state"`
}

func slicesContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
