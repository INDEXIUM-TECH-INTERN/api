package utils

import (
	"strconv"
	"strings"
)

func ParsePage(raw string) int {
	const (
		defaultPage = 1
		maxPage     = 100
	)

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultPage
	}

	u, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		// abc, số cực lớn
		return maxPage
	}

	if u == 0 {
		return defaultPage
	}

	if u > maxPage {
		return maxPage
	}

	return int(u)
}
