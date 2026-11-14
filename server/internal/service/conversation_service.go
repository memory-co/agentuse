package service

import (
	"sync"

	fileservice "github.com/memory-co/agentuse/server/internal/service/file"
)

// ConversationService owns messages, shared history, attachment metadata and the
// conversation file lifecycle. It orchestrates input assignment and reply commits.
type ConversationService struct {
	*serviceRuntime
	runs                         *RunService
	works                        *WorkService
	files                        *fileservice.Service
	conversationAttachmentLimits conversationAttachmentLimits
	conversationFilesMu          sync.RWMutex
}
