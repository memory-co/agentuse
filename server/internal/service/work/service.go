package work

import (
	"context"
	"database/sql"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
)

// Service owns the work business operations. Peers are explicit contracts.
type Service struct {
	*core.Runtime
	ConversationService conversationService
	DeviceService       deviceService
	RunService          runService
	UserService         userService
}

type conversationService interface {
	CleanupFiles(ctx context.Context, id string)
	DeleteWorkConversations(ctx context.Context, tx *sql.Tx, work string) ([]string, error)
	LookupConversation(ctx context.Context, q core.Querier, u core.User, id string) (core.Conversation, error)
	InsertConversation(ctx context.Context, tx *sql.Tx, u core.User, work, title string) (core.Conversation, error)
	RenameConversation(ctx context.Context, tx *sql.Tx, id, title string) error
	SendMessageTx(ctx context.Context, tx *sql.Tx, u core.User, ss core.Conversation, input string, toAgentIDs, toUserIDs, attachmentIDs []string, replyToMessageID string) (core.Reply, error)
}

type deviceService interface {
	CheckRuntime(ctx context.Context, q core.Querier, deviceID, harness string) error
}

type runService interface {
	DeliveriesIn(ctx context.Context, q core.RowsQuerier, where string, args ...any) ([]core.Delivery, error)
	LookupRun(ctx context.Context, q core.Querier, id string) (core.Run, error)
}

type userService interface {
	GetUser(ctx context.Context, id string) (core.User, error)
}
