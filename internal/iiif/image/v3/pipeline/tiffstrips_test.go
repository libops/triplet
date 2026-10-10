package pipeline

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"

	gv "github.com/davidbyttow/govips/v2/vips"
	"github.com/libops/triplet/internal/storage"
)

func TestStripWindowMatchesFullLoad(t *testing.T) {
	root := t.TempDir()
	writeSamplePNG(t, filepath.Join(root, "sample.png"))
	fixtures := map[string]string{
		"lzw.tif":     writeStripedVariant(t, root, "lzw.tif", gv.InterpretationSRGB, gv.TiffCompressionLzw),
		"deflate.tif": writeStripedVariant(t, root, "deflate.tif", gv.InterpretationRGB16, gv.TiffCompressionDeflate),
		"none.tif":    writeStripedVariant(t, root, "none.tif", gv.InterpretationBW, gv.TiffCompressionNone),
		"float.tif":   writeStripedFloat(t, root, "float.tif"),
		"jpeg.tif":    writeStripedJPEG(t, root, "jpeg.tif"),
	}
	palette := filepath.Join(root, "palette.tif")
	writeGrayStripedTIFF(t, palette, binary.LittleEndian, false, []tiffTestEntry{
		{tag: 262, typ: tiffTypeShort, data: []byte{3, 0}},
		{tag: 320, typ: tiffTypeShort, data: testColorMap()},
	})
	fixtures["palette.tif"] = palette
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for _, bigTIFF := range []bool{false, true} {
			path := filepath.Join(root, order.String()+map[bool]string{true: "-big", false: ""}[bigTIFF]+".tif")
			writeGrayStripedTIFF(t, path, order, bigTIFF, nil)
			fixtures[filepath.Base(path)] = path
		}
	}

	for name, path := range fixtures {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		l := readStripLayout(f)
		f.Close()
		if l == nil {
			t.Fatalf("%s: no strip layout", name)
		}
		for _, r := range [][2]int{{0, 3}, {l.rowsPerStrip - 1, 2}, {l.height / 2, 17}, {l.height - 5, 5}} {
			assertStripWindowMatches(t, name, path, l, r[0], r[1])
		}
	}
}

func TestStripWindowFromTempFile(t *testing.T) {
	inMemory := stripWindowInMemory
	stripWindowInMemory = func(uint64) bool { return false }
	t.Cleanup(func() { stripWindowInMemory = inMemory })

	root := t.TempDir()
	writeSamplePNG(t, filepath.Join(root, "sample.png"))
	path := writeStripedVariant(t, root, "lzw.tif", gv.InterpretationRGB16, gv.TiffCompressionLzw)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l := readStripLayout(f)
	f.Close()
	if l == nil {
		t.Fatal("no strip layout")
	}
	before, _ := filepath.Glob(filepath.Join(os.TempDir(), "triplet-strips-*.tif"))
	assertStripWindowMatches(t, "lzw.tif", path, l, l.height/2, 9)
	after, _ := filepath.Glob(filepath.Join(os.TempDir(), "triplet-strips-*.tif"))
	if len(after) > len(before) {
		t.Fatalf("temporary strip files left behind: %v", after)
	}
}

func TestStripWindowKeepsICCProfile(t *testing.T) {
	profile := loadNamedProfileForTest(t)
	path := filepath.Join(t.TempDir(), "icc.tif")
	writeGrayStripedTIFF(t, path, binary.LittleEndian, false, []tiffTestEntry{{tag: 34675, typ: 7, data: profile}})
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	l := readStripLayout(f)
	if l == nil {
		t.Fatal("no strip layout")
	}
	img, _, release, err := loadStripWindow(path, l, 40, 4, gv.NewImportParams())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	defer img.Close()
	if !bytes.Equal(img.GetICCProfile(), profile) {
		t.Fatal("ICC profile not carried into the strip window")
	}
}

func TestReadStripLayoutSkipsUnsupported(t *testing.T) {
	root := t.TempDir()
	writeSamplePNG(t, filepath.Join(root, "sample.png"))
	src, err := gv.LoadImageFromFile(filepath.Join(root, "sample.png"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	tiled, _, err := src.ExportTiff(&gv.TiffExportParams{Compression: gv.TiffCompressionLzw, Tile: true, TileWidth: 64, TileHeight: 64})
	if err != nil {
		t.Fatal(err)
	}
	oneStrip, _, err := src.ExportTiff(&gv.TiffExportParams{Compression: gv.TiffCompressionNone, TileHeight: 512})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"tiled.tif": tiled, "one-strip.tif": oneStrip, "png.tif": mustRead(t, filepath.Join(root, "sample.png"))} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if l := readStripLayout(f); l != nil {
			t.Errorf("%s: got a strip layout, want none", name)
		}
		f.Close()
	}
	for name, entry := range map[string]tiffTestEntry{
		"planar.tif":   {tag: tiffTagPlanarConfiguration, typ: tiffTypeShort, data: []byte{2, 0}},
		"old-jpeg.tif": {tag: tiffTagCompression, typ: tiffTypeShort, data: []byte{tiffCompressionOldJPEG, 0}},
	} {
		path := filepath.Join(root, name)
		writeGrayStripedTIFF(t, path, binary.LittleEndian, false, []tiffTestEntry{entry})
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if l := readStripLayout(f); l != nil {
			t.Errorf("%s: got a strip layout, want none", name)
		}
		f.Close()
	}
}

func FuzzStripWindow(f *testing.F) {
	root := f.TempDir()
	for i, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		path := filepath.Join(root, "seed.tif")
		writeGrayStripedTIFF(f, path, order, i == 1, nil)
		seed, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "fuzz.tif")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		l := readStripLayout(file)
		if l == nil {
			return
		}
		for _, r := range [][2]int{{0, 1}, {l.height - 1, 1}, {0, l.height}} {
			if w, err := l.window(file, r[0], r[1]); err == nil {
				_, _ = w.bytes(file)
				_ = w.writeTo(file, io.Discard)
			}
		}
	})
}

func TestTransformCropsStripedTIFF(t *testing.T) {
	root := t.TempDir()
	writeSamplePNG(t, filepath.Join(root, "sample.png"))
	path := writeStripedVariant(t, root, "striped.tif", gv.InterpretationRGB16, gv.TiffCompressionLzw)
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	p := New(op, Limits{})
	for _, region := range []string{"10,0,80,20", "10,40,80,20", "10,80,80,20", "pct:10,70,50,30", "square"} {
		var buf bytes.Buffer
		if _, err := p.Transform(context.Background(), mustParseImageRequest(t, "striped.tif/"+region+"/max/0/default.png"), &buf); err != nil {
			t.Fatalf("%s: transform: %v", region, err)
		}
		got := decodePNG(t, buf.Bytes())

		ref, err := gv.LoadImageFromFile(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req := mustParseImageRequest(t, "striped.tif/"+region+"/max/0/default.png")
		left, top, w, h, err := resolveRegion(req.Region, ref.Width(), ref.Height())
		if err != nil {
			t.Fatal(err)
		}
		if err := ref.ExtractArea(left, top, w, h); err != nil {
			t.Fatal(err)
		}
		out, _, err := ref.ExportPng(gv.NewPngExportParams())
		ref.Close()
		if err != nil {
			t.Fatal(err)
		}
		want := decodePNG(t, out)
		if got.Bounds() != want.Bounds() {
			t.Fatalf("%s: bounds %v, want %v", region, got.Bounds(), want.Bounds())
		}
		for y := 0; y < want.Bounds().Dy(); y++ {
			for x := 0; x < want.Bounds().Dx(); x++ {
				if got.At(x, y) != want.At(x, y) {
					t.Fatalf("%s: pixel %d,%d = %v, want %v", region, x, y, got.At(x, y), want.At(x, y))
				}
			}
		}
	}
}

func assertStripWindowMatches(t *testing.T, name, path string, l *stripLayout, top, rows int) {
	t.Helper()
	params := gv.NewImportParams()
	full, err := gv.LoadImageFromFileDirect(path, params)
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	win, windowTop, release, err := loadStripWindow(path, l, top, rows, params)
	if err != nil {
		t.Fatalf("%s rows %d+%d: %v", name, top, rows, err)
	}
	defer release()
	defer win.Close()
	if win.Height() >= l.height {
		t.Fatalf("%s rows %d+%d: window has all %d rows", name, top, rows, win.Height())
	}
	if err := full.ExtractArea(0, top, l.width, rows); err != nil {
		t.Fatal(err)
	}
	if err := win.ExtractArea(0, top-windowTop, l.width, rows); err != nil {
		t.Fatal(err)
	}
	a, err := full.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	b, err := win.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if full.Interpretation() != win.Interpretation() || !bytes.Equal(a, b) {
		t.Fatalf("%s rows %d+%d: strip window differs from full load", name, top, rows)
	}
}

// writeStripedVariant saves sample.png in root as a striped TIFF with 7-row strips.
func writeStripedVariant(t *testing.T, root, name string, space gv.Interpretation, compression gv.TiffCompression) string {
	t.Helper()
	img, err := gv.LoadImageFromFile(filepath.Join(root, "sample.png"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	if err := img.ToColorSpace(space); err != nil {
		t.Fatal(err)
	}
	out, _, err := img.ExportTiff(&gv.TiffExportParams{Compression: compression, Predictor: gv.TiffPredictorHorizontal, TileHeight: 7})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeStripedFloat saves sample.png as a 32-bit float TIFF with the floating
// point predictor.
func writeStripedFloat(t *testing.T, root, name string) string {
	t.Helper()
	img, err := gv.LoadImageFromFile(filepath.Join(root, "sample.png"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	if err := img.Cast(gv.BandFormatFloat); err != nil {
		t.Fatal(err)
	}
	out, _, err := img.ExportTiff(&gv.TiffExportParams{Compression: gv.TiffCompressionDeflate, Predictor: gv.TiffPredictorFloat, TileHeight: 7})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeStripedJPEG saves sample.png as a JPEG/YCbCr TIFF with 16-row strips and
// shared JPEGTables; its 100 rows leave a short last strip.
func writeStripedJPEG(t *testing.T, root, name string) string {
	t.Helper()
	img, err := gv.LoadImageFromFile(filepath.Join(root, "sample.png"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	if err := img.ExtractBand(0, 3); err != nil {
		t.Fatal(err)
	}
	out, _, err := img.ExportTiff(&gv.TiffExportParams{Compression: gv.TiffCompressionJpeg, Quality: 75, TileHeight: 16})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	l := readStripLayout(f)
	if l == nil {
		t.Fatalf("%s: no strip layout", name)
	}
	var ycbcr, tables bool
	for _, e := range l.entries {
		ycbcr = ycbcr || e.tag == 262 && l.order.Uint16(e.field) == 6
		tables = tables || e.tag == 347
	}
	if !ycbcr || !tables {
		t.Fatalf("%s: YCbCr %v, JPEGTables %v; want both", name, ycbcr, tables)
	}
	return path
}

// testColorMap returns a little-endian 8-bit TIFF ColorMap.
func testColorMap() []byte {
	b := make([]byte, 3*256*2)
	for i := 0; i < 3*256; i++ {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(i*997%65536))
	}
	return b
}

type tiffTestEntry struct {
	tag, typ uint16
	data     []byte
}

// writeGrayStripedTIFF writes an uncompressed 64x50 gray TIFF with 4-row strips
// by hand, so tests can cover byte orders, BigTIFF and unusual tags.
func writeGrayStripedTIFF(t testing.TB, path string, order binary.ByteOrder, bigTIFF bool, extra []tiffTestEntry) {
	t.Helper()
	const width, height, rps = 64, 50, 4
	strips := (height + rps - 1) / rps
	pixels := make([]byte, width*height)
	for i := range pixels {
		pixels[i] = byte(i*7 + i/width*13)
	}
	short := func(v uint16) []byte { b := make([]byte, 2); order.PutUint16(b, v); return b }
	offType, offSize := uint16(tiffTypeLong), 4
	if bigTIFF {
		offType, offSize = tiffTypeLong8, 8
	}
	offsets, counts := make([]byte, strips*offSize), make([]byte, strips*offSize)
	entries := []tiffTestEntry{
		{tag: tiffTagImageWidth, typ: tiffTypeShort, data: short(width)},
		{tag: tiffTagImageLength, typ: tiffTypeShort, data: short(height)},
		{tag: 258, typ: tiffTypeShort, data: short(8)},
		{tag: tiffTagCompression, typ: tiffTypeShort, data: short(1)},
		{tag: 262, typ: tiffTypeShort, data: short(1)},
		{tag: tiffTagStripOffsets, typ: offType, data: offsets},
		{tag: 277, typ: tiffTypeShort, data: short(1)},
		{tag: tiffTagRowsPerStrip, typ: tiffTypeShort, data: short(rps)},
		{tag: tiffTagStripByteCounts, typ: offType, data: counts},
	}
	for _, e := range extra {
		replaced := false
		for i := range entries {
			if entries[i].tag == e.tag {
				entries[i], replaced = e, true
			}
		}
		if !replaced {
			entries = append(entries, e)
		}
	}
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].tag < entries[j-1].tag; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}

	headerSize, countSize, entrySize, field := 8, 2, 12, 4
	if bigTIFF {
		headerSize, countSize, entrySize, field = 16, 8, 20, 8
	}
	putOff := func(b []byte, v int) {
		if bigTIFF {
			order.PutUint64(b, uint64(v))
		} else {
			order.PutUint32(b, uint32(v))
		}
	}
	pos := headerSize + countSize + len(entries)*entrySize + field
	valueAt := make([]int, len(entries))
	for i, e := range entries {
		if len(e.data) > field {
			valueAt[i] = pos
			pos += len(e.data) + len(e.data)%2
		}
	}
	for s := 0; s < strips; s++ {
		n := min(rps, height-s*rps) * width
		putOff(offsets[s*offSize:], pos)
		putOff(counts[s*offSize:], n)
		pos += n
	}

	buf := make([]byte, pos)
	copy(buf, "MM")
	if order == binary.LittleEndian {
		copy(buf, "II")
	}
	if bigTIFF {
		order.PutUint16(buf[2:], 43)
		order.PutUint16(buf[4:], 8)
		order.PutUint64(buf[8:], uint64(headerSize))
		order.PutUint64(buf[headerSize:], uint64(len(entries)))
	} else {
		order.PutUint16(buf[2:], 42)
		order.PutUint32(buf[4:], uint32(headerSize))
		order.PutUint16(buf[headerSize:], uint16(len(entries)))
	}
	for i, e := range entries {
		raw := buf[headerSize+countSize+i*entrySize:]
		order.PutUint16(raw, e.tag)
		order.PutUint16(raw[2:], e.typ)
		count := len(e.data) / tiffTypeSizes[e.typ]
		value := raw[8:]
		if bigTIFF {
			order.PutUint64(raw[4:], uint64(count))
			value = raw[12:]
		} else {
			order.PutUint32(raw[4:], uint32(count))
		}
		if valueAt[i] > 0 {
			putOff(value, valueAt[i])
			copy(buf[valueAt[i]:], e.data)
		} else {
			copy(value, e.data)
		}
	}
	copy(buf[pos-len(pixels):], pixels)
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
