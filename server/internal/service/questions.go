package service

import (
	"errors"
	"sort"
	"strings"
)

// ErrQuestionClosed 表示问题已经答过或已经作废，不能再答。
var ErrQuestionClosed = errors.New("这个问题已经回答过或已经过期")

func answerText(answers map[string]string) string {
	keys := make([]string, 0, len(answers))
	for k := range answers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("我对你之前提问的回答：")
	for _, k := range keys {
		b.WriteString("\n" + k + " → " + answers[k])
	}
	return b.String()
}
