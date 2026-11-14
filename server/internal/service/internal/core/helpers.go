package core

import (
	"fmt"
	"strings"
)

func NullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func RecipientIDs(ids []string) ([]string, error) {
	out := []string{}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || strings.TrimSpace(id) != id {
			return nil, fmt.Errorf("收件人 ID 必须是非空且无首尾空格的字符串")
		}
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out, nil
}
