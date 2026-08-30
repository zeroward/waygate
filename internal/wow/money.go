package wow

import (
	"fmt"
	"strings"
)

// Gold formats copper as compact g/s/c, omitting zero middle units.
func Gold(copper uint32) string {
	g := copper / 10000
	s := (copper % 10000) / 100
	c := copper % 100
	var parts []string
	if g > 0 {
		parts = append(parts, fmt.Sprintf("%dg", g))
	}
	if s > 0 {
		parts = append(parts, fmt.Sprintf("%ds", s))
	}
	if c > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%dc", c))
	}
	return strings.Join(parts, " ")
}
