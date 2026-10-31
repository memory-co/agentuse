package store

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// Question 是 harness 向用户提的一次问题(Claude Code 的 AskUserQuestion),见 docs/v2/designs/question.md。
type Question struct {
	RunID      string            `json:"run_id,omitempty"`
	AttemptID  string            `json:"attempt_id,omitempty"`
	ID         int64             `json:"id"`
	SessionID  string            `json:"session_id"`
	MessageID  int64             `json:"message_id"`
	ToolUseID  string            `json:"tool_use_id"`
	Questions  json.RawMessage   `json:"questions"`
	Answers    map[string]string `json:"answers,omitempty"`
	AnsweredBy string            `json:"answered_by,omitempty"`
	Status     string            `json:"status"` // pending / answered / expired
	CreatedAt  int64             `json:"created_at"`
}

const (
	QuestionPending  = "pending"
	QuestionAnswered = "answered"
	QuestionExpired  = "expired"
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
