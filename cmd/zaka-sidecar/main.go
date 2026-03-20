// Command zaka-sidecar runs the CASS observation MCP server.
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/mistakeknot/Zaka/internal/mcpsidecar"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	s, err := mcpsidecar.New()
	if err != nil {
		log.Fatalf("sidecar init: %v", err)
	}

	if err := s.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("sidecar run: %v", err)
	}
}
