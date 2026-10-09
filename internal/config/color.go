package config

import (
	"encoding/hex"
	"fmt"
	"image/color"
	"strings"

	"golang.org/x/image/colornames"
)

// Background returns the parsed background_color, or nil when it is unset.
// Load rejects invalid values, so a loaded config never yields nil for a set one.
func (i Image) Background() *color.RGBA {
	if i.BackgroundColor == "" {
		return nil
	}
	c, err := parseColor(i.BackgroundColor)
	if err != nil {
		return nil
	}
	return &c
}

func parseColor(raw string) (color.RGBA, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if named, ok := colornames.Map[value]; ok {
		return named, nil
	}
	digits, ok := strings.CutPrefix(value, "#")
	if ok && len(digits) == 3 {
		digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
	}
	b, err := hex.DecodeString(digits)
	if !ok || err != nil || len(b) != 3 {
		return color.RGBA{}, fmt.Errorf("%q is not #rgb, #rrggbb, or a CSS color name", raw)
	}
	return color.RGBA{R: b[0], G: b[1], B: b[2], A: 255}, nil
}
