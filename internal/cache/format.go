package cache

import "strconv"

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
