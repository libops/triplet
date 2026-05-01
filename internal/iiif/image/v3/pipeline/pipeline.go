// Package pipeline executes a parsed IIIF Image API request against a source
// image, writing the encoded derivative to an io.Writer.
//
// The pipeline applies the IIIF transform sequence in spec order:
// region → size → rotation → quality → format. libvips provides the
// underlying decode, transform, and encode operations.
package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	gv "github.com/davidbyttow/govips/v2/vips"
	"github.com/libops/triplet/internal/iiif/image/v3/parse"
	"github.com/libops/triplet/internal/redact"
	"github.com/libops/triplet/internal/storage"
	tvips "github.com/libops/triplet/internal/vips"
)

// ErrBadRequest marks syntactically valid IIIF requests that cannot be
// fulfilled because their resolved parameters violate the Image API rules.
var ErrBadRequest = errors.New("pipeline: bad request")

// ErrUnsupportedSource marks source bytes that libvips cannot decode as one of
// Triplet's supported input image formats.
var ErrUnsupportedSource = errors.New("pipeline: unsupported source image")

// Limits caps resource use per request.
type Limits struct {
	// MaxOutputPixels rejects requests whose computed output exceeds this
	// area. 0 disables the check.
	MaxOutputPixels int64
	// MaxSourcePixels rejects sources whose decoded dimensions exceed this
	// area. 0 disables the decoded source dimension limit.
	MaxSourcePixels int64
	// MaxSourceBytes rejects or stops spooling sources larger than this many
	// encoded bytes when a file path is not already available. 0 disables the
	// encoded source limit.
	MaxSourceBytes int64
	// MaxDerivativeBytes rejects encoded derivatives larger than this many
	// bytes after libvips export. 0 disables the encoded derivative limit.
	MaxDerivativeBytes int64
}

// Pipeline transforms a source image per a parsed [parse.Request].
type Pipeline struct {
	src     storage.Opener
	limits  Limits
	options Options
}

// Options controls optional performance/correctness tradeoffs.
type Options struct {
	// ColorManagement is "preserve", "normalize", or "none". Empty means "preserve".
	ColorManagement string
	// LoadAccess is "auto", "sequential", or "random". Empty means "auto".
	LoadAccess string
}

// New constructs a pipeline backed by src.
func New(src storage.Opener, limits Limits, opts ...Options) *Pipeline {
	options := Options{
		ColorManagement: "preserve",
		LoadAccess:      "auto",
	}
	if len(opts) > 0 {
		options = opts[0]
		if options.ColorManagement == "" {
			options.ColorManagement = "preserve"
		}
		if options.LoadAccess == "" {
			options.LoadAccess = "auto"
		}
	}
	return &Pipeline{src: src, limits: limits, options: options}
}

// Result describes a successfully encoded derivative.
type Result struct {
	ContentType string
	Width       int
	Height      int
}

// Transform decodes the source identified by req.Identifier, applies the IIIF
// region/size/rotation/quality/format transforms, encodes the result, and
// writes it to w.
//
// When w is an *os.File, Transform asks libvips to encode directly to that file
// so large derivatives do not need a matching Go byte slice. Other writer types
// use govips' buffer exporters as a compatibility fallback.
func (p *Pipeline) Transform(ctx context.Context, req parse.Request, w io.Writer) (Result, error) {
	if req.Kind != parse.KindImage {
		return Result{}, fmt.Errorf("pipeline: expected image request, got kind %d", req.Kind)
	}

	source, err := p.openSource(ctx, req.Identifier)
	if err != nil {
		return Result{}, err
	}
	defer source.Close()

	params := gv.NewImportParams()
	params.Access.Set(p.loadAccess(req))
	img, err := gv.LoadImageFromFileDirect(source.Path, params)
	if err != nil {
		return Result{}, WrapSourceLoadError("govips load", err)
	}
	defer func() {
		if img != nil {
			img.Close()
		}
	}()

	dims := dimensions{width: img.Width(), height: img.Height(), contentType: source.Meta.ContentType}
	if err := CheckSourcePixels(dims.width, dims.height, p.limits.MaxSourcePixels); err != nil {
		return Result{}, err
	}
	jp2Pages := jp2PageCount(req.Identifier, dims.contentType, img)
	left, top, regionW, regionH, err := resolveRegion(req.Region, dims.width, dims.height)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrBadRequest, err)
	}

	outW, outH, err := resolveSize(req.Size, regionW, regionH)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrBadRequest, err)
	}
	if p.limits.MaxOutputPixels > 0 && int64(outW)*int64(outH) > p.limits.MaxOutputPixels {
		return Result{}, fmt.Errorf("%w: output %dx%d exceeds max_output_pixels %d", ErrBadRequest, outW, outH, p.limits.MaxOutputPixels)
	}

	if page := chooseJP2Page(jp2Pages, regionW, regionH, outW, outH); page > 0 {
		img.Close()
		params := gv.NewImportParams()
		params.Access.Set(p.loadAccess(req))
		params.Page.Set(page)
		img, err = gv.LoadImageFromFileDirect(source.Path, params)
		if err != nil {
			return Result{}, WrapSourceLoadError("govips jp2kload page", err)
		}
		left, top, regionW, regionH = scaleRegionToLoadedPage(left, top, regionW, regionH, dims.width, dims.height, img.Width(), img.Height())
	}

	if err := applyColorManagement(img, p.options.ColorManagement); err != nil {
		return Result{}, err
	}

	if left != 0 || top != 0 || regionW != img.Width() || regionH != img.Height() {
		if err := img.ExtractArea(left, top, regionW, regionH); err != nil {
			return Result{}, tvips.Wrap("govips extract_area", err)
		}
	}
	if outW != regionW || outH != regionH {
		scaleX := float64(outW) / float64(regionW)
		scaleY := float64(outH) / float64(regionH)
		if err := img.ResizeWithVScale(scaleX, scaleY, gv.KernelLanczos3); err != nil {
			return Result{}, tvips.Wrap("govips resize", err)
		}
	}

	if req.Rotation.Mirror {
		if err := img.Flip(gv.DirectionHorizontal); err != nil {
			return Result{}, tvips.Wrap("govips flip", err)
		}
	}
	if req.Rotation.Degrees != 0 {
		if err := rotate(img, req.Rotation.Degrees); err != nil {
			return Result{}, err
		}
	}

	if replacement, err := applyQuality(img, req.Quality); err != nil {
		return Result{}, err
	} else if replacement != nil {
		img.Close()
		img = replacement
	}

	contentType, err := p.encode(img, req.Format, w)
	if err != nil {
		return Result{}, err
	}

	return Result{
		ContentType: contentType,
		Width:       img.Width(),
		Height:      img.Height(),
	}, nil
}

type dimensions struct {
	width, height int
	contentType   string
}

type sourceFile struct {
	Path    string
	Meta    storage.Meta
	rc      io.Closer
	tmpPath string
}

func (s *sourceFile) Close() error {
	var err error
	if s.rc != nil {
		err = s.rc.Close()
	}
	if s.tmpPath != "" {
		if remErr := os.Remove(s.tmpPath); err == nil {
			err = remErr
		}
	}
	return err
}

type namedFile interface {
	Name() string
}

func (p *Pipeline) openSource(ctx context.Context, identifier string) (*sourceFile, error) {
	rc, meta, err := p.src.Open(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if p.limits.MaxSourceBytes > 0 && meta.Size > p.limits.MaxSourceBytes {
		_ = rc.Close()
		return nil, fmt.Errorf("source %q exceeds max_source_bytes %d", redact.Identifier(identifier), p.limits.MaxSourceBytes)
	}
	if f, ok := rc.(namedFile); ok && f.Name() != "" {
		return &sourceFile{Path: f.Name(), Meta: meta, rc: rc}, nil
	}

	tmp, err := os.CreateTemp("", "triplet-source-*")
	if err != nil {
		_ = rc.Close()
		return nil, tvips.Wrap("source temp file", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	var reader io.Reader = rc
	if p.limits.MaxSourceBytes > 0 {
		reader = io.LimitReader(rc, p.limits.MaxSourceBytes+1)
	}
	n, err := io.Copy(tmp, reader)
	if err != nil {
		_ = rc.Close()
		cleanup()
		return nil, tvips.Wrap("spool source", err)
	}
	if p.limits.MaxSourceBytes > 0 && n > p.limits.MaxSourceBytes {
		_ = rc.Close()
		cleanup()
		return nil, fmt.Errorf("source %q exceeds max_source_bytes %d", redact.Identifier(identifier), p.limits.MaxSourceBytes)
	}
	if err := tmp.Close(); err != nil {
		_ = rc.Close()
		_ = os.Remove(tmpPath)
		return nil, tvips.Wrap("close source temp file", err)
	}
	_ = rc.Close()
	if meta.Size == 0 {
		meta.Size = n
	}
	return &sourceFile{Path: tmpPath, Meta: meta, tmpPath: tmpPath}, nil
}

// WrapSourceLoadError preserves libvips load details while marking errors that
// mean the source is not a decodable supported image.
func WrapSourceLoadError(op string, err error) error {
	if err == nil {
		return nil
	}
	if isUnsupportedSourceLoad(err) {
		return fmt.Errorf("%w: vips %s: %w", ErrUnsupportedSource, op, err)
	}
	return tvips.Wrap(op, err)
}

func isUnsupportedSourceLoad(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "is not a known file format") ||
		strings.Contains(msg, "no known loader") ||
		strings.Contains(msg, "operation class") && strings.Contains(msg, "is blocked")
}

func applyColorManagement(img *gv.ImageRef, mode string) error {
	switch mode {
	case "", "preserve", "none":
		return nil
	case "normalize":
		return normalizeColorSpace(img)
	default:
		return fmt.Errorf("color_management %q not supported", mode)
	}
}

func normalizeColorSpace(img *gv.ImageRef) error {
	if err := img.OptimizeICCProfile(); err != nil {
		return tvips.Wrap("govips optimize_icc_profile", err)
	}
	switch img.ColorSpace() {
	case gv.InterpretationSRGB, gv.InterpretationBW:
		return nil
	default:
		if !img.IsColorSpaceSupported() {
			return nil
		}
		if err := img.ToColorSpace(gv.InterpretationSRGB); err != nil {
			return tvips.Wrap("govips colourspace srgb", err)
		}
		return nil
	}
}

func rotate(img *gv.ImageRef, degrees float64) error {
	switch degrees {
	case 90:
		return tvips.Wrap("govips rotate", img.Rotate(gv.Angle90))
	case 180:
		return tvips.Wrap("govips rotate", img.Rotate(gv.Angle180))
	case 270:
		return tvips.Wrap("govips rotate", img.Rotate(gv.Angle270))
	default:
		return tvips.Wrap("govips similarity rotate", img.Similarity(1, degrees, &gv.ColorRGBA{A: 255}, 0, 0, 0, 0))
	}
}

func applyQuality(img *gv.ImageRef, q parse.Quality) (*gv.ImageRef, error) {
	switch q {
	case parse.QualityDefault, parse.QualityColor, "":
		return nil, nil
	case parse.QualityGray:
		if err := img.ToColorSpace(gv.InterpretationBW); err != nil {
			return nil, tvips.Wrap("govips colourspace gray", err)
		}
		return nil, nil
	case parse.QualityBitonal:
		return nil, applyBitonal(img)
	default:
		return nil, fmt.Errorf("quality %q not supported", q)
	}
}

func applyBitonal(img *gv.ImageRef) error {
	if err := img.ToColorSpace(gv.InterpretationBW); err != nil {
		return tvips.Wrap("govips colourspace gray", err)
	}
	if img.BandFormat() != gv.BandFormatUchar {
		if err := img.Cast(gv.BandFormatUchar); err != nil {
			return tvips.Wrap("govips bitonal cast", err)
		}
	}
	lut, err := loadBitonalLUT()
	if err != nil {
		return err
	}
	defer lut.Close()
	if err := img.Maplut(lut); err != nil {
		return tvips.Wrap("govips bitonal maplut", err)
	}
	return nil
}

var (
	bitonalLUTOnce sync.Once
	bitonalLUTPNG  []byte
	bitonalLUTErr  error
)

func loadBitonalLUT() (*gv.ImageRef, error) {
	bitonalLUTOnce.Do(func() {
		lut := image.NewGray(image.Rect(0, 0, 256, 1))
		for x := 0; x < 256; x++ {
			y := uint8(0)
			if x >= 128 {
				y = 255
			}
			lut.SetGray(x, 0, color.Gray{Y: y})
		}
		var out bytes.Buffer
		if err := png.Encode(&out, lut); err != nil {
			bitonalLUTErr = tvips.Wrap("encode bitonal lut", err)
			return
		}
		bitonalLUTPNG = out.Bytes()
	})
	if bitonalLUTErr != nil {
		return nil, bitonalLUTErr
	}
	lut, err := gv.LoadImageFromBuffer(bitonalLUTPNG, nil)
	if err != nil {
		return nil, tvips.Wrap("govips load bitonal lut", err)
	}
	return lut, nil
}

func (p *Pipeline) encode(img *gv.ImageRef, format parse.Format, w io.Writer) (string, error) {
	if f, ok := w.(*os.File); ok {
		contentType, n, err := encodeToFile(img, format, p.options.ColorManagement, f.Name())
		if err != nil {
			return "", err
		}
		if p.limits.MaxDerivativeBytes > 0 && n > p.limits.MaxDerivativeBytes {
			return "", fmt.Errorf("%w: derivative %d bytes exceeds max_derivative_bytes %d", ErrBadRequest, n, p.limits.MaxDerivativeBytes)
		}
		return contentType, nil
	}
	contentType, out, err := encodeToBuffer(img, format, p.options.ColorManagement)
	if err != nil {
		return "", err
	}
	if p.limits.MaxDerivativeBytes > 0 && int64(len(out)) > p.limits.MaxDerivativeBytes {
		return "", fmt.Errorf("%w: derivative %d bytes exceeds max_derivative_bytes %d", ErrBadRequest, len(out), p.limits.MaxDerivativeBytes)
	}
	n, err := w.Write(out)
	if err != nil {
		return "", tvips.Wrap("write", err)
	}
	if n != len(out) {
		return "", io.ErrShortWrite
	}
	return contentType, nil
}

func encodeToBuffer(img *gv.ImageRef, format parse.Format, colorManagement string) (string, []byte, error) {
	stripMetadata := colorManagement != "preserve"
	switch format {
	case parse.FormatJPG:
		opts := gv.NewJpegExportParams()
		opts.Quality = 75
		opts.StripMetadata = stripMetadata
		opts.OptimizeCoding = true
		opts.SubsampleMode = gv.VipsForeignSubsampleOn
		out, _, err := img.ExportJpeg(opts)
		return "image/jpeg", out, tvips.Wrap("govips jpegsave", err)
	case parse.FormatPNG:
		opts := gv.NewPngExportParams()
		opts.Compression = 6
		opts.StripMetadata = stripMetadata
		out, _, err := img.ExportPng(opts)
		return "image/png", out, tvips.Wrap("govips pngsave", err)
	case parse.FormatGIF:
		opts := gv.NewGifExportParams()
		opts.StripMetadata = stripMetadata
		out, _, err := img.ExportGIF(opts)
		return "image/gif", out, tvips.Wrap("govips gifsave", err)
	case parse.FormatWEBP:
		opts := gv.NewWebpExportParams()
		opts.Quality = 85
		opts.StripMetadata = stripMetadata
		out, _, err := img.ExportWebp(opts)
		return "image/webp", out, tvips.Wrap("govips webpsave", err)
	case parse.FormatTIF:
		opts := gv.NewTiffExportParams()
		opts.Compression = gv.TiffCompressionDeflate
		opts.StripMetadata = stripMetadata
		out, _, err := img.ExportTiff(opts)
		return "image/tiff", out, tvips.Wrap("govips tiffsave", err)
	case parse.FormatJP2:
		out, _, err := img.ExportJp2k(gv.NewJp2kExportParams())
		return "image/jp2", out, tvips.Wrap("govips jp2ksave", err)
	case parse.FormatPDF:
		out, err := savePDF(img)
		return "application/pdf", out, err
	default:
		return "", nil, fmt.Errorf("format %q unknown", format)
	}
}

func resolveRegion(r parse.Region, srcW, srcH int) (left, top, w, h int, err error) {
	switch r.Kind {
	case parse.RegionFull:
		return 0, 0, srcW, srcH, nil
	case parse.RegionSquare:
		side := srcW
		if srcH < side {
			side = srcH
		}
		return (srcW - side) / 2, (srcH - side) / 2, side, side, nil
	case parse.RegionPixels:
		left, top = int(r.X), int(r.Y)
		w, h = int(r.W), int(r.H)
	case parse.RegionPercent:
		left = int(math.Round(r.X / 100 * float64(srcW)))
		top = int(math.Round(r.Y / 100 * float64(srcH)))
		w = int(math.Round(r.W / 100 * float64(srcW)))
		h = int(math.Round(r.H / 100 * float64(srcH)))
	default:
		return 0, 0, 0, 0, fmt.Errorf("region: unknown kind")
	}
	// Clip to source bounds per IIIF spec §4.1.
	if left >= srcW || top >= srcH {
		return 0, 0, 0, 0, errors.New("region: outside source")
	}
	if left+w > srcW {
		w = srcW - left
	}
	if top+h > srcH {
		h = srcH - top
	}
	if w <= 0 || h <= 0 {
		return 0, 0, 0, 0, errors.New("region: empty after clipping")
	}
	return
}

func resolveSize(s parse.Size, regionW, regionH int) (int, int, error) {
	switch s.Kind {
	case parse.SizeMax, parse.SizeMaxUp:
		return regionW, regionH, nil
	case parse.SizeWidth:
		w := s.W
		if !s.Upscale && w > regionW {
			return 0, 0, errors.New("size: requested width exceeds region (use ^ for upscale)")
		}
		h := int(math.Round(float64(w) * float64(regionH) / float64(regionW)))
		return w, max1(h), nil
	case parse.SizeHeight:
		h := s.H
		if !s.Upscale && h > regionH {
			return 0, 0, errors.New("size: requested height exceeds region (use ^ for upscale)")
		}
		w := int(math.Round(float64(h) * float64(regionW) / float64(regionH)))
		return max1(w), h, nil
	case parse.SizePercent:
		scale := s.Percent / 100
		if !s.Upscale && scale > 1 {
			return 0, 0, errors.New("size: requested percentage exceeds region (use ^ for upscale)")
		}
		w := int(math.Round(float64(regionW) * scale))
		h := int(math.Round(float64(regionH) * scale))
		return max1(w), max1(h), nil
	case parse.SizeWH:
		if !s.Upscale && (s.W > regionW || s.H > regionH) {
			return 0, 0, errors.New("size: requested dimensions exceed region (use ^ for upscale)")
		}
		return s.W, s.H, nil
	case parse.SizeBestFit:
		// Fit within s.W × s.H preserving aspect.
		ar := float64(regionW) / float64(regionH)
		w, h := s.W, int(math.Round(float64(s.W)/ar))
		if h > s.H {
			h = s.H
			w = int(math.Round(float64(s.H) * ar))
		}
		if !s.Upscale && (w > regionW || h > regionH) {
			return 0, 0, errors.New("size: requested best-fit dimensions exceed region (use ^ for upscale)")
		}
		return max1(w), max1(h), nil
	}
	return 0, 0, fmt.Errorf("size: unknown kind")
}

func max1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

// CheckSourcePixels rejects decoded source dimensions above maxPixels.
func CheckSourcePixels(width, height int, maxPixels int64) error {
	if maxPixels <= 0 {
		return nil
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("%w: source dimensions %dx%d are invalid", ErrBadRequest, width, height)
	}
	if int64(width) > maxPixels/int64(height) {
		return fmt.Errorf("%w: source %dx%d exceeds max_source_pixels %d", ErrBadRequest, width, height, maxPixels)
	}
	return nil
}

func (p *Pipeline) loadAccess(req parse.Request) int {
	switch p.options.LoadAccess {
	case "sequential":
		return gv.AccessSequential
	case "random":
		return gv.AccessRandom
	}
	if req.Region.Kind == parse.RegionPixels || req.Region.Kind == parse.RegionPercent {
		return gv.AccessRandom
	}
	if req.Rotation.Mirror || req.Rotation.Degrees != 0 {
		return gv.AccessRandom
	}
	return gv.AccessSequential
}

func jp2PageCount(identifier, contentType string, img *gv.ImageRef) int {
	if !isJP2Source(identifier, contentType) {
		return 1
	}
	if pages := img.GetInt("n-pages"); pages > 0 {
		return pages
	}
	if pages := img.Pages(); pages > 0 {
		return pages
	}
	return 1
}

func isJP2Source(identifier, contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "jp2") || strings.Contains(ct, "jpeg2000") || strings.Contains(ct, "jpeg-2000") {
		return true
	}
	path := identifier
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jp2", ".j2k", ".j2c", ".jpf", ".jpx", ".jpm", ".mj2":
		return true
	default:
		return false
	}
}

func chooseJP2Page(pages, regionW, regionH, outW, outH int) int {
	if pages <= 1 || regionW <= 0 || regionH <= 0 || outW <= 0 || outH <= 0 {
		return 0
	}
	shrink := math.Min(float64(regionW)/float64(outW), float64(regionH)/float64(outH))
	if shrink < 2 {
		return 0
	}
	page := 0
	for candidate := 1; candidate < pages; candidate++ {
		if math.Ldexp(1, candidate) > shrink {
			break
		}
		page = candidate
	}
	return page
}

func scaleRegionToLoadedPage(left, top, width, height, fullW, fullH, pageW, pageH int) (int, int, int, int) {
	if fullW <= 0 || fullH <= 0 || pageW <= 0 || pageH <= 0 {
		return left, top, width, height
	}
	scaleX := float64(pageW) / float64(fullW)
	scaleY := float64(pageH) / float64(fullH)

	pageLeft := clampInt(int(math.Floor(float64(left)*scaleX)), 0, pageW-1)
	pageTop := clampInt(int(math.Floor(float64(top)*scaleY)), 0, pageH-1)
	pageRight := clampInt(int(math.Ceil(float64(left+width)*scaleX)), pageLeft+1, pageW)
	pageBottom := clampInt(int(math.Ceil(float64(top+height)*scaleY)), pageTop+1, pageH)

	return pageLeft, pageTop, pageRight - pageLeft, pageBottom - pageTop
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func savePDF(img *gv.ImageRef) ([]byte, error) {
	opts := gv.NewJpegExportParams()
	opts.Quality = 75
	opts.StripMetadata = true
	opts.OptimizeCoding = true
	opts.SubsampleMode = gv.VipsForeignSubsampleOn
	jpegBytes, _, err := img.ExportJpeg(opts)
	if err != nil {
		return nil, tvips.Wrap("pdf govips jpegsave", err)
	}
	colorSpace := "DeviceRGB"
	if img.Bands() == 1 {
		colorSpace = "DeviceGray"
	}
	return singleImagePDF(img.Width(), img.Height(), colorSpace, jpegBytes), nil
}

func singleImagePDF(width, height int, colorSpace string, jpeg []byte) []byte {
	var b bytes.Buffer
	offsets := make([]int, 0, 5)
	write := func(format string, args ...any) {
		_, _ = fmt.Fprintf(&b, format, args...)
	}
	object := func(n int, body func()) {
		offsets = append(offsets, b.Len())
		write("%d 0 obj\n", n)
		body()
		write("\nendobj\n")
	}

	write("%%PDF-1.4\n")
	object(1, func() { write("<< /Type /Catalog /Pages 2 0 R >>") })
	object(2, func() { write("<< /Type /Pages /Kids [3 0 R] /Count 1 >>") })
	object(3, func() {
		write("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>", width, height)
	})
	object(4, func() {
		write("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /%s /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", width, height, colorSpace, len(jpeg))
		_, _ = b.Write(jpeg)
		write("\nendstream")
	})
	content := fmt.Sprintf("q\n%d 0 0 %d 0 0 cm\n/Im0 Do\nQ\n", width, height)
	object(5, func() {
		write("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)
	})

	xref := b.Len()
	write("xref\n0 %d\n", len(offsets)+1)
	write("0000000000 65535 f \n")
	for _, off := range offsets {
		write("%010d 00000 n \n", off)
	}
	write("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return b.Bytes()
}
