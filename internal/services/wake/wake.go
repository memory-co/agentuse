// Package wake 是唤醒判定:这条评论是不是在叫 agent,叫醒者有没有资格。
package wake

import (
	"regexp"
	"strings"

	"github.com/memory-co/agentuse/internal/models"
)

// Reason 说明为什么(不)唤醒,写进日志和 notify 的响应。
type Reason string

const (
	Woken       Reason = "woken"
	SelfComment Reason = "self_comment" // agent 自己发的评论,防自触发
	NoMention   Reason = "no_mention"
	RoleDenied  Reason = "role_denied"
)

// Decide 判断 comment 是否唤醒 policy 描述的 agent。
func Decide(p models.Policy, c models.Comment) (bool, Reason) {
	if p.AgentUserID != "" && c.Author.ID == p.AgentUserID {
		return false, SelfComment
	}
	if !Mentions(c.Text, p.AgentName) {
		return false, NoMention
	}
	if !models.Has(p.WakeRoles, c.Author.Role) {
		return false, RoleDenied
	}
	return true, Woken
}

// Mentions 报告 text 里是否有独立的 @name(@claude 算,@claudette 和 a@claude 不算)。
func Mentions(text, name string) bool {
	if name == "" {
		return false
	}
	re := regexp.MustCompile(`(^|[^\w@])@` + regexp.QuoteMeta(strings.ToLower(name)) + `\b`)
	return re.MatchString(strings.ToLower(text))
}
