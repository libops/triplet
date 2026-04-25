package cache

import "testing"

func TestGCSStoreObjectName(t *testing.T) {
	st := &GCSStore{prefix: "derivatives"}
	if got := st.objectName("abc"); got != "derivatives/abc" {
		t.Fatalf("objectName = %q", got)
	}
}

func TestBucketFromURL(t *testing.T) {
	got, err := bucketFromURL("gs://cache-bucket")
	if err != nil {
		t.Fatal(err)
	}
	if got != "cache-bucket" {
		t.Fatalf("bucket = %q", got)
	}
}
