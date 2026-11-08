// Package agenttools contains Server-owned tools delivered to coordinating agents.
// They call Server APIs and are independent of the Client executor.
package agenttools

import _ "embed"

//go:embed work.py
var Work []byte
