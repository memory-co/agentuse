package controller

import (
	"context"
	"net/http"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"

	"github.com/memory-co/agentuse/server/internal/service"
)

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request, work, title string, prompt string, toAgentIDs, toUserIDs []string) {
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
	ss, reply, err := s.service.CreateConversation(r.Context(), currentUser(r), work, title, prompt, toAgentIDs, toUserIDs)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	out := map[string]any{"conversation": ss}
	if reply != nil {
		out["message"] = reply.Message
		out["runs"] = reply.Runs
		out["deliveries"] = reply.Deliveries
	}
	writeJSON(w, 201, out)
}
func (s *Server) getConversation(w http.ResponseWriter, r *http.Request, ss service.Conversation) {
	detail, err := s.service.ReadConversation(r.Context(), currentUser(r), ss.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, detail)
}
func (s *Server) conversationReadAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(requestToken(r), "dk_") {
			s.workAuth(h)(w, r)
			return
		}
		if r.Header.Get(actAsHeader) != "" {
			writeErr(w, 403, "device 不能代为用户")
			return
		}
		d, err := s.service.DeviceByKey(r.Context(), requestToken(r))
		if err != nil {
			writeErr(w, 401, "device 密钥无效")
			return
		}
		if err = s.service.DeviceConversationAccess(r.Context(), d.ID, r.PathValue("id")); err != nil {
			s.storeErr(w, err)
			return
		}
		ss, err := s.service.GetConversation(r.Context(), service.User{Role: service.RoleAdmin}, r.PathValue("id"))
		if err != nil {
			s.storeErr(w, err)
			return
		}
		u, err := s.service.GetUser(r.Context(), ss.UserID)
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
	run, err := s.service.GetRun(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("run_id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	events, err := s.service.RunEvents(r.Context(), run.ConversationID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	filtered := []service.ConversationEvent{}
	for _, e := range events {
		if e.RunID == run.ID {
			filtered = append(filtered, e)
		}
	}
	writeJSON(w, 200, agentuse.RunDetail{Run: run, Events: filtered})
}
func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if !s.threadInScope(w, r, r.PathValue("id")) {
		return
	}
	run, err := s.service.CancelRun(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("run_id"))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 200, run)
}
func (s *Server) handleAnswerRun(w http.ResponseWriter, r *http.Request) {
	if !s.threadInScope(w, r, r.PathValue("id")) {
		return
	}
	var body answerRunBody
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.service.ReplyRequest(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("run_id"), r.PathValue("request_id"), body.Answer); err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, 202, answerRunResponse{State: "pending"})
}
