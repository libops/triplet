package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
)

// GCSOpener reads objects from a Google Cloud Storage bucket.
type GCSOpener struct {
	client   *gcs.Client
	bucket   string
	prefix   string
	maxBytes int64
}

// NewGCSOpener constructs a GCS source opener. bucketURL must be gs://bucket.
func NewGCSOpener(ctx context.Context, bucketURL, prefix string, maxBytes int64) (*GCSOpener, error) {
	bucket, err := bucketFromURL(bucketURL)
	if err != nil {
		return nil, err
	}
	client, err := gcs.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcs opener: %w", err)
	}
	return &GCSOpener{client: client, bucket: bucket, prefix: strings.Trim(prefix, "/"), maxBytes: maxBytes}, nil
}

// Close releases resources held by the GCS client.
func (o *GCSOpener) Close() error {
	return o.client.Close()
}

// Open implements Opener.
func (o *GCSOpener) Open(ctx context.Context, identifier string) (io.ReadSeekCloser, Meta, error) {
	key, err := objectKey(o.prefix, identifier)
	if err != nil {
		return nil, Meta{}, err
	}
	obj := o.client.Bucket(o.bucket).Object(key)
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		if isGCSNotFound(err) {
			return nil, Meta{}, ErrNotFound
		}
		return nil, Meta{}, err
	}
	if o.maxBytes > 0 && attrs.Size > o.maxBytes {
		return nil, Meta{}, fmt.Errorf("gcs source %q: response exceeds max_bytes %d", key, o.maxBytes)
	}
	r, err := obj.NewReader(ctx)
	if err != nil {
		if isGCSNotFound(err) {
			return nil, Meta{}, ErrNotFound
		}
		return nil, Meta{}, err
	}
	defer r.Close()

	f, err := os.CreateTemp("", "triplet-gcs-*")
	if err != nil {
		return nil, Meta{}, err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(f.Name())
			_ = f.Close()
		}
	}()
	var reader io.Reader = r
	if o.maxBytes > 0 {
		reader = io.LimitReader(r, o.maxBytes+1)
	}
	n, err := io.Copy(f, reader)
	if err != nil {
		return nil, Meta{}, err
	}
	if o.maxBytes > 0 && n > o.maxBytes {
		return nil, Meta{}, fmt.Errorf("gcs source %q: response exceeds max_bytes %d", key, o.maxBytes)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, Meta{}, err
	}
	return &tempReadSeekCloser{File: f}, Meta{
		ContentType: attrs.ContentType,
		Size:        attrs.Size,
		ModTime:     attrs.Updated,
		Version:     fmt.Sprintf("gcs:%d:%d", attrs.Generation, attrs.Metageneration),
	}, nil
}

func bucketFromURL(bucketURL string) (string, error) {
	u, err := url.Parse(bucketURL)
	if err != nil {
		return "", fmt.Errorf("gcs bucket url: %w", err)
	}
	if u.Scheme != "gs" || u.Host == "" || u.Path != "" {
		return "", fmt.Errorf("gcs bucket url: expected gs://bucket, got %q", bucketURL)
	}
	return u.Host, nil
}

func objectKey(prefix, identifier string) (string, error) {
	if identifier == "" || strings.ContainsAny(identifier, "\x00\n\r") {
		return "", ErrNotFound
	}
	if u, err := url.Parse(identifier); err == nil && u.Scheme == "gs" {
		identifier = strings.TrimPrefix(identifier, "gs://")
		if idx := strings.Index(identifier, "/"); idx >= 0 {
			identifier = identifier[idx+1:]
		} else {
			return "", ErrNotFound
		}
	}
	clean := path.Clean("/" + identifier)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." || strings.HasPrefix(clean, "../") {
		return "", ErrNotFound
	}
	if prefix == "" {
		return clean, nil
	}
	return strings.TrimPrefix(path.Join(prefix, clean), "/"), nil
}

func isGCSNotFound(err error) bool {
	if e, ok := err.(*googleapi.Error); ok && e.Code == 404 {
		return true
	}
	return false
}

type tempReadSeekCloser struct {
	*os.File
}

func (t *tempReadSeekCloser) Close() error {
	name := t.Name()
	err := t.File.Close()
	_ = os.Remove(name)
	return err
}
