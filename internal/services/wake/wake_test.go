package wake

import (
	"reflect"
	"testing"

	"github.com/memory-co/agentuse/internal/models"
)

func TestDecide(t *testing.T) {
	p := models.Policy{WakeRoles: []models.Role{models.RoleOwner, models.RoleMember}}
	ownerOnly := models.Agent{Handle: "alice-claude", Owner: "alice", Wake: models.WakeOwner}
	members := models.Agent{Handle: "alice-claude", Owner: "alice", Wake: models.WakeMembers}
	cases := []struct {
		name   string
		agent  models.Agent
		author models.Author
		want   Reason
	}{
		{"owner", ownerOnly, models.Author{ID: "alice", Role: models.RoleMember}, Woken},
		{"owner even as guest", members, models.Author{ID: "alice", Role: models.RoleGuest}, Woken},
		{"member, owner only", ownerOnly, models.Author{ID: "bob", Role: models.RoleMember}, NotOwner},
		{"default wake is owner only", models.Agent{Owner: "alice"}, models.Author{ID: "bob", Role: models.RoleOwner}, NotOwner},
		{"member, members", members, models.Author{ID: "bob", Role: models.RoleMember}, Woken},
		{"guest, members", members, models.Author{ID: "eve", Role: models.RoleGuest}, RoleDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, got := Decide(p, tc.agent, models.Comment{Author: tc.author})
			if got != tc.want || ok != (tc.want == Woken) {
				t.Fatalf("got (%v, %s), want %s", ok, got, tc.want)
			}
		})
	}
}

func TestHandles(t *testing.T) {
	for text, want := range map[string][]string{
		"@alice-claude 退款金额算错了":             {"alice-claude"},
		"@Alice-Claude @bob-claude，你们俩各看一下": {"alice-claude", "bob-claude"},
		"@alice-claude- 末尾的横线":              {"alice-claude"},
		"mail a@claude.dev":                 nil,
		"重复 @bob @bob":                      {"bob"},
		"(@carol)":                          {"carol"},
		"没有 @":                              nil,
	} {
		got := Handles(text)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v want %v", text, got, want)
		}
	}
}
