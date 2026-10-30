package pipeline

import (
	"encoding/binary"
	"os"
	"regexp"

	gv "github.com/davidbyttow/govips/v2/vips"
	tvips "github.com/libops/triplet/internal/vips"
)

// Orientation returns the source's orientation (1–8), or 1, read like
// Cantaloupe does: the TIFF/Exif Orientation tag, else XMP tiff:Orientation.
// libvips sets the "orientation" field for TIFF; for JPEG, PNG and WebP it
// needs libexif, which Triplet's build leaves out, so the Exif block is read
// here. path is the source file, consulted only for a TIFF with an XMP
// orientation, since libvips reports a missing TIFF tag as 1.
func Orientation(img *gv.ImageRef, path string) int {
	xmp := func() int {
		if v, at := xmpOrientation(img.GetBlob("xmp-data")); at >= 0 {
			return v
		}
		return 1
	}
	if img.Format() == gv.ImageTypeTIFF {
		o := img.Orientation()
		if o == 1 {
			if v := xmp(); v != 1 && !tiffHasOrientationTag(path) {
				return v
			}
		}
		if o >= 1 && o <= 8 {
			return o
		}
		return 1
	}
	if o := img.Orientation(); o >= 1 && o <= 8 {
		return o
	}
	exif := img.GetBlob("exif-data")
	if e, ok := exifOrientation(exif); ok {
		if o := e.get(exif); o >= 1 && o <= 8 {
			return o
		}
		return 1
	}
	return xmp()
}

// DisplaySize returns the size of img once its orientation is applied.
func DisplaySize(img *gv.ImageRef, path string) (int, int) {
	return displaySize(img.Width(), img.Height(), Orientation(img, path))
}

func displaySize(w, h, orientation int) (int, int) {
	if orientation >= 5 {
		return h, w
	}
	return w, h
}

// orientationEntry locates the Orientation value in an Exif block.
type orientationEntry struct {
	order binary.ByteOrder
	at    int
	// LONG or SLONG instead of SHORT: wrong, but found in the wild (Cantaloupe #548).
	long bool
}

func (e orientationEntry) get(b []byte) int {
	if e.long {
		return int(int32(e.order.Uint32(b[e.at:])))
	}
	return int(int16(e.order.Uint16(b[e.at:])))
}

func (e orientationEntry) set(b []byte, v int) {
	if e.long {
		e.order.PutUint32(b[e.at:], uint32(v))
	} else {
		e.order.PutUint16(b[e.at:], uint16(v))
	}
}

// exifOrientation finds the Orientation entry (tag 0x0112, one SHORT, SSHORT,
// LONG or SLONG) in IFD0 of an Exif block, with or without the "Exif\0\0"
// prefix.
func exifOrientation(exif []byte) (orientationEntry, bool) {
	base := 0
	if len(exif) >= 6 && string(exif[:6]) == "Exif\x00\x00" {
		base = 6
	}
	t := exif[base:]
	if len(t) < 8 {
		return orientationEntry{}, false
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return orientationEntry{}, false
	}
	ifd := int(order.Uint32(t[4:8]))
	if ifd < 8 || ifd > len(t)-2 {
		return orientationEntry{}, false
	}
	n := int(order.Uint16(t[ifd:]))
	for i := 0; i < n && ifd+14+12*i <= len(t); i++ {
		e := ifd + 2 + 12*i
		if order.Uint16(t[e:]) != 0x0112 || order.Uint32(t[e+4:]) != 1 {
			continue
		}
		switch order.Uint16(t[e+2:]) {
		case 3, 8: // SHORT, SSHORT
			return orientationEntry{order: order, at: base + e + 8}, true
		case 4, 9: // LONG, SLONG
			return orientationEntry{order: order, at: base + e + 8, long: true}, true
		}
	}
	return orientationEntry{}, false
}

// xmpTIFFNamespace finds the prefixes bound to the XMP TIFF namespace,
// usually but not necessarily "tiff".
var xmpTIFFNamespace = regexp.MustCompile(`xmlns:([A-Za-z_][\w.-]*)\s*=\s*["']http://ns\.adobe\.com/tiff/1\.0/["']`)

// xmpOrientation returns the TIFF-namespace Orientation value in an XMP
// packet, as an attribute or an element, and the offset of its digit, or -1.
func xmpOrientation(xmp []byte) (int, int) {
	for _, ns := range xmpTIFFNamespace.FindAllSubmatch(xmp, 8) {
		re := regexp.MustCompile(`[\s<]` + regexp.QuoteMeta(string(ns[1])) + `:Orientation\s*(?:=\s*["']|>)\s*([1-8])\s*["'<]`)
		if m := re.FindSubmatchIndex(xmp); m != nil {
			return int(xmp[m[2]] - '0'), m[2]
		}
	}
	return 0, -1
}

// tiffHasOrientationTag reports whether IFD 0 of a classic or BigTIFF file has
// an Orientation tag. Unreadable files count as having one, so libvips' value
// stands.
func tiffHasOrientationTag(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	var head [16]byte
	if _, err := f.ReadAt(head[:], 0); err != nil {
		return true
	}
	var order binary.ByteOrder
	switch string(head[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return true
	}
	countSize, entrySize := int64(2), int64(12)
	var ifd int64
	switch order.Uint16(head[2:4]) {
	case 42:
		ifd = int64(order.Uint32(head[4:8]))
	case 43:
		countSize, entrySize = 8, 20
		ifd = int64(order.Uint64(head[8:16]))
	default:
		return true
	}
	var c [8]byte
	if ifd < 8 || ifd > 1<<40 {
		return true
	}
	if _, err := f.ReadAt(c[:countSize], ifd); err != nil {
		return true
	}
	var n int64
	if countSize == 2 {
		n = int64(order.Uint16(c[:2]))
	} else {
		n = int64(order.Uint64(c[:8]))
	}
	if n > 4096 {
		return true
	}
	entries := make([]byte, n*entrySize)
	if _, err := f.ReadAt(entries, ifd+countSize); err != nil {
		return true
	}
	for i := int64(0); i < n; i++ {
		if order.Uint16(entries[i*entrySize:]) == 0x0112 {
			return true
		}
	}
	return false
}

// storedRegion maps a region of the displayed image (orientation applied) to
// the stored pixels of a w×h source.
func storedRegion(orientation, w, h, left, top, rw, rh int) (int, int, int, int) {
	switch orientation {
	case 2:
		return w - left - rw, top, rw, rh
	case 3:
		return w - left - rw, h - top - rh, rw, rh
	case 4:
		return left, h - top - rh, rw, rh
	case 5:
		return top, left, rh, rw
	case 6:
		return top, h - left - rw, rh, rw
	case 7:
		return w - top - rh, h - left - rw, rh, rw
	case 8:
		return w - top - rh, left, rh, rw
	default:
		return left, top, rw, rh
	}
}

// applyOrientation turns img upright with the steps of libvips' autorot and
// sets its orientation to 1 everywhere it is stored, so viewers don't turn it
// a second time.
func applyOrientation(img *gv.ImageRef, orientation int) error {
	angle, flip := gv.Angle0, false
	switch orientation {
	case 2:
		flip = true
	case 3:
		angle = gv.Angle180
	case 4:
		angle, flip = gv.Angle180, true
	case 5:
		angle, flip = gv.Angle90, true
	case 6:
		angle = gv.Angle90
	case 7:
		angle, flip = gv.Angle270, true
	case 8:
		angle = gv.Angle270
	}
	if angle != gv.Angle0 {
		if err := img.Rotate(angle); err != nil {
			return tvips.Wrap("govips rotate", err)
		}
	}
	if flip {
		if err := img.Flip(gv.DirectionHorizontal); err != nil {
			return tvips.Wrap("govips flip", err)
		}
	}
	if err := img.RemoveOrientation(); err != nil {
		return tvips.Wrap("govips remove orientation", err)
	}
	// Without libexif, libvips saves the Exif and XMP blocks as loaded.
	exif := img.GetBlob("exif-data")
	if e, ok := exifOrientation(exif); ok {
		upright := append([]byte(nil), exif...)
		e.set(upright, 1)
		setBlob(img, "exif-data", upright)
	}
	xmp := img.GetBlob("xmp-data")
	if _, at := xmpOrientation(xmp); at >= 0 {
		upright := append([]byte(nil), xmp...)
		upright[at] = '1'
		setBlob(img, "xmp-data", upright)
	}
	return nil
}
