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
	"io"
	"math"

	vg "github.com/cshum/vipsgen/vips"
	"github.com/libops/triplet/internal/iiif/image/v3/parse"
	"github.com/libops/triplet/internal/storage"
	tvips "github.com/libops/triplet/internal/vips"
)

// Limits caps resource use per request.
type Limits struct {
	// MaxOutputPixels rejects requests whose computed output exceeds this
	// area. 0 disables the check.
	MaxOutputPixels int64
}

// Pipeline transforms a source image per a parsed [parse.Request].
type Pipeline struct {
	src    storage.Opener
	limits Limits
}

// New constructs a pipeline backed by src.
func New(src storage.Opener, limits Limits) *Pipeline {
	return &Pipeline{src: src, limits: limits}
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
// w is wrapped as a libvips streaming target — derivative bytes flow to w as
// libvips encodes them, without buffering the full output in memory.
func (p *Pipeline) Transform(ctx context.Context, req parse.Request, w io.Writer) (Result, error) {
	if req.Kind != parse.KindImage {
		return Result{}, fmt.Errorf("pipeline: expected image request, got kind %d", req.Kind)
	}

	rsc, _, err := p.src.Open(ctx, req.Identifier)
	if err != nil {
		return Result{}, err
	}
	defer rsc.Close()

	source := vg.NewSource(rsc)
	defer source.Close()

	img, err := vg.NewImageFromSource(source, nil)
	if err != nil {
		return Result{}, tvips.Wrap("load", err)
	}
	defer img.Close()
	if err := normalizeColorSpace(img); err != nil {
		return Result{}, err
	}

	srcW, srcH := img.Width(), img.Height()

	left, top, regionW, regionH, err := resolveRegion(req.Region, srcW, srcH)
	if err != nil {
		return Result{}, err
	}

	outW, outH, err := resolveSize(req.Size, regionW, regionH)
	if err != nil {
		return Result{}, err
	}
	if p.limits.MaxOutputPixels > 0 && int64(outW)*int64(outH) > p.limits.MaxOutputPixels {
		return Result{}, fmt.Errorf("output %dx%d exceeds max_output_pixels %d", outW, outH, p.limits.MaxOutputPixels)
	}

	if !(left == 0 && top == 0 && regionW == srcW && regionH == srcH) {
		if err := img.ExtractArea(left, top, regionW, regionH); err != nil {
			return Result{}, tvips.Wrap("extract_area", err)
		}
	}

	if outW != regionW || outH != regionH {
		scaleX := float64(outW) / float64(regionW)
		scaleY := float64(outH) / float64(regionH)
		if err := img.Resize(scaleX, &vg.ResizeOptions{Vscale: scaleY}); err != nil {
			return Result{}, tvips.Wrap("resize", err)
		}
	}

	if req.Rotation.Mirror {
		if err := img.Flip(vg.DirectionHorizontal); err != nil {
			return Result{}, tvips.Wrap("flip", err)
		}
	}
	if req.Rotation.Degrees != 0 {
		if err := img.Rotate(req.Rotation.Degrees, nil); err != nil {
			return Result{}, tvips.Wrap("rotate", err)
		}
	}

	if err := applyQuality(img, req.Quality); err != nil {
		return Result{}, err
	}

	target := vg.NewTarget(tvips.NopWriteCloser(w))
	defer target.Close()

	contentType, err := encode(img, target, w, req.Format)
	if err != nil {
		return Result{}, err
	}

	return Result{
		ContentType: contentType,
		Width:       img.Width(),
		Height:      img.Height(),
	}, nil
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
			w = regionW
		}
		h := int(math.Round(float64(w) * float64(regionH) / float64(regionW)))
		return w, max1(h), nil
	case parse.SizeHeight:
		h := s.H
		if !s.Upscale && h > regionH {
			h = regionH
		}
		w := int(math.Round(float64(h) * float64(regionW) / float64(regionH)))
		return max1(w), h, nil
	case parse.SizePercent:
		scale := s.Percent / 100
		if !s.Upscale && scale > 1 {
			scale = 1
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
		if !s.Upscale {
			if w > regionW || h > regionH {
				w, h = regionW, regionH
			}
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

func applyQuality(img *vg.Image, q parse.Quality) error {
	switch q {
	case parse.QualityDefault, parse.QualityColor, "":
		return nil
	case parse.QualityGray:
		if err := img.Colourspace(vg.InterpretationBW, nil); err != nil {
			return tvips.Wrap("colourspace gray", err)
		}
		return nil
	case parse.QualityBitonal:
		if err := img.Colourspace(vg.InterpretationBW, nil); err != nil {
			return tvips.Wrap("colourspace gray", err)
		}
		// Threshold after grayscale conversion so bitonal is not just "gray".
		// libvips relational ops yield a mask-like image; cast to uchar so
		// downstream encoders reliably write 0/255 sample values.
		if err := img.RelationalConst(vg.OperationRelationalMoreeq, []float64{128}); err != nil {
			return tvips.Wrap("relational bitonal", err)
		}
		if err := img.Cast(vg.BandFormatUchar, nil); err != nil {
			return tvips.Wrap("cast bitonal", err)
		}
		return nil
	}
	return fmt.Errorf("quality %q not supported", q)
}

func normalizeColorSpace(img *vg.Image) error {
	if img.HasICCProfile() {
		opts := vg.DefaultIccImportOptions()
		opts.Embedded = true
		if err := img.IccImport(opts); err != nil {
			return tvips.Wrap("icc_import", err)
		}
	}

	switch img.Interpretation() {
	case vg.InterpretationSrgb, vg.InterpretationBW:
		return nil
	default:
		if !img.IsColorSpaceSupported() {
			return nil
		}
		if err := img.Colourspace(vg.InterpretationSrgb, nil); err != nil {
			return tvips.Wrap("colourspace srgb", err)
		}
		return nil
	}
}

func encode(img *vg.Image, target *vg.Target, w io.Writer, format parse.Format) (string, error) {
	switch format {
	case parse.FormatJPG:
		if err := img.JpegsaveTarget(target, &vg.JpegsaveTargetOptions{Q: 85, OptimizeCoding: true, Keep: vg.KeepIcc}); err != nil {
			return "", tvips.Wrap("jpegsave", err)
		}
		return "image/jpeg", nil
	case parse.FormatPNG:
		if err := img.PngsaveTarget(target, &vg.PngsaveTargetOptions{Compression: 6, Keep: vg.KeepIcc}); err != nil {
			return "", tvips.Wrap("pngsave", err)
		}
		return "image/png", nil
	case parse.FormatGIF:
		if err := img.GifsaveTarget(target, &vg.GifsaveTargetOptions{Keep: vg.KeepIcc}); err != nil {
			return "", tvips.Wrap("gifsave", err)
		}
		return "image/gif", nil
	case parse.FormatWEBP:
		if err := img.WebpsaveTarget(target, &vg.WebpsaveTargetOptions{Q: 85, Keep: vg.KeepIcc}); err != nil {
			return "", tvips.Wrap("webpsave", err)
		}
		return "image/webp", nil
	case parse.FormatTIF:
		if err := img.TiffsaveTarget(target, &vg.TiffsaveTargetOptions{Compression: vg.TiffCompressionDeflate, Keep: vg.KeepIcc}); err != nil {
			return "", tvips.Wrap("tiffsave", err)
		}
		return "image/tiff", nil
	case parse.FormatJP2:
		opts := vg.DefaultJp2ksaveTargetOptions()
		opts.Keep = vg.KeepIcc
		if err := img.Jp2ksaveTarget(target, opts); err != nil {
			return "", tvips.Wrap("jp2ksave", err)
		}
		return "image/jp2", nil
	case parse.FormatPDF:
		if err := savePDF(img, w); err != nil {
			return "", err
		}
		return "application/pdf", nil
	}
	return "", fmt.Errorf("format %q unknown", format)
}

func savePDF(img *vg.Image, w io.Writer) error {
	var jpeg bytes.Buffer
	jpegTarget := vg.NewTarget(tvips.NopWriteCloser(&jpeg))
	if err := img.JpegsaveTarget(jpegTarget, &vg.JpegsaveTargetOptions{Q: 85, OptimizeCoding: true, Keep: vg.KeepNone}); err != nil {
		jpegTarget.Close()
		return tvips.Wrap("pdf jpegsave", err)
	}
	jpegTarget.Close()

	colorSpace := "DeviceRGB"
	if img.Bands() == 1 {
		colorSpace = "DeviceGray"
	}
	if _, err := w.Write(singleImagePDF(img.Width(), img.Height(), colorSpace, jpeg.Bytes())); err != nil {
		return tvips.Wrap("pdf write", err)
	}
	return nil
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
