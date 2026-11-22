package database

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// TargetQuery is a dialect-specific projection of message routing metadata.
// It never creates a delivery record or predicts a run/input identity.
type TargetQuery struct {
	message                                                 string
	From, Key, Version, Device, Harness, Dispatch, Error    string
	DispatchType, ErrorType, Uncertain, Dispatched, Pending string
}

func MessageTargets(db *gorm.DB, message, target string) TargetQuery {
	column := message + ".agent_targets"
	key, value := target+".key", target+".value"
	var entries string
	switch db.Dialector.Name() {
	case "postgres":
		entries = "jsonb_each(" + column + ") AS " + target
	case "mysql":
		entries = "JSON_TABLE(JSON_KEYS(" + column + "), '$[*]' COLUMNS(agent_key VARCHAR(128) PATH '$')) AS " + target
		key = target + ".agent_key"
		value = "JSON_EXTRACT(" + column + ", CONCAT('$.\"', " + key + ", '\"'))"
	default:
		entries = "json_each(" + column + ") AS " + target
	}
	t := TargetQuery{message: message, From: "messages " + message + " CROSS JOIN " + entries, Key: key}
	t.Version, t.Device, t.Harness = jsonText(db, value, "agent_version_id"), jsonText(db, value, "device_id"), jsonText(db, value, "harness")
	t.Dispatch, t.Error = jsonText(db, value, "dispatch"), jsonText(db, value, "error")
	t.DispatchType, t.ErrorType = jsonType(db, value, "dispatch"), jsonType(db, value, "error")
	t.Uncertain = "CASE WHEN " + jsonText(db, value, "error.uncertain") + " IN ('true','1') THEN 1 ELSE 0 END"
	t.Dispatched = "COALESCE(" + t.DispatchType + "='object',false)"
	accepted := "EXISTS(SELECT 1 FROM run_inputs i JOIN runs r ON r.id=i.run_id WHERE i.message_id=" + message + ".id AND r.agent_id=" + key + ")"
	t.Pending = "NOT " + accepted + " AND (" + t.ErrorType + " IS NULL OR " + t.Uncertain + "=1)"
	return t
}

// Column/path fragments are internal constants, never user supplied SQL.
func jsonText(db *gorm.DB, column, path string) string {
	switch db.Dialector.Name() {
	case "postgres":
		expression := column
		parts := strings.Split(path, ".")
		for i, part := range parts {
			operator := "->"
			if i == len(parts)-1 {
				operator = "->>"
			}
			expression = "(" + expression + operator + "'" + part + "')"
		}
		return expression
	case "mysql":
		return "JSON_UNQUOTE(JSON_EXTRACT(" + column + ", '$." + path + "'))"
	default:
		return "CAST(json_extract(" + column + ", '$." + path + "') AS TEXT)"
	}
}

func jsonType(db *gorm.DB, column, path string) string {
	switch db.Dialector.Name() {
	case "postgres":
		return "jsonb_typeof(" + column + "->'" + path + "')"
	case "mysql":
		return "LOWER(JSON_TYPE(JSON_EXTRACT(" + column + ", '$." + path + "')))"
	default:
		return "json_type(" + column + ", '$." + path + "')"
	}
}

func AttachmentReferences(db *gorm.DB) string {
	switch db.Dialector.Name() {
	case "postgres":
		return "SELECT COUNT(*) FROM messages m CROSS JOIN jsonb_array_elements(m.attachments) f(value) WHERE m.conversation_id=? AND f.value->>'id'=?"
	case "mysql":
		return "SELECT COUNT(*) FROM messages m CROSS JOIN JSON_TABLE(m.attachments, '$[*]' COLUMNS(attachment_id VARCHAR(128) PATH '$.id')) f WHERE m.conversation_id=? AND f.attachment_id=?"
	default:
		return "SELECT COUNT(*) FROM messages m CROSS JOIN json_each(m.attachments) f WHERE m.conversation_id=? AND json_extract(f.value,'$.id')=?"
	}
}

// RunResultSummary projects the measurements used by the conversation timeline.
func RunResultSummary(db *gorm.DB) string {
	switch db.Dialector.Name() {
	case "postgres":
		return `jsonb_build_object('model', result::jsonb->'model', 'usage', result::jsonb->'usage')`
	default:
		return `JSON_OBJECT('model', JSON_EXTRACT(result, '$.model'), 'usage', JSON_EXTRACT(result, '$.usage'))`
	}
}

func (t TargetQuery) Columns() string {
	return fmt.Sprintf("SELECT %s.id,%s.conversation_id,%s,%s,%s,%s,%s.content,COALESCE(%s,'null'),COALESCE(%s,'null') FROM %s", t.message, t.message, t.Key, t.Version, t.Device, t.Harness, t.message, t.Dispatch, t.Error, t.From)
}
