package harness

import (
	"github.com/memory-co/agentuse/internal/protocol"
	"strings"
	"testing"
)

func TestCredentialsDoNotSelectBusinessInstructions(t *testing.T) {
	for _, local := range []bool{false, true} {
		m := protocol.Dispatch{Local: local, RunID: "run", AgentID: "agent", Instructions: "server-owned instructions", AttachmentSupport: !local}
		without := instructionsFor(m)
		m.WorkToken = "opaque-work-credential"
		with := instructionsFor(m)
		if with != without {
			t.Fatal("client inferred business policy from a credential")
		}
		if !strings.Contains(with, m.Instructions) || strings.Contains(with, m.WorkToken) {
			t.Fatal("instructions lost or credential leaked")
		}
	}
}
