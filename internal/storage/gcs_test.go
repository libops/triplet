package storage

import "testing"

func TestBucketFromURL(t *testing.T) {
	got, err := bucketFromURL("gs://example-bucket")
	if err != nil {
		t.Fatal(err)
	}
	if got != "example-bucket" {
		t.Fatalf("bucket = %q", got)
	}
}

func TestObjectKey(t *testing.T) {
	got, err := objectKey("images", "gs://example-bucket/path/to/sample.tif")
	if err != nil {
		t.Fatal(err)
	}
	if got != "images/path/to/sample.tif" {
		t.Fatalf("key = %q", got)
	}
}
