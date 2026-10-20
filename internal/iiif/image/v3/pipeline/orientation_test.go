package pipeline

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	gv "github.com/davidbyttow/govips/v2/vips"
	"github.com/libops/triplet/internal/storage"
)

// blockColors is a 3×2 grid of distinct colours: no flip or rotation maps it
// onto itself, so each orientation gives a different stored image.
var blockColors = [2][3]color.RGBA{
	{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}},
	{{255, 255, 0, 255}, {0, 255, 255, 255}, {255, 0, 255, 255}},
}

const block = 32

// displayToStored is the Exif definition of orientations 1–8: the stored
// pixel shown at displayed (x, y) of a w×h stored image.
func displayToStored(orientation, w, h, x, y int) (int, int) {
	switch orientation {
	case 2:
		return w - 1 - x, y
	case 3:
		return w - 1 - x, h - 1 - y
	case 4:
		return x, h - 1 - y
	case 5:
		return y, x
	case 6:
		return y, h - 1 - x
	case 7:
		return w - 1 - y, h - 1 - x
	case 8:
		return w - 1 - y, x
	default:
		return x, y
	}
}

// writeOrientedJPEG stores the block grid (blocks of size px) so that it
// displays upright with the given Exif orientation, in the given byte order.
func writeOrientedJPEG(t *testing.T, path string, orientation int, order binary.ByteOrder, size int) {
	t.Helper()
	dw, dh := 3*size, 2*size
	w, h := dw, dh
	if orientation >= 5 {
		w, h = dh, dw
	}
	stored := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			sx, sy := displayToStored(orientation, w, h, x, y)
			stored.Set(sx, sy, blockColors[y/size][x/size])
		}
	}
	var enc bytes.Buffer
	if err := jpeg.Encode(&enc, stored, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, withExifOrientation(enc.Bytes(), orientation, order), 0o600); err != nil {
		t.Fatal(err)
	}
}

// withExifOrientation inserts an APP1 Exif segment holding only an
// Orientation entry (SHORT) after the JPEG's SOI marker.
func withExifOrientation(jpg []byte, orientation int, order binary.ByteOrder) []byte {
	return withExif(jpg, orientation, order, 3)
}

// withExif is withExifOrientation with the entry's TIFF type: 3 SHORT, 4 LONG.
func withExif(jpg []byte, orientation int, order binary.ByteOrder, typ uint16) []byte {
	tiff := make([]byte, 26)
	if order == binary.BigEndian {
		copy(tiff, "MM")
	} else {
		copy(tiff, "II")
	}
	order.PutUint16(tiff[2:], 42)
	order.PutUint32(tiff[4:], 8)
	order.PutUint16(tiff[8:], 1)
	order.PutUint16(tiff[10:], 0x0112)
	order.PutUint16(tiff[12:], typ)
	order.PutUint32(tiff[14:], 1)
	if typ == 4 {
		order.PutUint32(tiff[18:], uint32(orientation))
	} else {
		order.PutUint16(tiff[18:], uint16(orientation))
	}
	return withAPP1(jpg, append([]byte("Exif\x00\x00"), tiff...))
}

// withXMP inserts an APP1 XMP segment after the JPEG's SOI marker.
func withXMP(jpg []byte, packet string) []byte {
	return withAPP1(jpg, append([]byte("http://ns.adobe.com/xap/1.0/\x00"), packet...))
}

func withAPP1(jpg, payload []byte) []byte {
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	out := append([]byte(nil), jpg[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, jpg[2:]...)
}

// storedGridJPEG is the block grid stored for the given orientation, without
// any metadata.
func storedGridJPEG(t *testing.T, orientation int) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "grid.jpg")
	writeOrientedJPEG(t, path, orientation, binary.BigEndian, block)
	img, err := gv.LoadImageFromFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	out, _, err := img.ExportJpeg(&gv.JpegExportParams{Quality: 95, StripMetadata: true})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// xmpPacket gives tiff:Orientation as an attribute or as an element.
func xmpPacket(orientation int, element bool) string {
	desc := fmt.Sprintf(`<rdf:Description rdf:about="" xmlns:tiff="http://ns.adobe.com/tiff/1.0/" tiff:Orientation="%d"/>`, orientation)
	if element {
		desc = fmt.Sprintf(`<rdf:Description rdf:about="" xmlns:tiff="http://ns.adobe.com/tiff/1.0/"><tiff:Orientation>%d</tiff:Orientation></rdf:Description>`, orientation)
	}
	return `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` + desc + `</rdf:RDF></x:xmpmeta>`
}

// orientedRegions are requests on the displayed 96×64 grid, with the output
// size and the grid block (row, column) at the output's top left.
var orientedRegions = []struct {
	region       string
	wantW, wantH int
	row, col     int
}{
	{"full", 96, 64, 0, 0},
	{"32,0,64,64", 64, 64, 0, 1},
	{"64,32,32,32", 32, 32, 1, 2},
	{"pct:0,50,100,50", 96, 32, 1, 0},
}

func TestTransformAppliesExifOrientation(t *testing.T) {
	root := t.TempDir()
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	p := New(op, Limits{})
	for _, order := range []binary.ByteOrder{binary.BigEndian, binary.LittleEndian} {
		for o := 1; o <= 8; o++ {
			name := fmt.Sprintf("o%d-%s.jpg", o, order)
			writeOrientedJPEG(t, filepath.Join(root, name), o, order, block)
			checkOrientedBlocks(t, p, name)
		}
	}
}

// PNG and WebP keep the orientation in their Exif chunk, TIFF in its
// Orientation tag, which libvips reads into the "orientation" field.
func TestTransformAppliesOrientationOfPNGWebPAndTIFF(t *testing.T) {
	root := t.TempDir()
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	p := New(op, Limits{})
	for _, o := range []int{2, 3, 6, 7, 8} {
		jpg := filepath.Join(root, fmt.Sprintf("o%d.jpg", o))
		writeOrientedJPEG(t, jpg, o, binary.BigEndian, block)
		img, err := gv.LoadImageFromFile(jpg, nil)
		if err != nil {
			t.Fatal(err)
		}
		png := gv.NewPngExportParams()
		webp := gv.NewWebpExportParams()
		webp.Lossless = true
		if err := img.SetOrientation(o); err != nil {
			t.Fatal(err)
		}
		for ext, export := range map[string]func() ([]byte, *gv.ImageMetadata, error){
			"png":  func() ([]byte, *gv.ImageMetadata, error) { return img.ExportPng(png) },
			"webp": func() ([]byte, *gv.ImageMetadata, error) { return img.ExportWebp(webp) },
			"tif":  func() ([]byte, *gv.ImageMetadata, error) { return img.ExportTiff(gv.NewTiffExportParams()) },
		} {
			name := fmt.Sprintf("o%d.%s", o, ext)
			out, _, err := export()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if err := os.WriteFile(filepath.Join(root, name), out, 0o600); err != nil {
				t.Fatal(err)
			}
			checkOrientedBlocks(t, p, name)
		}
		img.Close()
	}
}

func checkOrientedBlocks(t *testing.T, p *Pipeline, name string) {
	t.Helper()
	for _, tc := range orientedRegions {
		var buf bytes.Buffer
		res, err := p.Transform(context.Background(), mustParseImageRequest(t, name+"/"+tc.region+"/max/0/default.png"), &buf)
		if err != nil {
			t.Fatalf("%s %s: %v", name, tc.region, err)
		}
		if res.Width != tc.wantW || res.Height != tc.wantH {
			t.Fatalf("%s %s: size %dx%d, want %dx%d", name, tc.region, res.Width, res.Height, tc.wantW, tc.wantH)
		}
		img := decodePNG(t, buf.Bytes())
		for by := 0; by*block < tc.wantH; by++ {
			for bx := 0; bx*block < tc.wantW; bx++ {
				got := img.At(bx*block+block/2, by*block+block/2)
				if want := blockColors[tc.row+by][tc.col+bx]; !closeColor(got, want) {
					t.Fatalf("%s %s: block (%d,%d) = %v, want %v", name, tc.region, bx, by, got, want)
				}
			}
		}
	}
}

// A requested rotation or mirror turns the upright image, not the stored one.
func TestTransformRotatesOrientedJPEG(t *testing.T) {
	root := t.TempDir()
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	p := New(op, Limits{})
	for o := 1; o <= 8; o++ {
		name := fmt.Sprintf("o%d.jpg", o)
		writeOrientedJPEG(t, filepath.Join(root, name), o, binary.BigEndian, block)
		for _, tc := range []struct {
			rotation     string
			wantW, wantH int
			// shown gives the grid block (row, column) at output block (x, y).
			shown func(x, y int) (int, int)
		}{
			{"90", 64, 96, func(x, y int) (int, int) { return 1 - x, y }},
			{"180", 96, 64, func(x, y int) (int, int) { return 1 - y, 2 - x }},
			{"!0", 96, 64, func(x, y int) (int, int) { return y, 2 - x }},
		} {
			var buf bytes.Buffer
			res, err := p.Transform(context.Background(), mustParseImageRequest(t, name+"/full/max/"+tc.rotation+"/default.png"), &buf)
			if err != nil {
				t.Fatalf("%s %s: %v", name, tc.rotation, err)
			}
			if res.Width != tc.wantW || res.Height != tc.wantH {
				t.Fatalf("%s %s: size %dx%d, want %dx%d", name, tc.rotation, res.Width, res.Height, tc.wantW, tc.wantH)
			}
			img := decodePNG(t, buf.Bytes())
			for by := 0; by*block < tc.wantH; by++ {
				for bx := 0; bx*block < tc.wantW; bx++ {
					row, col := tc.shown(bx, by)
					if got, want := img.At(bx*block+block/2, by*block+block/2), blockColors[row][col]; !closeColor(got, want) {
						t.Fatalf("%s %s: block (%d,%d) = %v, want %v", name, tc.rotation, bx, by, got, want)
					}
				}
			}
		}
	}
}

func TestTransformMarksOrientedJPEGUpright(t *testing.T) {
	root := t.TempDir()
	writeOrientedJPEG(t, filepath.Join(root, "o6.jpg"), 6, binary.BigEndian, block)
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := New(op, Limits{}).Transform(context.Background(), mustParseImageRequest(t, "o6.jpg/0,0,64,64/max/0/default.jpg"), &buf); err != nil {
		t.Fatal(err)
	}
	out, err := gv.NewImageFromBuffer(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	exif := out.GetBlob("exif-data")
	e, ok := exifOrientation(exif)
	if !ok {
		t.Fatalf("derivative has no Exif orientation entry (preserve keeps the Exif block)")
	}
	if got := e.get(exif); got != 1 {
		t.Fatalf("derivative Exif orientation = %d, want 1", got)
	}
}

// Some files store the orientation as a LONG (Cantaloupe #548).
func TestTransformAppliesLongExifOrientation(t *testing.T) {
	root := t.TempDir()
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	p := New(op, Limits{})
	for _, order := range []binary.ByteOrder{binary.BigEndian, binary.LittleEndian} {
		for _, o := range []int{3, 6, 8} {
			name := fmt.Sprintf("long%d-%s.jpg", o, order)
			if err := os.WriteFile(filepath.Join(root, name), withExif(storedGridJPEG(t, o), o, order, 4), 0o600); err != nil {
				t.Fatal(err)
			}
			checkOrientedBlocks(t, p, name)
			exif := derivativeBlob(t, p, name, "exif-data")
			if e, ok := exifOrientation(exif); !ok || !e.long || e.get(exif) != 1 {
				t.Fatalf("%s: derivative Exif orientation entry %+v, want a LONG 1", name, e)
			}
		}
	}
}

// Without an Exif orientation, XMP tiff:Orientation applies, as in Cantaloupe;
// the derivative's XMP then says 1.
func TestTransformAppliesXMPOrientation(t *testing.T) {
	root := t.TempDir()
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	p := New(op, Limits{})
	for _, o := range []int{3, 6, 7} {
		for _, element := range []bool{false, true} {
			name := fmt.Sprintf("xmp%d-%t.jpg", o, element)
			if err := os.WriteFile(filepath.Join(root, name), withXMP(storedGridJPEG(t, o), xmpPacket(o, element)), 0o600); err != nil {
				t.Fatal(err)
			}
			checkOrientedBlocks(t, p, name)
			if v, at := xmpOrientation(derivativeBlob(t, p, name, "xmp-data")); at < 0 || v != 1 {
				t.Fatalf("%s: derivative XMP orientation %d (at %d), want 1", name, v, at)
			}
		}
	}
}

// Exif is the authority when both are present (MWG); a stale XMP value is ignored.
func TestTransformPrefersExifOverXMP(t *testing.T) {
	root := t.TempDir()
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	jpg := withXMP(withExifOrientation(storedGridJPEG(t, 1), 1, binary.BigEndian), xmpPacket(6, false))
	if err := os.WriteFile(filepath.Join(root, "both.jpg"), jpg, 0o600); err != nil {
		t.Fatal(err)
	}
	checkOrientedBlocks(t, New(op, Limits{}), "both.jpg")
}

// libvips reports a missing TIFF Orientation tag as 1, so for TIFF the XMP
// value applies only when the file has no Orientation tag.
func TestTransformAppliesXMPOrientationOfTIFF(t *testing.T) {
	root := t.TempDir()
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	p := New(op, Limits{})
	for _, tc := range []struct {
		name     string
		stored   int
		keepTag  bool
		xmpValue int
	}{
		{"xmp-only.tif", 6, false, 6},
		{"tag-and-xmp.tif", 1, true, 6},
	} {
		img, err := gv.NewImageFromBuffer(storedGridJPEG(t, tc.stored))
		if err != nil {
			t.Fatal(err)
		}
		setBlob(img, "xmp-data", []byte(xmpPacket(tc.xmpValue, false)))
		tif, _, err := img.ExportTiff(gv.NewTiffExportParams())
		img.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !tc.keepTag {
			tif = renameTIFFTag(t, tif, 0x0112, 0x0114)
		}
		path := filepath.Join(root, tc.name)
		if err := os.WriteFile(path, tif, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := tiffHasOrientationTag(path); got != tc.keepTag {
			t.Fatalf("%s: tiffHasOrientationTag = %t", tc.name, got)
		}
		checkOrientedBlocks(t, p, tc.name)
	}
}

// renameTIFFTag changes a tag number in IFD 0 of a little- or big-endian
// classic TIFF; to keeps the entries sorted when it lies between from's
// neighbours.
func renameTIFFTag(t *testing.T, tif []byte, from, to uint16) []byte {
	t.Helper()
	order := binary.ByteOrder(binary.LittleEndian)
	if string(tif[:2]) == "MM" {
		order = binary.BigEndian
	}
	out := append([]byte(nil), tif...)
	ifd := int(order.Uint32(out[4:8]))
	for i := 0; i < int(order.Uint16(out[ifd:])); i++ {
		if e := ifd + 2 + 12*i; order.Uint16(out[e:]) == from {
			order.PutUint16(out[e:], to)
			return out
		}
	}
	t.Fatalf("tag %d not in IFD 0", from)
	return nil
}

// derivativeBlob renders a full/max JPEG of name and returns one of its
// metadata blobs.
func derivativeBlob(t *testing.T, p *Pipeline, name, blob string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := p.Transform(context.Background(), mustParseImageRequest(t, name+"/full/max/0/default.jpg"), &buf); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	out, err := gv.NewImageFromBuffer(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	return out.GetBlob(blob)
}

// Full-region requests load sequentially, which can't feed a rotation of a
// source taller than libvips' sequential buffer.
func TestTransformFullRegionOfLargeOrientedJPEG(t *testing.T) {
	root := t.TempDir()
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	p := New(op, Limits{})
	for _, o := range []int{3, 6} {
		name := fmt.Sprintf("o%d.jpg", o)
		writeOrientedJPEG(t, filepath.Join(root, name), o, binary.BigEndian, 800)
		out, err := os.Create(filepath.Join(t.TempDir(), "out.jpg"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := p.Transform(context.Background(), mustParseImageRequest(t, name+"/full/max/0/default.jpg"), out)
		_ = out.Close()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Width != 2400 || res.Height != 1600 {
			t.Fatalf("%s: size %dx%d, want 2400x1600", name, res.Width, res.Height)
		}
	}
}

func TestStoredRegionMatchesDisplayToStored(t *testing.T) {
	const w, h = 7, 5
	for o := 1; o <= 8; o++ {
		dw, dh := w, h
		if o >= 5 {
			dw, dh = h, w
		}
		left, top, rw, rh := 1, 2, 2, 3
		sl, st, sw, sh := storedRegion(o, w, h, left, top, rw, rh)
		for y := top; y < top+rh; y++ {
			for x := left; x < left+rw; x++ {
				sx, sy := displayToStored(o, w, h, x, y)
				if sx < sl || sx >= sl+sw || sy < st || sy >= st+sh {
					t.Fatalf("orientation %d (%dx%d shown): displayed (%d,%d) is stored (%d,%d), outside %d,%d,%d,%d", o, dw, dh, x, y, sx, sy, sl, st, sw, sh)
				}
			}
		}
		if sw*sh != rw*rh {
			t.Fatalf("orientation %d: stored region %dx%d for a %dx%d region", o, sw, sh, rw, rh)
		}
	}
}

func TestExifOrientationIgnoresMalformedBlocks(t *testing.T) {
	for _, exif := range [][]byte{nil, []byte("Exif\x00\x00"), []byte("Exif\x00\x00MM\x00\x2a\xff\xff\xff\xff"), []byte("XX\x00\x2a\x00\x00\x00\x08")} {
		if _, ok := exifOrientation(exif); ok {
			t.Fatalf("exifOrientation(%q) found an entry", exif)
		}
	}
}

func FuzzExifOrientation(f *testing.F) {
	f.Add(withExifOrientation([]byte{0xFF, 0xD8}, 6, binary.BigEndian)[6:])
	f.Add(withExifOrientation([]byte{0xFF, 0xD8}, 8, binary.LittleEndian)[12:])
	f.Add(withExif([]byte{0xFF, 0xD8}, 6, binary.BigEndian, 4)[6:])
	f.Fuzz(func(t *testing.T, exif []byte) {
		if e, ok := exifOrientation(exif); ok {
			_ = e.get(exif)
			e.set(append([]byte(nil), exif...), 1)
		}
	})
}

func TestXMPOrientationResolvesNamespace(t *testing.T) {
	for _, tc := range []struct {
		xmp  string
		want int
	}{
		{xmpPacket(6, false), 6},
		{xmpPacket(8, true), 8},
		{`<rdf:Description xmlns:t="http://ns.adobe.com/tiff/1.0/" t:Orientation="3"/>`, 3},
		{`<x xmlns:t='http://ns.adobe.com/tiff/1.0/'><t:Orientation> 5 </t:Orientation></x>`, 5},
		// The prefix is bound to another namespace, or not bound at all.
		{`<rdf:Description xmlns:tiff="http://example.com/tiff/" tiff:Orientation="6"/>`, 0},
		{`<rdf:Description tiff:Orientation="6"/>`, 0},
		// Another property that ends in the same name.
		{`<rdf:Description xmlns:tiff="http://ns.adobe.com/tiff/1.0/" xtiff:Orientation="6"/>`, 0},
	} {
		v, at := xmpOrientation([]byte(tc.xmp))
		if at < 0 {
			v = 0
		}
		if v != tc.want {
			t.Errorf("xmpOrientation(%s) = %d, want %d", tc.xmp, v, tc.want)
		}
	}
}

func FuzzXMPOrientation(f *testing.F) {
	f.Add([]byte(xmpPacket(6, false)))
	f.Add([]byte(xmpPacket(8, true)))
	f.Fuzz(func(t *testing.T, xmp []byte) {
		if v, at := xmpOrientation(xmp); at >= 0 && (v < 1 || v > 8 || int(xmp[at]-'0') != v) {
			t.Fatalf("xmpOrientation = %d at %d", v, at)
		}
	})
}

func closeColor(got color.Color, want color.RGBA) bool {
	r, g, b, _ := got.RGBA()
	near := func(v uint32, w uint8) bool { d := int(v>>8) - int(w); return d > -40 && d < 40 }
	return near(r, want.R) && near(g, want.G) && near(b, want.B)
}
