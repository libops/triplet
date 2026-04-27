package pipeline

/*
#cgo pkg-config: vips
#include <stdlib.h>
#include <vips/vips.h>

static int triplet_jpegsave(void *image, const char *filename, int quality, int strip, int optimize, int subsample) {
	return vips_jpegsave((VipsImage *)image, filename,
		"Q", quality,
		"strip", strip,
		"optimize-coding", optimize,
		"subsample-mode", subsample,
		NULL);
}

static int triplet_pngsave(void *image, const char *filename, int compression, int strip) {
	return vips_pngsave((VipsImage *)image, filename,
		"compression", compression,
		"strip", strip,
		NULL);
}

static int triplet_gifsave(void *image, const char *filename, int strip) {
	return vips_gifsave((VipsImage *)image, filename,
		"strip", strip,
		NULL);
}

static int triplet_webpsave(void *image, const char *filename, int quality, int strip) {
	return vips_webpsave((VipsImage *)image, filename,
		"Q", quality,
		"strip", strip,
		NULL);
}

static int triplet_tiffsave(void *image, const char *filename, int compression, int strip) {
	return vips_tiffsave((VipsImage *)image, filename,
		"compression", compression,
		"strip", strip,
		NULL);
}

static int triplet_jp2ksave(void *image, const char *filename) {
	return vips_jp2ksave((VipsImage *)image, filename, NULL);
}
*/
import "C"

import (
	"fmt"
	"io"
	"os"
	"unsafe"

	gv "github.com/davidbyttow/govips/v2/vips"
	"github.com/libops/triplet/internal/iiif/image/v3/parse"
)

type imageRefHeader struct {
	buf   []byte
	image unsafe.Pointer
}

func encodeToFile(img *gv.ImageRef, format parse.Format, colorManagement, path string) (string, int64, error) {
	if path == "" {
		return "", 0, fmt.Errorf("file encoder requires a named output file")
	}
	contentType, err := saveImageFile(img, format, colorManagement, path)
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("stat encoded derivative: %w", err)
	}
	return contentType, info.Size(), nil
}

func saveImageFile(img *gv.ImageRef, format parse.Format, colorManagement, path string) (string, error) {
	strip := cBool(colorManagement != "preserve")
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	ptr := imagePtr(img)

	switch format {
	case parse.FormatJPG:
		if C.triplet_jpegsave(ptr, cpath, 75, strip, 1, C.int(gv.VipsForeignSubsampleOn)) != 0 {
			return "", vipsSaveError("govips jpegsave")
		}
		return "image/jpeg", nil
	case parse.FormatPNG:
		if C.triplet_pngsave(ptr, cpath, 6, strip) != 0 {
			return "", vipsSaveError("govips pngsave")
		}
		return "image/png", nil
	case parse.FormatGIF:
		if C.triplet_gifsave(ptr, cpath, strip) != 0 {
			return "", vipsSaveError("govips gifsave")
		}
		return "image/gif", nil
	case parse.FormatWEBP:
		if C.triplet_webpsave(ptr, cpath, 85, strip) != 0 {
			return "", vipsSaveError("govips webpsave")
		}
		return "image/webp", nil
	case parse.FormatTIF:
		if C.triplet_tiffsave(ptr, cpath, C.int(gv.TiffCompressionDeflate), strip) != 0 {
			return "", vipsSaveError("govips tiffsave")
		}
		return "image/tiff", nil
	case parse.FormatJP2:
		if C.triplet_jp2ksave(ptr, cpath) != 0 {
			return "", vipsSaveError("govips jp2ksave")
		}
		return "image/jp2", nil
	case parse.FormatPDF:
		if err := savePDFFile(img, path); err != nil {
			return "", err
		}
		return "application/pdf", nil
	default:
		return "", fmt.Errorf("format %q unknown", format)
	}
}

func savePDFFile(img *gv.ImageRef, path string) error {
	tmp, err := os.CreateTemp("", "triplet-pdf-jpeg-*")
	if err != nil {
		return fmt.Errorf("pdf jpeg temp file: %w", err)
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("pdf jpeg temp close: %w", err)
	}
	defer os.Remove(tmpName)

	cpath := C.CString(tmpName)
	ptr := imagePtr(img)
	if C.triplet_jpegsave(ptr, cpath, 75, 1, 1, C.int(gv.VipsForeignSubsampleOn)) != 0 {
		C.free(unsafe.Pointer(cpath))
		return vipsSaveError("pdf govips jpegsave")
	}
	C.free(unsafe.Pointer(cpath))

	jpeg, err := os.Open(tmpName)
	if err != nil {
		return fmt.Errorf("open pdf jpeg stream: %w", err)
	}
	defer jpeg.Close()
	stat, err := jpeg.Stat()
	if err != nil {
		return fmt.Errorf("stat pdf jpeg stream: %w", err)
	}

	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create pdf derivative: %w", err)
	}
	defer out.Close()

	colorSpace := "DeviceRGB"
	if img.Bands() == 1 {
		colorSpace = "DeviceGray"
	}
	return writeSingleImagePDF(out, img.Width(), img.Height(), colorSpace, stat.Size(), jpeg)
}

func writeSingleImagePDF(w io.Writer, width, height int, colorSpace string, jpegLen int64, jpeg io.Reader) error {
	cw := &countingWriter{w: w}
	offsets := make([]int64, 0, 5)
	write := func(format string, args ...any) error {
		_, err := fmt.Fprintf(cw, format, args...)
		return err
	}
	object := func(n int, body func() error) error {
		offsets = append(offsets, cw.n)
		if err := write("%d 0 obj\n", n); err != nil {
			return err
		}
		if err := body(); err != nil {
			return err
		}
		return write("\nendobj\n")
	}

	if err := write("%%PDF-1.4\n"); err != nil {
		return err
	}
	if err := object(1, func() error { return write("<< /Type /Catalog /Pages 2 0 R >>") }); err != nil {
		return err
	}
	if err := object(2, func() error { return write("<< /Type /Pages /Kids [3 0 R] /Count 1 >>") }); err != nil {
		return err
	}
	if err := object(3, func() error {
		return write("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>", width, height)
	}); err != nil {
		return err
	}
	if err := object(4, func() error {
		if err := write("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /%s /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", width, height, colorSpace, jpegLen); err != nil {
			return err
		}
		if _, err := io.Copy(cw, jpeg); err != nil {
			return err
		}
		return write("\nendstream")
	}); err != nil {
		return err
	}
	content := fmt.Sprintf("q\n%d 0 0 %d 0 0 cm\n/Im0 Do\nQ\n", width, height)
	if err := object(5, func() error {
		return write("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)
	}); err != nil {
		return err
	}

	xref := cw.n
	if err := write("xref\n0 %d\n", len(offsets)+1); err != nil {
		return err
	}
	if err := write("0000000000 65535 f \n"); err != nil {
		return err
	}
	for _, off := range offsets {
		if err := write("%010d 00000 n \n", off); err != nil {
			return err
		}
	}
	return write("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

func imagePtr(img *gv.ImageRef) unsafe.Pointer {
	return (*imageRefHeader)(unsafe.Pointer(img)).image
}

func cBool(v bool) C.int {
	if v {
		return 1
	}
	return 0
}

func vipsSaveError(op string) error {
	msg := C.GoString(C.vips_error_buffer())
	C.vips_error_clear()
	if msg == "" {
		return fmt.Errorf("%s failed", op)
	}
	return fmt.Errorf("%s: %s", op, msg)
}
