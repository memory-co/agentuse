package version

import (
	"regexp"
	"strconv"
)

// Version is set by -ldflags at build time.
var Version = "dev"

var stable = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

func numbers(value string) ([3]uint64, bool) {
	var result [3]uint64
	match := stable.FindStringSubmatch(value)
	if match == nil {
		return result, false
	}
	for i := range result {
		n, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return result, false
		}
		result[i] = n
	}
	return result, true
}

func Stable(value string) bool {
	_, ok := numbers(value)
	return ok
}

func Newer(target, current string) bool {
	next, ok := numbers(target)
	if !ok {
		return false
	}
	previous, ok := numbers(current)
	if !ok {
		return false
	}
	for i := range next {
		if next[i] != previous[i] {
			return next[i] > previous[i]
		}
	}
	return false
}
