package main

import (
	"flag"
	"log"

	"xuanwu/internal/panel"
)

func main() {
	resetAdmin := flag.Bool("reset-admin", false,
		"reset the PANEL_ADMIN_USER account to PANEL_ADMIN_PASS (recreating it if missing) and exit")
	clear2FA := flag.Bool("clear-2fa", false,
		"with -reset-admin: also remove that account's two-factor authentication")
	flag.Parse()

	cfg := panel.ConfigFromEnv()
	switch {
	case *resetAdmin:
		if err := panel.ResetAdmin(cfg, *clear2FA); err != nil {
			log.Fatalf("panel: reset-admin: %v", err)
		}
	case *clear2FA:
		log.Fatalf("panel: -clear-2fa requires -reset-admin")
	default:
		if err := panel.Run(cfg); err != nil {
			log.Fatalf("panel: %v", err)
		}
	}
}
