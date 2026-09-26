package config

import (
	"encoding/hex"
	"fmt"
	"image/color"
	"strings"

	"golang.org/x/image/colornames"
	"gopkg.in/yaml.v3"
)

// Color is an opaque RGB color written as #rgb, #rrggbb, or a CSS color name.
type Color color.RGBA

func (c *Color) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("color must be a scalar")
	}
	rgba, err := parseColor(node.Value)
	if err != nil {
		return err
	}
	*c = Color(rgba)
	return nil
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
		return color.RGBA{}, fmt.Errorf("color %q must be #rgb, #rrggbb, or a CSS color name", raw)
	}
	return color.RGBA{R: b[0], G: b[1], B: b[2], A: 255}, nil
}
