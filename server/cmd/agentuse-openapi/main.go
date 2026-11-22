// Command agentuse-openapi writes the Server HTTP contract to stdout.
package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/memory-co/agentuse/server/internal/controller"
)

func main() {
	if err := json.NewEncoder(os.Stdout).Encode(controller.OpenAPIDocument()); err != nil {
		log.Fatal(err)
	}
}
