package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/store"
)

func (s *Server) createMultiSession(w http.ResponseWriter, r *http.Request, work, title, agent string, input json.RawMessage, prompt *string, targets []string) {
	if agent != "" {
		writeErr(w, 400, "multi_agent 使用 target_agent_ids，不绑定 agent_id")
		return
	}
	if sc := workScope(r); sc != nil {
		if work == "" {
			work = sc.ID
		}
		if work != sc.ID {
			writeErr(w, 403, "只能在本工作创建线程")
			return
		}
	} else if work != "" {
		writeErr(w, 403, "线程只能由协调者创建")
		return
	}
	var content json.RawMessage
	var err error
	if input != nil || prompt != nil {
		p := ""
		if prompt != nil {
			p = *prompt
		}
		content, err = messageContent(input, p)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	} else if len(targets) > 0 {
		writeErr(w, 400, "指定目标时需要 prompt/input")
		return
	}
	ss, reply, err := s.store.CreateMultiSession(r.Context(), currentUser(r), work, title, content, targets, r.Header.Get("Idempotency-Key"), len(input) == 0)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	out := map[string]any{"session": ss}
	if reply != nil {
		out["message"] = reply.Message
		out["runs"] = reply.Runs
		out["deliveries"] = reply.Deliveries
	}
	s.hub.kickAll()
	writeJSON(w, 201, out)
}
func (s *Server) getMultiSession(w http.ResponseWriter, r *http.Request, ss store.Session) {
	ctx := r.Context()
	messages, err := s.store.Messages(ctx, ss.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	runs, err := s.store.Runs(ctx, ss.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	ds, err := s.store.Deliveries(ctx, ss.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	events, err := s.store.RunEvents(ctx, ss.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	questions, err := s.store.RunQuestions(ctx, ss.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	counts := map[string]int{}
	for _, run := range runs {
		counts[run.Status]++
	}
	writeJSON(w, 200, map[string]any{"session": ss, "messages": messages, "runs": runs, "deliveries": ds, "events": events, "steers": []any{}, "questions": questions, "run_counts": counts})
}
func (s *Server) sessionReadAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(requestToken(r), "dk_") {
			s.workAuth(h)(w, r)
			return
		}
		if r.Header.Get(actAsHeader) != "" {
			writeErr(w, 403, "daemon 不能代为用户")
			return
		}
		d, err := s.store.DaemonByKey(r.Context(), requestToken(r))
		if err != nil {
			writeErr(w, 401, "daemon 密钥无效")
			return
		}
		if _, err = s.store.RunAccess(r.Context(), d.ID, r.PathValue("id"), r.Header.Get("X-Agentuse-Run"), r.Header.Get("X-Agentuse-Attempt")); err != nil {
			s.storeErr(w, err)
			return
		}
		ss, err := s.store.GetSession(r.Context(), store.User{Role: store.RoleAdmin}, r.PathValue("id"))
		if err != nil {
			s.storeErr(w, err)
			return
		}
		u, err := s.store.GetUser(r.Context(), ss.UserID)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	}
}
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if !s.threadInScope(w, r, r.PathValue("id")) {
		return
	}
	run, err := s.store.GetRun(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("run_id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	events, err := s.store.RunEvents(r.Context(), run.SessionID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	filtered := []store.SessionEvent{}
	for _, e := range events {
		if e.RunID == run.ID {
			filtered = append(filtered, e)
		}
	}
	writeJSON(w, 200, map[string]any{"run": run, "events": filtered})
}
func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if !s.threadInScope(w, r, r.PathValue("id")) {
		return
	}
	run, err := s.store.CancelRun(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("run_id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	s.hub.pushRun(run.DaemonID, protocol.Message{Type: protocol.TypeCancel, RunID: run.ID, AttemptID: run.AttemptID})
	s.hub.kickAll()
	writeJSON(w, 200, run)
}
func (s *Server) handleAnswerRun(w http.ResponseWriter, r *http.Request) {
	if !s.threadInScope(w, r, r.PathValue("id")) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("qid"), 10, 64)
	if err != nil {
		writeErr(w, 404, "提问不存在")
		return
	}
	var req struct {
		Answers map[string]string `json:"answers"`
	}
	if err = readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	run, q, err := s.store.AnswerRunQuestion(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("run_id"), id, req.Answers)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	s.hub.pushRun(run.DaemonID, protocol.Message{Type: protocol.TypeAnswer, Answer: &protocol.Answer{RunID: run.ID, AttemptID: run.AttemptID, MessageID: run.MessageID, ToolUseID: q.ToolUseID, Answers: q.Answers}})
	writeJSON(w, 202, q)
}
func (s *Server) handleUpgradeSession(w http.ResponseWriter, r *http.Request) {
	ss, err := s.store.UpgradeSession(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, ss)
}
