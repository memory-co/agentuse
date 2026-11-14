package integration

import (
	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	"github.com/memory-co/agentuse/server/internal/store"
)

type discardNotifier = core.DiscardNotifier

var randomID = store.RandomID
var newUUID = store.NewUUID

const conversationFileNamespace = "conversations"
