package controller

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/memory-co/agentuse/server/internal/service"
)

func TestMessageRecipientsAreExplicitAndOnlyAgentsExecute(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	admin, adminToken := f.e.user("moderator", service.RoleAdmin)
	_, outsiderToken := f.e.user("outsider", service.RoleUser)
	path := "/api/sessions/" + f.session.ID

	cases := []struct {
		name          string
		agents, users []string
		deliveries    int
	}{
		{"note", nil, nil, 0},
		{"users", nil, []string{f.u.ID, admin.ID}, 0},
		{"agents_and_users", []string{f.agent}, []string{admin.ID}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Repeated recipients must be normalized before storing or dispatching.
			body := map[string]any{"prompt": "@" + f.agent, "to_agent_ids": append(append([]string{}, tc.agents...), tc.agents...), "to_user_ids": append(append([]string{}, tc.users...), tc.users...)}
			var reply service.MultiReply
			if code := f.e.req(f.token, http.MethodPost, path+"/messages", body, &reply).StatusCode; code != 201 {
				t.Fatal(code)
			}
			m := reply.Message
			if m == nil || m.ToAgentIDs == nil || m.ToUserIDs == nil || !reflect.DeepEqual(m.ToAgentIDs, append([]string{}, tc.agents...)) || !reflect.DeepEqual(m.ToUserIDs, append([]string{}, tc.users...)) || m.AuthorUserID != f.u.ID {
				t.Fatalf("wrong recipients in submission response: %+v", m)
			}
			if len(reply.Deliveries) != tc.deliveries || len(reply.Runs) != 0 {
				t.Fatalf("only agent recipients may create delivery relations: %+v", reply)
			}
			var detail service.SessionDetail
			if code := f.e.req(adminToken, http.MethodGet, path, nil, &detail).StatusCode; code != 200 {
				t.Fatal(code)
			}
			if !reflect.DeepEqual(*m, detail.Messages[len(detail.Messages)-1]) {
				t.Fatalf("recipients differ after reading history: %+v", detail.Messages)
			}
			if tc.deliveries == 0 && (detail.Session.Status != "idle" || len(detail.Deliveries) != 0 || len(detail.Runs) != 0) {
				t.Fatalf("note or human recipients started execution: %+v", detail)
			}
		})
	}
	if code := f.e.code(outsiderToken, http.MethodGet, path, nil); code != 404 {
		t.Fatal("recipient metadata must not change visibility", code)
	}
}

func TestInvalidRecipientsRejectTheWholeMessage(t *testing.T) {
	f := attachmentEnv(t, 1024, 4096)
	outsider, outsiderToken := f.e.user("outsider", service.RoleUser)
	ctx := context.Background()
	path := "/api/sessions/" + f.session.ID
	cases := []struct {
		name string
		body any
		code int
	}{
		{"unknown_user", map[string]any{"prompt": "test", "to_agent_ids": []string{f.agent}, "to_user_ids": []string{"missing"}}, 404},
		{"invisible_user", map[string]any{"prompt": "test", "to_agent_ids": []string{f.agent}, "to_user_ids": []string{f.u.ID, outsider.ID}}, 404},
		{"unknown_agent", map[string]any{"prompt": "test", "to_agent_ids": []string{f.agent, "missing"}, "to_user_ids": []string{f.u.ID}}, 404},
		{"empty_agent", map[string]any{"prompt": "test", "to_agent_ids": []string{f.agent, ""}}, 400},
		{"empty_user", map[string]any{"prompt": "test", "to_user_ids": []string{f.u.ID, ""}}, 400},
		{"whitespace_user", map[string]any{"prompt": "test", "to_user_ids": []string{" "}}, 400},
		{"wrong_type", `{"prompt":"test","to_user_ids":[1]}`, 400},
		{"null_element", `{"prompt":"test","to_user_ids":[null]}`, 400},
		{"old_field", `{"prompt":"test","target_agent_ids":["coder"]}`, 400},
		{"multiple_values", `{"prompt":"test"}{"prompt":"extra"}`, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := f.e.code(f.token, http.MethodPost, path+"/messages", tc.body); code != tc.code {
				t.Fatalf("status %d, want %d", code, tc.code)
			}
			detail, err := f.e.st.ReadSession(ctx, f.u, f.session.ID)
			if err != nil || len(detail.Messages) != 0 || len(detail.Deliveries) != 0 || detail.Session.Status != "idle" {
				t.Fatalf("invalid recipients partially committed: %+v, %v", detail, err)
			}
		})
	}
	if code := f.e.code(outsiderToken, http.MethodGet, path, nil); code != 404 {
		t.Fatal("rejected user recipient gained access", code)
	}
	if code := f.e.code(f.token, http.MethodPost, "/api/sessions", map[string]any{"prompt": "test", "to_user_ids": []string{outsider.ID}}); code != 404 {
		t.Fatal("invalid initial recipients accepted", code)
	}
	for _, body := range []any{
		map[string]any{"to_user_ids": []string{f.u.ID}},
		map[string]any{"defer_start": true, "to_user_ids": []string{f.u.ID}},
		`{"prompt":"test","target_agent_ids":[]}`,
	} {
		if code := f.e.code(f.token, http.MethodPost, "/api/sessions", body); code != 400 {
			t.Fatal("invalid create accepted", body, code)
		}
	}
	sessions, err := f.e.st.ListSessions(ctx, f.u, 100)
	if err != nil || len(sessions) != 1 {
		t.Fatal("failed create leaked a conversation", sessions, err)
	}
}

func TestCreateSessionWithHumanRecipients(t *testing.T) {
	e := newEnv(t, Config{})
	u, token := e.user("alice", service.RoleUser)
	var reply struct {
		Session service.Session `json:"session"`
		service.MultiReply
	}
	body := map[string]any{"prompt": "FYI", "to_user_ids": []string{u.ID, u.ID}}
	if code := e.req(token, http.MethodPost, "/api/sessions", body, &reply).StatusCode; code != 201 {
		t.Fatal(code)
	}
	if reply.Session.Status != "idle" || reply.Message == nil || !reflect.DeepEqual(reply.Message.ToUserIDs, []string{u.ID}) || reply.Message.ToAgentIDs == nil || len(reply.Message.ToAgentIDs) != 0 || len(reply.Deliveries) != 0 {
		t.Fatalf("bad human-only conversation: %+v", reply)
	}
}
