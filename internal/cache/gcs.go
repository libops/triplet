package cache

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
)

// GCSStore stores cached bytes in a Google Cloud Storage bucket.
type GCSStore struct {
	client *gcs.Client
	bucket string
	prefix string
}

// NewGCSStore constructs a GCS-backed cache store. bucketURL must be gs://bucket.
func NewGCSStore(ctx context.Context, bucketURL, prefix string) (*GCSStore, error) {
	bucket, err := bucketFromURL(bucketURL)
	if err != nil {
		return nil, err
	}
	client, err := gcs.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("cache gcs store: %w", err)
	}
	return &GCSStore{client: client, bucket: bucket, prefix: strings.Trim(prefix, "/")}, nil
}

// Close releases resources held by the GCS client.
func (s *GCSStore) Close() error {
	return s.client.Close()
}

// Get implements Store.
func (s *GCSStore) Get(ctx context.Context, key string) (io.ReadCloser, Entry, error) {
	obj := s.client.Bucket(s.bucket).Object(s.objectName(key))
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		if isGCSNotFound(err) {
			return nil, Entry{}, ErrMiss
		}
		return nil, Entry{}, err
	}
	r, err := obj.NewReader(ctx)
	if err != nil {
		if isGCSNotFound(err) {
			return nil, Entry{}, ErrMiss
		}
		return nil, Entry{}, err
	}
	return r, Entry{
		ContentType: attrs.ContentType,
		Size:        attrs.Size,
		StoredAt:    attrs.Updated,
	}, nil
}

// Put implements Store.
func (s *GCSStore) Put(ctx context.Context, key, contentType string, value io.Reader) error {
	w := s.client.Bucket(s.bucket).Object(s.objectName(key)).NewWriter(ctx)
	w.ContentType = contentType
	if _, err := io.Copy(w, value); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// Delete implements Store.
func (s *GCSStore) Delete(ctx context.Context, key string) error {
	err := s.client.Bucket(s.bucket).Object(s.objectName(key)).Delete(ctx)
	if isGCSNotFound(err) {
		return nil
	}
	return err
}

func (s *GCSStore) objectName(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
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

func isGCSNotFound(err error) bool {
	if e, ok := err.(*googleapi.Error); ok && e.Code == 404 {
		return true
	}
	return false
}
