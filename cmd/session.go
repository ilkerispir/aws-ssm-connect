package cmd

import (
	"log"

	"github.com/ilkerispir/aws-ssm-connect/internal/tunnel"
)

func ListSessions() {
	if err := tunnel.ListPIDs(); err != nil {
		log.Fatalf("list sessions failed: %v", err)
	}
}

func KillSession(pid int) {
	if err := tunnel.KillPID(pid); err != nil {
		log.Fatalf("kill session failed: %v", err)
	}
}

func KillAllSessions() {
	if err := tunnel.KillAllPIDs(); err != nil {
		log.Fatalf("kill all sessions failed: %v", err)
	}
}
