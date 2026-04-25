package cache

import "github.com/libops/triplet/internal/iiif/image/v3/parse"

// DerivativeKey returns the canonical cache key for a parsed Image API request.
// The key is stable across rotations of the cache and across processes.
func DerivativeKey(req parse.Request) string {
	return "iiif/3/" + req.Identifier + "/" +
		regionString(req.Region) + "/" +
		sizeString(req.Size) + "/" +
		rotationString(req.Rotation) + "/" +
		string(req.Quality) + "." + string(req.Format)
}

func regionString(r parse.Region) string {
	switch r.Kind {
	case parse.RegionFull:
		return "full"
	case parse.RegionSquare:
		return "square"
	case parse.RegionPercent:
		return "pct:" + ftoa(r.X) + "," + ftoa(r.Y) + "," + ftoa(r.W) + "," + ftoa(r.H)
	}
	return ftoa(r.X) + "," + ftoa(r.Y) + "," + ftoa(r.W) + "," + ftoa(r.H)
}

func sizeString(s parse.Size) string {
	prefix := ""
	if s.Upscale {
		prefix = "^"
	}
	switch s.Kind {
	case parse.SizeMax, parse.SizeMaxUp:
		return prefix + "max"
	case parse.SizeWidth:
		return prefix + itoa(s.W) + ","
	case parse.SizeHeight:
		return prefix + "," + itoa(s.H)
	case parse.SizeWH:
		return prefix + itoa(s.W) + "," + itoa(s.H)
	case parse.SizeBestFit:
		return prefix + "!" + itoa(s.W) + "," + itoa(s.H)
	case parse.SizePercent:
		return prefix + "pct:" + ftoa(s.Percent)
	}
	return ""
}

func rotationString(r parse.Rotation) string {
	out := ""
	if r.Mirror {
		out = "!"
	}
	return out + ftoa(r.Degrees)
}

// minimal int/float formatters (avoid pulling strconv allocs into a hot path).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func ftoa(f float64) string {
	if f == float64(int64(f)) {
		return itoa(int(f))
	}
	// Use a simple printf-style formatter via the standard library is fine
	// here; this path is not on the per-pixel hot path.
	return formatFloat(f)
}
