package handler

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"io"
	"math"
	"os"
	"strconv"

	gv "github.com/davidbyttow/govips/v2/vips"
	"github.com/libops/triplet/internal/iiif/image/v3/types"
)

const (
	maxXMPBytes  = 1 << 20
	maxXMPDepth  = 32
	maxPNGChunks = 64
	xmpRightsNS  = "http://ns.adobe.com/xap/1.0/rights/"
)

// mmPerPixel returns the pixel size the file declares, or 0 when it declares
// none. libvips sets resolution-unit only when the file has one, unlike
// xres/yres, which default to 1 pixel/mm (72 dpi for PNG), so PNG is read directly.
func mmPerPixel(img *gv.ImageRef, path string) float64 {
	if img.Format() == gv.ImageTypePNG {
		return pixelSize(pngPixelsPerMM(path))
	}
	if unit := img.GetString("resolution-unit"); unit != "in" && unit != "cm" {
		return 0
	}
	return pixelSize(img.ResX(), img.ResY())
}

// pixelSize converts pixels per mm to mm per pixel, or 0 when they are not a
// usable square resolution.
func pixelSize(xres, yres float64) float64 {
	if !(xres > 0) || !(yres > 0) || math.Abs(xres-yres) > 0.01*xres {
		return 0
	}
	scale := 1 / xres
	if !(scale > 0) || math.IsInf(scale, 0) {
		return 0
	}
	// libvips derives xres in float32; 7 digits drop that noise (400 dpi is 0.0635).
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(scale, 'g', 7, 64), 64)
	if err != nil {
		return 0
	}
	return rounded
}

// pngPixelsPerMM returns the pHYs resolution of a PNG, or 0, 0 when it has none
// or its unit is unknown.
func pngPixelsPerMM(path string) (float64, float64) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var sig [8]byte
	if _, err := io.ReadFull(f, sig[:]); err != nil || string(sig[:]) != "\x89PNG\r\n\x1a\n" {
		return 0, 0
	}
	for range maxPNGChunks {
		var head [8]byte
		if _, err := io.ReadFull(f, head[:]); err != nil {
			return 0, 0
		}
		size := int64(binary.BigEndian.Uint32(head[:4]))
		switch string(head[4:]) {
		case "IDAT", "IEND":
			return 0, 0
		case "pHYs":
			var phys [9]byte
			if size != int64(len(phys)) {
				return 0, 0
			}
			if _, err := io.ReadFull(f, phys[:]); err != nil || phys[8] != 1 {
				return 0, 0
			}
			return float64(binary.BigEndian.Uint32(phys[:4])) / 1000, float64(binary.BigEndian.Uint32(phys[4:8])) / 1000
		}
		if _, err := f.Seek(size+4, io.SeekCurrent); err != nil {
			return 0, 0
		}
	}
	return 0, 0
}

// webStatementRights returns the normalized xmpRights:WebStatement URI of an
// XMP packet, or "" when it is absent or not a recognised license URI.
func webStatementRights(xmp []byte) string {
	if len(xmp) > maxXMPBytes {
		xmp = xmp[:maxXMPBytes]
	}
	// Cheap filter: most packets never mention it.
	if !bytes.Contains(xmp, []byte("WebStatement")) {
		return ""
	}
	dec := xml.NewDecoder(bytes.NewReader(xmp))
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.EndElement:
			depth--
		case xml.StartElement:
			if depth++; depth > maxXMPDepth {
				return ""
			}
			for _, a := range t.Attr {
				if a.Name.Space == xmpRightsNS && a.Name.Local == "WebStatement" {
					if r, ok := types.NormalizeRights(a.Value); ok {
						return r
					}
				}
			}
			if t.Name.Space == xmpRightsNS && t.Name.Local == "WebStatement" {
				var value string
				if err := dec.DecodeElement(&value, &t); err != nil {
					return ""
				}
				depth--
				if r, ok := types.NormalizeRights(value); ok {
					return r
				}
			}
		}
	}
}
