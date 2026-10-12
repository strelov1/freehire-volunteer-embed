// Package pgvec formats Go float64 slices as pgvector's text input format.
package pgvec

import (
	"strconv"
	"strings"
)

// Format renders v as pgvector's text input format, e.g. "[0.1,0.2,0.3]".
func Format(v []float64) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
	}
	b.WriteByte(']')
	return b.String()
}
