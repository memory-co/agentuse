package run

import (
	"context"
	"encoding/json"
	"github.com/memory-co/agentuse/client/internal/service/harness"
	"github.com/memory-co/agentuse/internal/protocol"
)

// Translate the five public events only for the unchanged server protocol.
// Client run storage and HTTP always retain the original harnessctl identities.
func (d *endpointConnection) observeServerEvent(ctx context.Context, r *runningMsg, event harness.Event, report func(string, any)) {
	switch event.Type {
	case "accepted":
		if d.cfg.LocalStore != nil && r.local.ID != "" {
			if err := d.cfg.LocalStore.AcceptNative(ctx, r.local, event.RunID); err != nil {
				d.log.Printf("native run index: %v", err)
			}
			binding, _ := json.Marshal(map[string]string{"harness_run_id": event.RunID})
			if err := d.cfg.LocalStore.Event(ctx, d.cfg.EndpointID, r.dispatch.RunID, r.dispatch.AttemptID, "accepted", binding); err != nil {
				d.log.Printf("server delivery binding: %v", err)
			}
		}
	case "message":
		report("progress", map[string]string{"text": event.Text})
	case "log":
		if event.Topic == "tool.started" {
			var title string
			json.Unmarshal(event.Data["title"], &title)
			if title != "" {
				report("progress", map[string]string{"text": "🔧 " + title})
			}
		}
		if event.Topic == "plan" {
			report("plan", map[string]any{"steps": json.RawMessage(event.Data["steps"])})
		}
	case "request":
		question := protocol.QuestionPayload{ToolUseID: event.RequestID}
		if event.State == "pending" {
			d.mu.Lock()
			if r.requests == nil {
				r.requests = map[string]harness.Event{}
			}
			r.requests[event.RequestID] = event
			d.mu.Unlock()
			if event.Kind == "permission" {
				question.Questions = []protocol.Question{{Question: event.Message, Options: []protocol.Option{{Label: "allow"}, {Label: "deny"}}}}
			} else {
				for _, q := range event.Questions {
					options := []protocol.Option{}
					for _, o := range q.Options {
						options = append(options, protocol.Option{Label: o.Label, Description: o.Description})
					}
					question.Questions = append(question.Questions, protocol.Question{Question: q.Text, Header: q.Header, MultiSelect: q.Multiple, Options: options})
				}
			}
			report("question", question)
		} else {
			d.mu.Lock()
			delete(r.requests, event.RequestID)
			d.mu.Unlock()
			kind := "answered"
			if event.State == "expired" {
				kind = "question_expired"
			}
			report(kind, question)
		}
	}
}
func (d *endpointConnection) replyServerAnswer(r *runningMsg, answer protocol.Answer) {
	d.mu.Lock()
	request, found := r.requests[answer.ToolUseID]
	d.mu.Unlock()
	if !found {
		return
	}
	body := ""
	if request.Kind == "permission" {
		body = answer.Answers[request.Message]
		if body == "" {
			for _, value := range answer.Answers {
				body = value
				break
			}
		}
	} else {
		values := map[string]string{}
		for _, q := range request.Questions {
			v := answer.Answers[q.Text]
			if v == "" {
				v = answer.Answers[q.ID]
			}
			values[q.ID] = v
		}
		data, _ := json.Marshal(values)
		body = string(data)
	}
	r.in.Reply(answer.ToolUseID, body, func(err error) {
		if err != nil {
			d.log.Printf("server answer rejected by harnessctl: %v", err)
		}
	})
}
