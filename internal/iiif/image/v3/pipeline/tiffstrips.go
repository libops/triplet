package pipeline

/*
#cgo pkg-config: vips
#include <vips/vips.h>
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	gv "github.com/davidbyttow/govips/v2/vips"
	"github.com/libops/triplet/internal/iiif/image/v3/parse"
)

// libvips decodes striped TIFFs from the top, so for crops Triplet hands it a
// smaller TIFF with only the strips the crop needs and the rest of IFD 0 intact.

// Defined here so the build doesn't need libtiff's tiff.h.
const (
	// TIFF 6.0 specification.
	tiffTagImageWidth          = 256
	tiffTagImageLength         = 257
	tiffTagCompression         = 259
	tiffTagStripOffsets        = 273
	tiffTagRowsPerStrip        = 278
	tiffTagStripByteCounts     = 279
	tiffTagPlanarConfiguration = 284
	tiffTagTileWidth           = 322
	tiffTagJPEGIFOffset        = 513
	tiffTagJPEGIFByteCount     = 514
	tiffTypeShort              = 3
	tiffTypeLong               = 4
	tiffCompressionOldJPEG     = 6

	// Adobe TIFF Technical Note 1 (SubIFDs).
	tiffTagSubIFDs = 330
	tiffTypeIFD    = 13

	// EXIF 2.3 IFD pointers.
	tiffTagExifIFD    = 34665
	tiffTagGPSIFD     = 34853
	tiffTagInteropIFD = 40965

	// Adobe Photoshop TIFF Technical Notes (layer data).
	tiffTagImageSourceData = 37724

	// BigTIFF specification.
	tiffTypeLong8 = 16
	tiffTypeIFD8  = 18

	// Triplet's own cap, not a spec limit.
	tiffMaxIFDEntries = 4096
)

// stripWindowInMemory reports whether a window is small enough to hold in
// memory; libvips spills decoded images to disk above the same threshold.
var stripWindowInMemory = func(size uint64) bool {
	return size <= uint64(C.vips_get_disc_threshold())
}

// Field types 1–12 from TIFF 6.0, 13 from Technical Note 1, 16–18 from BigTIFF.
var tiffTypeSizes = map[uint16]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 6: 1, 7: 1, 8: 2, 9: 4, 10: 8, 11: 4, 12: 8, 13: 4, 16: 8, 17: 8, 18: 8}

type tiffEntry struct {
	tag, typ uint16
	count    uint64
	field    []byte // the raw value-or-offset field
}

// stripLayoutForCrop returns the strip layout when req crops a striped TIFF.
func stripLayoutForCrop(source *sourceFile, req parse.Request) *stripLayout {
	if req.Region.Kind == parse.RegionFull || isJP2Source(req.Identifier, source.Meta.ContentType) {
		return nil
	}
	f, err := os.Open(source.Path)
	if err != nil {
		return nil
	}
	defer f.Close()
	return readStripLayout(f)
}

// loadStrips loads the strips covering rows [top, top+rows), or the whole file
// if that fails, and returns the image with top relative to it. Call release
// once the image is no longer used.
func loadStrips(path string, l *stripLayout, top, rows int, params *gv.ImportParams) (img *gv.ImageRef, imgTop int, release func(), err error) {
	if rows < l.height {
		if img, windowTop, release, err := loadStripWindow(path, l, top, rows, params); err == nil {
			return img, top - windowTop, release, nil
		}
	}
	img, err = gv.LoadImageFromFileDirect(path, params)
	return img, top, func() {}, err
}

func loadStripWindow(path string, l *stripLayout, top, rows int, params *gv.ImportParams) (*gv.ImageRef, int, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, nil, err
	}
	defer f.Close()
	w, err := l.window(f, top, rows)
	if err != nil {
		return nil, 0, nil, err
	}
	release := func() {}
	var img *gv.ImageRef
	if stripWindowInMemory(w.size) {
		buf, err := w.bytes(f)
		if err != nil {
			return nil, 0, nil, err
		}
		img, err = gv.LoadImageFromBuffer(buf, params)
		if err != nil {
			return nil, 0, nil, err
		}
	} else {
		tmp, err := os.CreateTemp("", "triplet-strips-*.tif")
		if err != nil {
			return nil, 0, nil, err
		}
		release = func() { _ = os.Remove(tmp.Name()) }
		err = w.writeTo(f, tmp)
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			img, err = gv.LoadImageFromFileDirect(tmp.Name(), params)
		}
		if err != nil {
			release()
			return nil, 0, nil, err
		}
	}
	if img.Width() != l.width || img.Height() < top-w.top+rows {
		img.Close()
		release()
		return nil, 0, nil, errors.New("tiff: strip window has unexpected size")
	}
	return img, w.top, release, nil
}

// stripLayout describes the first IFD of a striped TIFF.
type stripLayout struct {
	order         binary.ByteOrder
	bigTIFF       bool
	size          uint64
	entries       []tiffEntry
	width, height int
	rowsPerStrip  int
	offsets       []uint64
	counts        []uint64
}

// readStripLayout handles striped TIFF/BigTIFF in any compression but old-style JPEG.
// Others (tiled, plane-separate, one strip, bad strip table) get nil: a normal load.
func readStripLayout(f *os.File) *stripLayout {
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	var head [16]byte
	if _, err := f.ReadAt(head[:], 0); err != nil {
		return nil
	}
	l := &stripLayout{size: uint64(info.Size())}
	switch string(head[:2]) {
	case "II":
		l.order = binary.LittleEndian
	case "MM":
		l.order = binary.BigEndian
	default:
		return nil
	}
	var ifdOffset uint64
	switch l.order.Uint16(head[2:4]) {
	case 42:
		ifdOffset = uint64(l.order.Uint32(head[4:8]))
	case 43:
		if l.order.Uint16(head[4:6]) != 8 || l.order.Uint16(head[6:8]) != 0 {
			return nil
		}
		l.bigTIFF = true
		ifdOffset = l.order.Uint64(head[8:16])
	default:
		return nil
	}
	if err := l.readIFD(f, ifdOffset); err != nil {
		return nil
	}

	values := map[uint16][]uint64{}
	for _, e := range l.entries {
		switch e.tag {
		case tiffTagTileWidth:
			return nil
		case tiffTagImageWidth, tiffTagImageLength, tiffTagCompression, tiffTagRowsPerStrip,
			tiffTagPlanarConfiguration, tiffTagStripOffsets, tiffTagStripByteCounts:
			v, err := l.uints(f, e)
			if err != nil || len(v) == 0 {
				return nil
			}
			values[e.tag] = v
		}
	}
	if len(values[tiffTagImageWidth]) != 1 || len(values[tiffTagImageLength]) != 1 {
		return nil
	}
	if c := values[tiffTagCompression]; len(c) == 1 && c[0] == tiffCompressionOldJPEG {
		return nil
	}
	if pc := values[tiffTagPlanarConfiguration]; len(pc) == 1 && pc[0] != 1 {
		return nil
	}
	width, height := values[tiffTagImageWidth][0], values[tiffTagImageLength][0]
	if width == 0 || height == 0 || width > 1<<31 || height > 1<<31 {
		return nil
	}
	l.width, l.height, l.rowsPerStrip = int(width), int(height), int(height)
	if rps := values[tiffTagRowsPerStrip]; len(rps) == 1 && rps[0] > 0 && rps[0] < height {
		l.rowsPerStrip = int(rps[0])
	}
	strips := (l.height + l.rowsPerStrip - 1) / l.rowsPerStrip
	l.offsets, l.counts = values[tiffTagStripOffsets], values[tiffTagStripByteCounts]
	if strips < 2 || len(l.offsets) != strips || len(l.counts) != strips {
		return nil
	}
	for i, c := range l.counts {
		if c == 0 || l.offsets[i] > l.size || c > l.size-l.offsets[i] {
			return nil
		}
	}
	return l
}

func (l *stripLayout) fieldSize() int {
	if l.bigTIFF {
		return 8
	}
	return 4
}

func (l *stripLayout) readIFD(f *os.File, offset uint64) error {
	countSize, entrySize := 2, 12
	if l.bigTIFF {
		countSize, entrySize = 8, 20
	}
	countBuf := make([]byte, countSize)
	if _, err := f.ReadAt(countBuf, int64(offset)); err != nil {
		return err
	}
	var n uint64
	if l.bigTIFF {
		n = l.order.Uint64(countBuf)
	} else {
		n = uint64(l.order.Uint16(countBuf))
	}
	if n == 0 || n > tiffMaxIFDEntries {
		return errors.New("tiff: bad IFD entry count")
	}
	buf := make([]byte, int(n)*entrySize)
	if _, err := f.ReadAt(buf, int64(offset)+int64(countSize)); err != nil {
		return err
	}
	for i := 0; i < int(n); i++ {
		raw := buf[i*entrySize : (i+1)*entrySize]
		e := tiffEntry{tag: l.order.Uint16(raw[0:2]), typ: l.order.Uint16(raw[2:4])}
		if l.bigTIFF {
			e.count, e.field = l.order.Uint64(raw[4:12]), raw[12:20]
		} else {
			e.count, e.field = uint64(l.order.Uint32(raw[4:8])), raw[8:12]
		}
		if _, ok := tiffTypeSizes[e.typ]; !ok {
			return fmt.Errorf("tiff: tag %d has unknown type %d", e.tag, e.typ)
		}
		l.entries = append(l.entries, e)
	}
	return nil
}

// payload returns an entry's value bytes, reading them from the file when they
// do not fit in the entry itself.
func (l *stripLayout) payload(f *os.File, e tiffEntry) ([]byte, error) {
	typeSize := uint64(tiffTypeSizes[e.typ])
	if e.count > l.size/typeSize {
		return nil, fmt.Errorf("tiff: tag %d value larger than the file", e.tag)
	}
	size := e.count * typeSize
	if size <= uint64(l.fieldSize()) {
		return e.field[:size], nil
	}
	var offset uint64
	if l.bigTIFF {
		offset = l.order.Uint64(e.field)
	} else {
		offset = uint64(l.order.Uint32(e.field))
	}
	if offset > l.size || size > l.size-offset {
		return nil, fmt.Errorf("tiff: tag %d value outside the file", e.tag)
	}
	b := make([]byte, size)
	if _, err := f.ReadAt(b, int64(offset)); err != nil {
		return nil, err
	}
	return b, nil
}

func (l *stripLayout) uints(f *os.File, e tiffEntry) ([]uint64, error) {
	b, err := l.payload(f, e)
	if err != nil {
		return nil, err
	}
	out := make([]uint64, e.count)
	for i := range out {
		switch e.typ {
		case tiffTypeShort:
			out[i] = uint64(l.order.Uint16(b[i*2:]))
		case tiffTypeLong:
			out[i] = uint64(l.order.Uint32(b[i*4:]))
		case tiffTypeLong8:
			out[i] = l.order.Uint64(b[i*8:])
		default:
			return nil, fmt.Errorf("tiff: tag %d is not an integer", e.tag)
		}
	}
	return out, nil
}

// stripWindow is a TIFF made of head followed by the listed source strips.
type stripWindow struct {
	head            []byte
	offsets, counts []uint64
	top             int // source row of the window's first row
	size            uint64
}

func (w *stripWindow) bytes(src *os.File) ([]byte, error) {
	buf := make([]byte, w.size)
	at := uint64(copy(buf, w.head))
	for j, o := range w.offsets {
		if _, err := src.ReadAt(buf[at:at+w.counts[j]], int64(o)); err != nil {
			return nil, err
		}
		at += w.counts[j]
	}
	return buf, nil
}

func (w *stripWindow) writeTo(src *os.File, dst io.Writer) error {
	if _, err := dst.Write(w.head); err != nil {
		return err
	}
	for j, o := range w.offsets {
		if n, err := io.Copy(dst, io.NewSectionReader(src, int64(o), int64(w.counts[j]))); err != nil || uint64(n) != w.counts[j] {
			return fmt.Errorf("tiff: copy strip %d: %v", j, err)
		}
	}
	return nil
}

// window describes a TIFF holding the strips that cover rows [top, top+rows).
func (l *stripLayout) window(f *os.File, top, rows int) (*stripWindow, error) {
	first, last := top/l.rowsPerStrip, (top+rows-1)/l.rowsPerStrip
	windowTop := first * l.rowsPerStrip
	windowRows := min((last+1)*l.rowsPerStrip, l.height) - windowTop
	offsets, counts := l.offsets[first:last+1], l.counts[first:last+1]

	offsetType, offsetSize := uint16(tiffTypeLong), 4
	if l.bigTIFF {
		offsetType, offsetSize = tiffTypeLong8, 8
	}
	type outEntry struct {
		tiffEntry
		data []byte
	}
	var out []outEntry
	for _, e := range l.entries {
		switch {
		case e.typ == tiffTypeIFD || e.typ == tiffTypeIFD8,
			e.tag == tiffTagSubIFDs, e.tag == tiffTagExifIFD, e.tag == tiffTagGPSIFD,
			e.tag == tiffTagInteropIFD, e.tag == tiffTagImageSourceData,
			e.tag == tiffTagJPEGIFOffset, e.tag == tiffTagJPEGIFByteCount:
			continue
		case e.tag == tiffTagImageLength:
			e.typ, e.count = tiffTypeLong, 1
			data := make([]byte, 4)
			l.order.PutUint32(data, uint32(windowRows))
			out = append(out, outEntry{e, data})
		case e.tag == tiffTagStripOffsets, e.tag == tiffTagStripByteCounts:
			e.typ, e.count = offsetType, uint64(len(offsets))
			out = append(out, outEntry{e, make([]byte, len(offsets)*offsetSize)})
		default:
			data, err := l.payload(f, e)
			if err != nil {
				return nil, err
			}
			out = append(out, outEntry{e, data})
		}
	}

	headerSize, countSize, entrySize, nextSize := 8, 2, 12, 4
	if l.bigTIFF {
		headerSize, countSize, entrySize, nextSize = 16, 8, 20, 8
	}
	pos := headerSize + countSize + len(out)*entrySize + nextSize
	align := func() { pos += pos % 2 }
	valueAt := make([]int, len(out))
	for i, e := range out {
		if len(e.data) > l.fieldSize() {
			align()
			valueAt[i] = pos
			pos += len(e.data)
		}
	}
	align()
	stripsAt := pos
	size := uint64(stripsAt)
	for _, c := range counts {
		size += c
	}

	putOffset := func(b []byte, v uint64) {
		if l.bigTIFF {
			l.order.PutUint64(b, v)
		} else {
			l.order.PutUint32(b, uint32(v))
		}
	}
	at := uint64(stripsAt)
	for i, e := range out {
		switch e.tag {
		case tiffTagStripOffsets:
			for j := range offsets {
				putOffset(e.data[j*offsetSize:], at)
				at += counts[j]
			}
		case tiffTagStripByteCounts:
			for j, c := range counts {
				putOffset(e.data[j*offsetSize:], c)
			}
		}
		out[i] = e
	}

	buf := make([]byte, stripsAt)
	copy(buf[0:2], "MM")
	if l.order == binary.LittleEndian {
		copy(buf[0:2], "II")
	}
	if l.bigTIFF {
		l.order.PutUint16(buf[2:], 43)
		l.order.PutUint16(buf[4:], 8)
		l.order.PutUint64(buf[8:], uint64(headerSize))
		l.order.PutUint64(buf[headerSize:], uint64(len(out)))
	} else {
		l.order.PutUint16(buf[2:], 42)
		l.order.PutUint32(buf[4:], uint32(headerSize))
		l.order.PutUint16(buf[headerSize:], uint16(len(out)))
	}
	for i, e := range out {
		raw := buf[headerSize+countSize+i*entrySize:]
		l.order.PutUint16(raw[0:], e.tag)
		l.order.PutUint16(raw[2:], e.typ)
		field := raw[8:12]
		if l.bigTIFF {
			l.order.PutUint64(raw[4:], e.count)
			field = raw[12:20]
		} else {
			l.order.PutUint32(raw[4:], uint32(e.count))
		}
		if valueAt[i] > 0 {
			putOffset(field, uint64(valueAt[i]))
			copy(buf[valueAt[i]:], e.data)
		} else {
			copy(field, e.data)
		}
	}
	return &stripWindow{head: buf, offsets: offsets, counts: counts, top: windowTop, size: size}, nil
}
