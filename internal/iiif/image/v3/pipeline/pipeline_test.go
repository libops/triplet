package pipeline

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vg "github.com/cshum/vipsgen/vips"
	"github.com/libops/triplet/internal/iiif/image/v3/parse"
	"github.com/libops/triplet/internal/storage"
	tvips "github.com/libops/triplet/internal/vips"
)

func TestMain(m *testing.M) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tvips.Startup(tvips.Config{}, logger)
	code := m.Run()
	tvips.Shutdown()
	os.Exit(code)
}

func TestTransformResizePNG(t *testing.T) {
	p := newTestPipeline(t)
	req := mustParseImageRequest(t, "sample.png/full/100,/0/default.png")

	var buf bytes.Buffer
	res, err := p.Transform(context.Background(), req, &buf)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if res.ContentType != "image/png" {
		t.Fatalf("content-type = %q", res.ContentType)
	}
	if res.Width != 100 || res.Height != 50 {
		t.Fatalf("result dims = %dx%d", res.Width, res.Height)
	}

	img := decodePNG(t, buf.Bytes())
	if got := img.Bounds().Dx(); got != 100 {
		t.Fatalf("decoded width = %d", got)
	}
	if got := img.Bounds().Dy(); got != 50 {
		t.Fatalf("decoded height = %d", got)
	}
}

func TestTransformRegionRotate(t *testing.T) {
	p := newTestPipeline(t)
	req := mustParseImageRequest(t, "sample.png/0,0,100,50/max/90/default.png")

	var buf bytes.Buffer
	res, err := p.Transform(context.Background(), req, &buf)
	if err != nil {
		if strings.Contains(err.Error(), "jp2ksave") {
			t.Skipf("libvips JP2 encoder unavailable: %v", err)
		}
		t.Fatalf("transform: %v", err)
	}
	if res.Width != 50 || res.Height != 100 {
		t.Fatalf("result dims = %dx%d", res.Width, res.Height)
	}

	img := decodePNG(t, buf.Bytes())
	if got := img.Bounds().Dx(); got != 50 {
		t.Fatalf("decoded width = %d", got)
	}
	if got := img.Bounds().Dy(); got != 100 {
		t.Fatalf("decoded height = %d", got)
	}
}

func TestTransformGrayQuality(t *testing.T) {
	p := newTestPipeline(t)
	req := mustParseImageRequest(t, "sample.png/full/max/0/gray.png")

	var buf bytes.Buffer
	_, err := p.Transform(context.Background(), req, &buf)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}

	img := decodePNG(t, buf.Bytes())
	r, g, b, _ := img.At(10, 10).RGBA()
	if r != g || g != b {
		t.Fatalf("pixel not grayscale: r=%d g=%d b=%d", r, g, b)
	}
}

func TestTransformBitonalQuality(t *testing.T) {
	p := newTestPipeline(t)
	req := mustParseImageRequest(t, "sample.png/full/max/0/bitonal.png")

	var buf bytes.Buffer
	_, err := p.Transform(context.Background(), req, &buf)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}

	img := decodePNG(t, buf.Bytes())
	black := colorValue(img.At(10, 10))
	white := colorValue(img.At(150, 10))
	if black != 0 {
		t.Fatalf("expected dark quadrant to threshold to black, got %d", black)
	}
	if white != 0xffff {
		t.Fatalf("expected bright quadrant to threshold to white, got %d", white)
	}
}

func TestTransformGIF(t *testing.T) {
	p := newTestPipeline(t)
	req := mustParseImageRequest(t, "sample.png/full/80,/0/default.gif")

	var buf bytes.Buffer
	res, err := p.Transform(context.Background(), req, &buf)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if res.ContentType != "image/gif" {
		t.Fatalf("content-type = %q", res.ContentType)
	}

	img, err := gif.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decode gif: %v", err)
	}
	if got := img.Bounds().Dx(); got != 80 {
		t.Fatalf("decoded width = %d", got)
	}
}

func TestTransformJP2(t *testing.T) {
	if !vg.HasOperation("jp2ksave") {
		t.Skip("libvips JP2 encoder operation is unavailable")
	}
	p := newTestPipeline(t)
	req := mustParseImageRequest(t, "sample.png/full/80,/0/default.jp2")

	var buf bytes.Buffer
	res, err := p.Transform(context.Background(), req, &buf)
	if err != nil {
		if strings.Contains(err.Error(), "jp2ksave") {
			t.Skipf("libvips JP2 encoder failed in this environment: %v", err)
		}
		t.Fatalf("transform: %v", err)
	}
	if res.ContentType != "image/jp2" {
		t.Fatalf("content-type = %q", res.ContentType)
	}
	if buf.Len() == 0 {
		t.Fatal("empty jp2 output")
	}
	img, err := vg.NewImageFromBuffer(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("decode jp2 output: %v", err)
	}
	defer img.Close()
	if got := img.Width(); got != 80 {
		t.Fatalf("decoded width = %d", got)
	}
}

func TestTransformMaxOutputPixels(t *testing.T) {
	p := newTestPipelineWithLimits(t, Limits{MaxOutputPixels: 1000})
	req := mustParseImageRequest(t, "sample.png/full/max/0/default.png")

	var buf bytes.Buffer
	_, err := p.Transform(context.Background(), req, &buf)
	if err == nil || !strings.Contains(err.Error(), "max_output_pixels") {
		t.Fatalf("err = %v, want max_output_pixels failure", err)
	}
}

func TestNormalizeColorSpaceFallbackToSRGB(t *testing.T) {
	path := writeSampleImage(t)
	img, err := vg.NewImageFromFile(path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer img.Close()

	if err := img.Colourspace(vg.InterpretationLab, nil); err != nil {
		t.Fatalf("to lab: %v", err)
	}
	if got := img.Interpretation(); got != vg.InterpretationLab {
		t.Fatalf("interpretation before normalize = %v", got)
	}

	if err := normalizeColorSpace(img); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got := img.Interpretation(); got != vg.InterpretationSrgb {
		t.Fatalf("interpretation after normalize = %v", got)
	}
}

func TestNormalizeColorSpaceEmbeddedICC(t *testing.T) {
	path := writeProfiledSampleImage(t)
	img, err := vg.NewImageFromFile(path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer img.Close()

	if !img.HasICCProfile() {
		t.Fatal("expected embedded ICC profile in fixture")
	}
	if err := normalizeColorSpace(img); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got := img.Interpretation(); got != vg.InterpretationSrgb {
		t.Fatalf("interpretation after normalize = %v", got)
	}
}

func newTestPipeline(t *testing.T) *Pipeline {
	t.Helper()
	return newTestPipelineWithLimits(t, Limits{})
}

func newTestPipelineWithLimits(t *testing.T, limits Limits) *Pipeline {
	t.Helper()
	root := t.TempDir()
	writeSamplePNG(t, filepath.Join(root, "sample.png"))
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	return New(op, limits)
}

func writeSampleImage(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "sample.png")
	writeSamplePNG(t, path)
	return path
}

func writeSamplePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			switch {
			case x < 100 && y < 50:
				img.Set(x, y, color.RGBA{255, 0, 0, 255})
			case x >= 100 && y < 50:
				img.Set(x, y, color.RGBA{0, 255, 0, 255})
			case x < 100 && y >= 50:
				img.Set(x, y, color.RGBA{0, 0, 255, 255})
			default:
				img.Set(x, y, color.RGBA{255, 255, 0, 255})
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func writeProfiledSampleImage(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "sample.png")
	writeSamplePNG(t, sourcePath)

	profileBytes := loadNamedProfileForTest(t)
	profilePath := filepath.Join(root, "srgb.icc")
	if err := os.WriteFile(profilePath, profileBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	img, err := vg.NewImageFromFile(sourcePath, nil)
	if err != nil {
		t.Fatalf("load source: %v", err)
	}
	defer img.Close()

	profiledPath := filepath.Join(root, "profiled.png")
	if err := img.Pngsave(profiledPath, &vg.PngsaveOptions{Profile: profilePath, Keep: vg.KeepIcc}); err != nil {
		t.Fatalf("save profiled png: %v", err)
	}
	return profiledPath
}

func mustParseImageRequest(t *testing.T, path string) parse.Request {
	t.Helper()
	req, err := parse.Parse(path)
	if err != nil {
		t.Fatalf("parse %q: %v", path, err)
	}
	return req
}

func decodePNG(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return img
}

func colorValue(c color.Color) uint32 {
	r, _, _, _ := c.RGBA()
	return r
}

func loadNamedProfileForTest(t *testing.T) []byte {
	t.Helper()
	for _, name := range []string{"srgb", "sRGB"} {
		b, err := vg.ProfileLoad(name)
		if err == nil && len(b) > 0 {
			return b
		}
	}
	t.Skip("libvips named sRGB ICC profile not available in this environment")
	return nil
}
