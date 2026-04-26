// Package parse decodes IIIF Image API 3.0 request URLs into structured form.
//
// The grammar implemented here is from
// https://iiif.io/api/image/3.0/#21-image-request-uri-syntax. The parser is
// intentionally strict: malformed components return [ErrSyntax] with a
// component-specific cause, never a "best effort" interpretation.
package parse

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	maxParsedDimension = 1_000_000
	maxParsedPercent   = 10_000
)

// ErrSyntax is returned when a URL component does not match the spec grammar.
var ErrSyntax = errors.New("iiif: syntax error")

// Kind identifies the shape of a parsed request.
type Kind int

const (
	// KindImage is a full image request:
	// /{prefix}/{id}/{region}/{size}/{rotation}/{quality}.{format}.
	KindImage Kind = iota
	// KindInfo is /{prefix}/{id}/info.json.
	KindInfo
	// KindBase is /{prefix}/{id} — must 303-redirect to info.json.
	KindBase
)

// Request is the parsed form of an Image API URL.
type Request struct {
	Kind       Kind
	Identifier string
	Region     Region
	Size       Size
	Rotation   Rotation
	Quality    Quality
	Format     Format
}

// RegionKind enumerates IIIF region selectors.
type RegionKind int

const (
	// RegionFull selects the entire image (`full`).
	RegionFull RegionKind = iota
	// RegionSquare selects a centered square (`square`).
	RegionSquare
	// RegionPixels selects an absolute pixel rectangle (`x,y,w,h`).
	RegionPixels
	// RegionPercent selects a percentage rectangle (`pct:x,y,w,h`).
	RegionPercent
)

// Region is a parsed region selector. For RegionPixels the four fields are
// integers; for RegionPercent they are floats in [0,100].
type Region struct {
	Kind       RegionKind
	X, Y, W, H float64
}

// SizeKind enumerates IIIF size selectors.
type SizeKind int

const (
	// SizeMax requests the largest size up to the source dimensions.
	SizeMax SizeKind = iota
	// SizeMaxUp allows upscaling (`^max`).
	SizeMaxUp
	// SizeWidth requests `w,` (height computed from aspect).
	SizeWidth
	// SizeHeight requests `,h`.
	SizeHeight
	// SizePercent requests `pct:n`.
	SizePercent
	// SizeWH requests an exact `w,h` (may distort aspect).
	SizeWH
	// SizeBestFit requests `!w,h` — fit within w×h preserving aspect.
	SizeBestFit
)

// Size is a parsed size selector. Upscale is true when prefixed with `^`.
type Size struct {
	Kind    SizeKind
	W, H    int
	Percent float64
	Upscale bool
}

// Rotation is a parsed rotation in degrees, with optional horizontal mirror.
type Rotation struct {
	Degrees float64
	Mirror  bool
}

// Quality is a IIIF image quality.
type Quality string

// Quality values defined by the spec.
const (
	QualityDefault Quality = "default"
	QualityColor   Quality = "color"
	QualityGray    Quality = "gray"
	QualityBitonal Quality = "bitonal"
)

// Format is a IIIF output format extension.
type Format string

// Format values defined by the spec.
const (
	FormatJPG  Format = "jpg"
	FormatTIF  Format = "tif"
	FormatPNG  Format = "png"
	FormatGIF  Format = "gif"
	FormatJP2  Format = "jp2"
	FormatPDF  Format = "pdf"
	FormatWEBP Format = "webp"
)

// Parse decodes a URL path beneath the configured Image API prefix into a
// Request. The prefix is the leading path the server is mounted at (e.g.
// `/iiif/3`); rest is everything that follows it, with or without a leading
// slash.
//
// Identifiers are URL-decoded per RFC 3986. A trailing component of
// `info.json` produces a KindInfo request; a bare identifier produces
// KindBase, which the caller must redirect to the info document.
func Parse(rest string) (Request, error) {
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return Request{}, fmt.Errorf("%w: empty path", ErrSyntax)
	}

	parts := strings.Split(rest, "/")
	if len(parts) >= 2 && parts[len(parts)-1] == "info.json" {
		id, err := decodeIdentifier(strings.Join(parts[:len(parts)-1], "/"))
		if err != nil {
			return Request{}, err
		}
		return Request{Kind: KindInfo, Identifier: id}, nil
	}
	if len(parts) >= 5 && strings.Contains(parts[len(parts)-1], ".") {
		return parseImage(parts)
	}
	if len(parts) >= 5 {
		return Request{}, fmt.Errorf("%w: expected {quality}.{format}, got %q", ErrSyntax, parts[len(parts)-1])
	}
	if len(parts) == 2 {
		return Request{}, fmt.Errorf("%w: expected info.json, got %q", ErrSyntax, parts[1])
	}
	if len(parts) > 1 && len(parts) < 5 {
		return Request{}, fmt.Errorf("%w: expected image request or info.json, got %d path segments", ErrSyntax, len(parts))
	}
	id, err := decodeIdentifier(rest)
	if err != nil {
		return Request{}, err
	}
	return Request{Kind: KindBase, Identifier: id}, nil
}

func parseImage(parts []string) (Request, error) {
	id, err := decodeIdentifier(strings.Join(parts[:len(parts)-4], "/"))
	if err != nil {
		return Request{}, err
	}
	tail := parts[len(parts)-4:]
	region, err := parseRegion(tail[0])
	if err != nil {
		return Request{}, err
	}
	size, err := parseSize(tail[1])
	if err != nil {
		return Request{}, err
	}
	rot, err := parseRotation(tail[2])
	if err != nil {
		return Request{}, err
	}
	qualityFormat := tail[3]
	dot := strings.LastIndexByte(qualityFormat, '.')
	if dot < 1 || dot == len(qualityFormat)-1 {
		return Request{}, fmt.Errorf("%w: expected {quality}.{format}, got %q", ErrSyntax, qualityFormat)
	}
	quality, err := parseQuality(qualityFormat[:dot])
	if err != nil {
		return Request{}, err
	}
	format, err := parseFormat(qualityFormat[dot+1:])
	if err != nil {
		return Request{}, err
	}
	return Request{
		Kind:       KindImage,
		Identifier: id,
		Region:     region,
		Size:       size,
		Rotation:   rot,
		Quality:    quality,
		Format:     format,
	}, nil
}

func decodeIdentifier(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty identifier", ErrSyntax)
	}
	id, err := url.PathUnescape(s)
	if err != nil {
		return "", fmt.Errorf("%w: identifier: %v", ErrSyntax, err)
	}
	if strings.ContainsAny(id, "\x00\n\r") {
		return "", fmt.Errorf("%w: identifier contains illegal control character", ErrSyntax)
	}
	return id, nil
}

func parseRegion(s string) (Region, error) {
	switch s {
	case "":
		return Region{}, fmt.Errorf("%w: empty region", ErrSyntax)
	case "full":
		return Region{Kind: RegionFull}, nil
	case "square":
		return Region{Kind: RegionSquare}, nil
	}
	pct := strings.HasPrefix(s, "pct:")
	body := strings.TrimPrefix(s, "pct:")
	parts := strings.Split(body, ",")
	if len(parts) != 4 {
		return Region{}, fmt.Errorf("%w: region %q: expected 4 comma-separated values", ErrSyntax, s)
	}
	vals := [4]float64{}
	for i, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return Region{}, fmt.Errorf("%w: region %q: %v", ErrSyntax, s, err)
		}
		vals[i] = v
	}
	if vals[2] <= 0 || vals[3] <= 0 {
		return Region{}, fmt.Errorf("%w: region %q: width and height must be > 0", ErrSyntax, s)
	}
	if vals[0] < 0 || vals[1] < 0 {
		return Region{}, fmt.Errorf("%w: region %q: x and y must be >= 0", ErrSyntax, s)
	}
	if pct {
		if vals[0] > 100 || vals[1] > 100 || vals[2] > 100 || vals[3] > 100 {
			return Region{}, fmt.Errorf("%w: region %q: percent values must be <= 100", ErrSyntax, s)
		}
		return Region{Kind: RegionPercent, X: vals[0], Y: vals[1], W: vals[2], H: vals[3]}, nil
	}
	return Region{Kind: RegionPixels, X: vals[0], Y: vals[1], W: vals[2], H: vals[3]}, nil
}

func parseSize(s string) (Size, error) {
	if s == "" {
		return Size{}, fmt.Errorf("%w: empty size", ErrSyntax)
	}
	upscale := false
	if strings.HasPrefix(s, "^") {
		upscale = true
		s = s[1:]
	}
	if s == "max" {
		if upscale {
			return Size{Kind: SizeMaxUp, Upscale: true}, nil
		}
		return Size{Kind: SizeMax}, nil
	}
	if strings.HasPrefix(s, "pct:") {
		v, err := strconv.ParseFloat(s[4:], 64)
		if err != nil || v <= 0 {
			return Size{}, fmt.Errorf("%w: size pct:%s: must be positive number", ErrSyntax, s[4:])
		}
		if v > maxParsedPercent {
			return Size{}, fmt.Errorf("%w: size pct:%s: must be <= %d", ErrSyntax, s[4:], maxParsedPercent)
		}
		return Size{Kind: SizePercent, Percent: v, Upscale: upscale}, nil
	}
	bestFit := false
	if strings.HasPrefix(s, "!") {
		bestFit = true
		s = s[1:]
	}
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return Size{}, fmt.Errorf("%w: size %q: expected w,h or w, or ,h", ErrSyntax, s)
	}
	w, h := parts[0], parts[1]
	if bestFit {
		wi, hi, err := parseDims(w, h)
		if err != nil {
			return Size{}, fmt.Errorf("%w: size !%s,%s: %v", ErrSyntax, w, h, err)
		}
		return Size{Kind: SizeBestFit, W: wi, H: hi, Upscale: upscale}, nil
	}
	switch {
	case w != "" && h == "":
		wi, err := parsePosInt(w)
		if err != nil {
			return Size{}, fmt.Errorf("%w: size %s,: %v", ErrSyntax, w, err)
		}
		return Size{Kind: SizeWidth, W: wi, Upscale: upscale}, nil
	case w == "" && h != "":
		hi, err := parsePosInt(h)
		if err != nil {
			return Size{}, fmt.Errorf("%w: size ,%s: %v", ErrSyntax, h, err)
		}
		return Size{Kind: SizeHeight, H: hi, Upscale: upscale}, nil
	case w != "" && h != "":
		wi, hi, err := parseDims(w, h)
		if err != nil {
			return Size{}, fmt.Errorf("%w: size %s,%s: %v", ErrSyntax, w, h, err)
		}
		return Size{Kind: SizeWH, W: wi, H: hi, Upscale: upscale}, nil
	default:
		return Size{}, fmt.Errorf("%w: size: must specify w, h, or both", ErrSyntax)
	}
}

func parseDims(w, h string) (int, int, error) {
	wi, err := parsePosInt(w)
	if err != nil {
		return 0, 0, err
	}
	hi, err := parsePosInt(h)
	if err != nil {
		return 0, 0, err
	}
	return wi, hi, nil
}

func parsePosInt(s string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if v <= 0 {
		return 0, errors.New("must be > 0")
	}
	if v > maxParsedDimension {
		return 0, fmt.Errorf("must be <= %d", maxParsedDimension)
	}
	return v, nil
}

func parseRotation(s string) (Rotation, error) {
	if s == "" {
		return Rotation{}, fmt.Errorf("%w: empty rotation", ErrSyntax)
	}
	mirror := false
	if strings.HasPrefix(s, "!") {
		mirror = true
		s = s[1:]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return Rotation{}, fmt.Errorf("%w: rotation %q: %v", ErrSyntax, s, err)
	}
	if v < 0 || v > 360 {
		return Rotation{}, fmt.Errorf("%w: rotation %v: must be in [0, 360]", ErrSyntax, v)
	}
	return Rotation{Degrees: v, Mirror: mirror}, nil
}

func parseQuality(s string) (Quality, error) {
	switch Quality(s) {
	case QualityDefault, QualityColor, QualityGray, QualityBitonal:
		return Quality(s), nil
	}
	return "", fmt.Errorf("%w: unknown quality %q", ErrSyntax, s)
}

func parseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatJPG, FormatTIF, FormatPNG, FormatGIF, FormatJP2, FormatPDF, FormatWEBP:
		return Format(s), nil
	}
	return "", fmt.Errorf("%w: unknown format %q", ErrSyntax, s)
}
