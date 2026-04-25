package handler

import "testing"

func TestDerivativeETagStable(t *testing.T) {
	a := derivativeETag("iiif/3/example/full/max/0/default.png")
	b := derivativeETag("iiif/3/example/full/max/0/default.png")
	c := derivativeETag("iiif/3/example/full/max/90/default.png")

	if a != b {
		t.Fatalf("etag not stable: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("etag should differ for different keys: %q", a)
	}
	if len(a) != 66 || a[0] != '"' || a[len(a)-1] != '"' {
		t.Fatalf("etag format = %q", a)
	}
}

func TestIfNoneMatchMatches(t *testing.T) {
	etag := `"abc123"`

	tests := []struct {
		name   string
		values []string
		want   bool
	}{
		{name: "single exact", values: []string{etag}, want: true},
		{name: "comma separated exact", values: []string{`"other", ` + etag}, want: true},
		{name: "wildcard", values: []string{"*"}, want: true},
		{name: "weak mismatch", values: []string{`W/"abc123"`}, want: false},
		{name: "no match", values: []string{`"other"`}, want: false},
		{name: "empty", values: nil, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ifNoneMatchMatches(tc.values, etag); got != tc.want {
				t.Fatalf("got %v want %v for %#v", got, tc.want, tc.values)
			}
		})
	}
}
