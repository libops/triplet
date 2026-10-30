package handler

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gv "github.com/davidbyttow/govips/v2/vips"
	"github.com/libops/triplet/internal/cache"
	"github.com/libops/triplet/internal/iiif/image/v3/schema"
	"github.com/libops/triplet/internal/iiif/image/v3/types"
)

// tiffSpec describes a 2x2 grayscale TIFF; zero values omit the tag.
type tiffSpec struct {
	xres, yres uint32 // pixels per unit
	unit       uint16
	xmp        string
}

type tiffEntry struct {
	tag, typ uint16
	count    uint32
	value    uint32
	data     []byte
}

func buildTIFF(s tiffSpec) []byte {
	pixels := []byte{0, 64, 128, 255}
	entries := []tiffEntry{
		{tag: 256, typ: 4, count: 1, value: 2},
		{tag: 257, typ: 4, count: 1, value: 2},
		{tag: 258, typ: 3, count: 1, value: 8},
		{tag: 259, typ: 3, count: 1, value: 1},
		{tag: 262, typ: 3, count: 1, value: 1},
		{tag: 273, typ: 4, count: 1}, // strip offset, set below
		{tag: 277, typ: 3, count: 1, value: 1},
		{tag: 278, typ: 4, count: 1, value: 2},
		{tag: 279, typ: 4, count: 1, value: uint32(len(pixels))},
	}
	rational := func(tag uint16, perUnit uint32) tiffEntry {
		data := binary.LittleEndian.AppendUint32(nil, perUnit)
		return tiffEntry{tag: tag, typ: 5, count: 1, data: binary.LittleEndian.AppendUint32(data, 1)}
	}
	if s.xres != 0 {
		entries = append(entries, rational(282, s.xres))
	}
	if s.yres != 0 {
		entries = append(entries, rational(283, s.yres))
	}
	if s.unit != 0 {
		entries = append(entries, tiffEntry{tag: 296, typ: 3, count: 1, value: uint32(s.unit)})
	}
	if s.xmp != "" {
		entries = append(entries, tiffEntry{tag: 700, typ: 1, count: uint32(len(s.xmp)), data: []byte(s.xmp)})
	}

	pixelsAt := 8 + 2 + 12*len(entries) + 4
	var extra bytes.Buffer
	for i := range entries {
		e := &entries[i]
		switch {
		case e.tag == 273:
			e.value = uint32(pixelsAt)
		case e.data != nil:
			e.value = uint32(pixelsAt + len(pixels) + extra.Len())
			extra.Write(e.data)
			if extra.Len()%2 == 1 {
				extra.WriteByte(0)
			}
		}
	}

	var out bytes.Buffer
	out.WriteString("II")
	out.Write(binary.LittleEndian.AppendUint16(nil, 42))
	out.Write(binary.LittleEndian.AppendUint32(nil, 8))
	out.Write(binary.LittleEndian.AppendUint16(nil, uint16(len(entries))))
	for _, e := range entries {
		out.Write(binary.LittleEndian.AppendUint16(nil, e.tag))
		out.Write(binary.LittleEndian.AppendUint16(nil, e.typ))
		out.Write(binary.LittleEndian.AppendUint32(nil, e.count))
		out.Write(binary.LittleEndian.AppendUint32(nil, e.value))
	}
	out.Write(binary.LittleEndian.AppendUint32(nil, 0))
	out.Write(pixels)
	out.Write(extra.Bytes())
	return out.Bytes()
}

func xmpPacket(rdfDescription string) string {
	return `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		rdfDescription + `</rdf:RDF></x:xmpmeta><?xpacket end="w"?>`
}

func writeTIFF(t *testing.T, dir, name string, s tiffSpec) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), buildTIFF(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func jfifJPEG(t *testing.T, units byte, x, y uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	app0 := []byte{0xFF, 0xE0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, units, byte(x >> 8), byte(x), byte(y >> 8), byte(y), 0, 0}
	return append(append(b[:2:2], app0...), b[2:]...)
}

// pngWithPHYs returns an 8x8 PNG; unit 0 means unknown, 1 pixels per metre.
// A zero ppm omits the chunk.
func pngWithPHYs(t *testing.T, x, y uint32, unit byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if x == 0 {
		return b
	}
	chunk := binary.BigEndian.AppendUint32(nil, 9)
	chunk = append(chunk, "pHYs"...)
	chunk = binary.BigEndian.AppendUint32(chunk, x)
	chunk = binary.BigEndian.AppendUint32(chunk, y)
	chunk = append(chunk, unit)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	const afterIHDR = 8 + 25
	return append(append(append([]byte{}, b[:afterIHDR]...), chunk...), b[afterIHDR:]...)
}

func TestMMPerPixel(t *testing.T) {
	cases := []struct {
		name string
		file string
		data []byte
		want float64
	}{
		{"tiff inch", "a.tif", buildTIFF(tiffSpec{xres: 400, yres: 400, unit: 2}), 0.0635},
		{"tiff default unit is inch", "a.tif", buildTIFF(tiffSpec{xres: 400, yres: 400}), 0.0635},
		{"tiff centimeter", "a.tif", buildTIFF(tiffSpec{xres: 200, yres: 200, unit: 3}), 0.05},
		{"tiff 72 dpi", "a.tif", buildTIFF(tiffSpec{xres: 72, yres: 72, unit: 2}), 0.3527778},
		{"tiff no resolution", "a.tif", buildTIFF(tiffSpec{}), 0},
		{"tiff unit none", "a.tif", buildTIFF(tiffSpec{xres: 400, yres: 400, unit: 1}), 0},
		{"tiff non-square pixels", "a.tif", buildTIFF(tiffSpec{xres: 400, yres: 200, unit: 2}), 0},
		{"jpeg dots per inch", "a.jpg", jfifJPEG(t, 1, 300, 300), 0.08466667},
		{"jpeg dots per cm", "a.jpg", jfifJPEG(t, 2, 100, 100), 0.1},
		{"jpeg aspect ratio only", "a.jpg", jfifJPEG(t, 0, 1, 1), 0},
		{"png pixels per metre", "a.png", pngWithPHYs(t, 10000, 10000, 1), 0.1},
		{"png 100 dpi", "a.png", pngWithPHYs(t, 3937, 3937, 1), 0.2540005},
		{"png unknown unit", "a.png", pngWithPHYs(t, 3000, 3000, 0), 0},
		{"png without pHYs", "a.png", pngWithPHYs(t, 0, 0, 0), 0},
		{"png non-square pixels", "a.png", pngWithPHYs(t, 10000, 5000, 1), 0},
		{"png zero resolution", "a.png", pngWithPHYs(t, 10000, 0, 1), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.file)
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			img, err := gv.LoadImageFromFileDirect(path, gv.NewImportParams())
			if err != nil {
				t.Fatal(err)
			}
			defer img.Close()
			if got := mmPerPixel(img, path); got != tc.want {
				t.Fatalf("mmPerPixel = %v, want %v", got, tc.want)
			}
		})
	}
}
func TestWebStatementRights(t *testing.T) {
	const ns = `xmlns:xmpRights="http://ns.adobe.com/xap/1.0/rights/"`
	cases := []struct {
		name string
		xmp  string
		want string
	}{
		{"element", xmpPacket(`<rdf:Description rdf:about="" ` + ns + `><xmpRights:WebStatement>https://creativecommons.org/licenses/by/4.0/</xmpRights:WebStatement></rdf:Description>`), "http://creativecommons.org/licenses/by/4.0/"},
		{"attribute", xmpPacket(`<rdf:Description rdf:about="" ` + ns + ` xmpRights:WebStatement="http://rightsstatements.org/vocab/InC-EDU/1.0/"/>`), "http://rightsstatements.org/vocab/InC-EDU/1.0/"},
		{"other prefix", xmpPacket(`<rdf:Description rdf:about="" xmlns:r="http://ns.adobe.com/xap/1.0/rights/"><r:WebStatement>https://creativecommons.org/publicdomain/zero/1.0/</r:WebStatement></rdf:Description>`), "http://creativecommons.org/publicdomain/zero/1.0/"},
		{"unrecognised", xmpPacket(`<rdf:Description rdf:about="" ` + ns + `><xmpRights:WebStatement>https://example.org/terms</xmpRights:WebStatement></rdf:Description>`), ""},
		{"wrong namespace", xmpPacket(`<rdf:Description rdf:about="" xmlns:x="http://example.org/ns/"><x:WebStatement>https://creativecommons.org/licenses/by/4.0/</x:WebStatement></rdf:Description>`), ""},
		{"absent", xmpPacket(`<rdf:Description rdf:about=""/>`), ""},
		{"second statement wins", xmpPacket(`<rdf:Description rdf:about="" ` + ns + `><xmpRights:WebStatement>https://example.org/terms</xmpRights:WebStatement></rdf:Description><rdf:Description rdf:about="" ` + ns + ` xmpRights:WebStatement="http://rightsstatements.org/vocab/UND/1.0/"/>`), "http://rightsstatements.org/vocab/UND/1.0/"},
		{"nested within depth limit", xmpPacket(strings.Repeat("<a>", 20) + `<rdf:Description rdf:about="" ` + ns + ` xmpRights:WebStatement="http://rightsstatements.org/vocab/UND/1.0/"/>` + strings.Repeat("</a>", 20)), "http://rightsstatements.org/vocab/UND/1.0/"},
		{"nested beyond depth limit", xmpPacket(strings.Repeat("<a>", 40) + `<rdf:Description rdf:about="" ` + ns + ` xmpRights:WebStatement="http://rightsstatements.org/vocab/UND/1.0/"/>` + strings.Repeat("</a>", 40)), ""},
		{"not xml", "\x00\x01 not xml", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := webStatementRights([]byte(tc.xmp)); got != tc.want {
				t.Fatalf("webStatementRights = %q, want %q", got, tc.want)
			}
		})
	}
}

func getInfo(t *testing.T, srv, id string) (map[string]any, []byte) {
	t.Helper()
	resp, err := http.Get(srv + "/iiif/3/" + id + "/info.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc, body.Bytes()
}

func TestInfoJSONSourceMetadata(t *testing.T) {
	xmp := xmpPacket(`<rdf:Description rdf:about="" xmlns:xmpRights="http://ns.adobe.com/xap/1.0/rights/"><xmpRights:WebStatement>https://creativecommons.org/licenses/by/4.0/</xmpRights:WebStatement></rdf:Description>`)
	cases := []struct {
		name         string
		meta         types.InfoMetadata
		spec         tiffSpec
		wantPhysical bool
		wantRights   string
	}{
		{"both disabled", types.InfoMetadata{}, tiffSpec{xres: 400, yres: 400, unit: 2, xmp: xmp}, false, ""},
		{"physical only", types.InfoMetadata{PhysicalDimensions: true}, tiffSpec{xres: 400, yres: 400, unit: 2, xmp: xmp}, true, ""},
		{"rights only", types.InfoMetadata{RightsFromWebStatement: true}, tiffSpec{xres: 400, yres: 400, unit: 2, xmp: xmp}, false, "http://creativecommons.org/licenses/by/4.0/"},
		{"both enabled", types.InfoMetadata{PhysicalDimensions: true, RightsFromWebStatement: true}, tiffSpec{xres: 400, yres: 400, unit: 2, xmp: xmp}, true, "http://creativecommons.org/licenses/by/4.0/"},
		{"enabled but source has none", types.InfoMetadata{PhysicalDimensions: true, RightsFromWebStatement: true}, tiffSpec{}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, root := setupTestServerWithInfoMetadata(t, cache.Noop{}, nil, "", tc.meta)
			defer srv.Close()
			writeTIFF(t, root, "scan.tif", tc.spec)
			doc, raw := getInfo(t, srv.URL, "scan.tif")
			if _, cached := getInfo(t, srv.URL, "scan.tif"); !bytes.Equal(raw, cached) {
				t.Errorf("info.json from the dimension cache differs:\n%s\n%s", raw, cached)
			}
			if err := schema.ValidateInfoBytes(raw); err != nil {
				t.Fatalf("invalid info.json: %v\n%s", err, raw)
			}
			rights, _ := doc["rights"].(string)
			if rights != tc.wantRights {
				t.Errorf("rights = %q, want %q", rights, tc.wantRights)
			}
			services, _ := doc["service"].([]any)
			if !tc.wantPhysical {
				if len(services) != 0 {
					t.Fatalf("service = %v, want none", services)
				}
				return
			}
			if len(services) != 1 {
				t.Fatalf("service = %v", services)
			}
			svc := services[0].(map[string]any)
			want := map[string]any{
				"@context":      types.PhysDimContext,
				"id":            "http://example.test/iiif/3/scan.tif/info.json",
				"type":          "Service",
				"profile":       types.PhysDimProfile,
				"physicalScale": 0.0635,
				"physicalUnits": "mm",
			}
			if len(svc) != len(want) {
				t.Errorf("service = %v", svc)
			}
			for k, v := range want {
				if svc[k] != v {
					t.Errorf("service[%q] = %v, want %v", k, svc[k], v)
				}
			}
		})
	}
}
