package parse

import (
	"errors"
	"testing"
)

func TestParseRouting(t *testing.T) {
	tests := []struct {
		path string
		want Kind
		id   string
	}{
		{"abc", KindBase, "abc"},
		{"/abc", KindBase, "abc"},
		{"abc/info.json", KindInfo, "abc"},
		{"my%2Fid/info.json", KindInfo, "my/id"},
		{"abc/full/max/0/default.jpg", KindImage, "abc"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			r, err := Parse(tc.path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Kind != tc.want {
				t.Errorf("Kind = %v, want %v", r.Kind, tc.want)
			}
			if r.Identifier != tc.id {
				t.Errorf("Identifier = %q, want %q", r.Identifier, tc.id)
			}
		})
	}
}

func TestParseRoutingError(t *testing.T) {
	for _, p := range []string{"", "/", "abc/garbage", "a/b/c/d", "a/b/c/d/e/f"} {
		t.Run(p, func(t *testing.T) {
			_, err := Parse(p)
			if !errors.Is(err, ErrSyntax) {
				t.Fatalf("err = %v, want ErrSyntax", err)
			}
		})
	}
}

func TestParseRegion(t *testing.T) {
	tests := []struct {
		in      string
		want    Region
		wantErr bool
	}{
		{in: "full", want: Region{Kind: RegionFull}},
		{in: "square", want: Region{Kind: RegionSquare}},
		{in: "0,0,100,100", want: Region{Kind: RegionPixels, W: 100, H: 100}},
		{in: "10,20,30,40", want: Region{Kind: RegionPixels, X: 10, Y: 20, W: 30, H: 40}},
		{in: "pct:0,0,50,50", want: Region{Kind: RegionPercent, W: 50, H: 50}},
		{in: "pct:10,20,30.5,40.25", want: Region{Kind: RegionPercent, X: 10, Y: 20, W: 30.5, H: 40.25}},
		{in: "", wantErr: true},
		{in: "0,0,0,0", wantErr: true},
		{in: "0,0,-1,1", wantErr: true},
		{in: "1,2,3", wantErr: true},
		{in: "pct:0,0,150,10", wantErr: true},
		{in: "pct:abc,0,1,1", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseRegion(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in      string
		want    Size
		wantErr bool
	}{
		{in: "max", want: Size{Kind: SizeMax}},
		{in: "^max", want: Size{Kind: SizeMaxUp, Upscale: true}},
		{in: "100,", want: Size{Kind: SizeWidth, W: 100}},
		{in: ",200", want: Size{Kind: SizeHeight, H: 200}},
		{in: "100,200", want: Size{Kind: SizeWH, W: 100, H: 200}},
		{in: "^100,200", want: Size{Kind: SizeWH, W: 100, H: 200, Upscale: true}},
		{in: "!100,200", want: Size{Kind: SizeBestFit, W: 100, H: 200}},
		{in: "^!100,200", want: Size{Kind: SizeBestFit, W: 100, H: 200, Upscale: true}},
		{in: "pct:50", want: Size{Kind: SizePercent, Percent: 50}},
		{in: "^pct:200", want: Size{Kind: SizePercent, Percent: 200, Upscale: true}},
		{in: "", wantErr: true},
		{in: ",", wantErr: true},
		{in: "0,0", wantErr: true},
		{in: "-1,10", wantErr: true},
		{in: "pct:0", wantErr: true},
		{in: "pct:-1", wantErr: true},
		{in: "abc,def", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseSize(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseRotation(t *testing.T) {
	tests := []struct {
		in      string
		want    Rotation
		wantErr bool
	}{
		{in: "0", want: Rotation{}},
		{in: "90", want: Rotation{Degrees: 90}},
		{in: "180", want: Rotation{Degrees: 180}},
		{in: "359.99", want: Rotation{Degrees: 359.99}},
		{in: "!0", want: Rotation{Mirror: true}},
		{in: "!90", want: Rotation{Degrees: 90, Mirror: true}},
		{in: "", wantErr: true},
		{in: "-1", wantErr: true},
		{in: "361", wantErr: true},
		{in: "abc", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseRotation(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseQualityFormat(t *testing.T) {
	r, err := Parse("abc/full/max/0/color.png")
	if err != nil {
		t.Fatal(err)
	}
	if r.Quality != QualityColor || r.Format != FormatPNG {
		t.Fatalf("got quality=%q format=%q", r.Quality, r.Format)
	}
	for _, p := range []string{
		"abc/full/max/0/.jpg",
		"abc/full/max/0/default.",
		"abc/full/max/0/default",
		"abc/full/max/0/bogus.jpg",
		"abc/full/max/0/default.bmp",
	} {
		if _, err := Parse(p); err == nil {
			t.Errorf("expected error for %q", p)
		}
	}
}
