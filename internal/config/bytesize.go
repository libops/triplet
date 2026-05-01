package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ByteSize is an integer byte count that may be written as either raw bytes or
// a human-readable value such as 50MiB, 1GiB, or 500GB.
type ByteSize int64

func (s *ByteSize) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("byte size must be a scalar")
	}
	n, err := parseByteSize(node.Value)
	if err != nil {
		return err
	}
	*s = ByteSize(n)
	return nil
}

func parseByteSize(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("byte size must not be empty")
	}
	i := 0
	if value[0] == '-' || value[0] == '+' {
		i++
	}
	startDigits := i
	for i < len(value) && ((value[i] >= '0' && value[i] <= '9') || value[i] == '_') {
		i++
	}
	if i == startDigits {
		return 0, fmt.Errorf("byte size %q must start with an integer", raw)
	}
	num := strings.ReplaceAll(value[:i], "_", "")
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("byte size %q: %w", raw, err)
	}
	unit := strings.ToLower(strings.TrimSpace(value[i:]))
	multiplier, ok := byteSizeMultipliers[unit]
	if !ok {
		return 0, fmt.Errorf("byte size %q uses unknown unit %q", raw, strings.TrimSpace(value[i:]))
	}
	if n > 0 && multiplier > 0 && n > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("byte size %q overflows int64", raw)
	}
	if n < 0 && multiplier > 0 && n < math.MinInt64/multiplier {
		return 0, fmt.Errorf("byte size %q overflows int64", raw)
	}
	return n * multiplier, nil
}

var byteSizeMultipliers = map[string]int64{
	"":    1,
	"b":   1,
	"kb":  1000,
	"mb":  1000 * 1000,
	"gb":  1000 * 1000 * 1000,
	"tb":  1000 * 1000 * 1000 * 1000,
	"pib": 1 << 50,
	"tib": 1 << 40,
	"gib": 1 << 30,
	"mib": 1 << 20,
	"kib": 1 << 10,
}
