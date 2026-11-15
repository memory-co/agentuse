package test

import (
	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/identity"
)

var randomID = identity.NewID
var newUUID = uuid.NewString

const conversationFileNamespace = "conversations"
