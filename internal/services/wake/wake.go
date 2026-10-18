// Package wake 是唤醒判定:这条评论 @ 了哪些 agent,叫醒者有没有资格叫醒每一个。
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
	SelfComment Reason = "self_comment" // 某个 agent 自己发的评论,防自触发 / 互相触发
	NoMention   Reason = "no_mention"   // 没有 @ 到这个平台上的任何 agent
	NotOwner    Reason = "not_owner"    // agent 只让主人叫醒(wake=owner),叫醒者不是主人
	RoleDenied  Reason = "role_denied"  // wake=members,但叫醒者的角色不在平台的 wake_roles 里
)

// Decide 判断 comment 能否唤醒被 @ 到的 agent a。调用方已经确认评论 @ 了它、作者不是任何 agent。
func Decide(p models.Policy, a models.Agent, c models.Comment) (bool, Reason) {
	if c.Author.ID == a.Owner {
		return true, Woken
	}
	if a.Wake != models.WakeMembers {
		return false, NotOwner
	}
	if !models.Has(p.WakeRoles, c.Author.Role) {
		return false, RoleDenied
	}
	return true, Woken
}

var mentionRe = regexp.MustCompile(`(^|[^\w@])@([a-z0-9][a-z0-9_-]*)`)

// Handles 交回 text 里独立 @ 到的名字,小写、去重、按出现顺序。
// @alice-claude 算;a@claude.dev(邮箱)不算;末尾的 - 和 _ 不算进名字。
func Handles(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(strings.ToLower(text), -1) {
		h := strings.TrimRight(m[2], "-_")
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}
