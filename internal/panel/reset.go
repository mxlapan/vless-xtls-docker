package panel

import (
	"database/sql"
	"errors"
	"log"
)

// ResetAdmin re-seeds the PANEL_ADMIN_USER account from PANEL_ADMIN_PASS and
// returns: the recovery path for a lost admin password, since a normal boot
// treats the database as authoritative and never overwrites a stored password
// (see Store.EnsureAdmin). The account is recreated if it was deleted, and its
// outstanding sessions are revoked. clear2FA additionally drops the account's
// TOTP enrolment, for an operator who lost the authenticator too.
func ResetAdmin(cfg Config, clear2FA bool) error {
	if err := validateSecrets(cfg); err != nil {
		return err
	}
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	store.crypt = newCrypter(cfg.JWTSecret)

	hash, err := hashPassword(cfg.AdminPass)
	if err != nil {
		return err
	}
	adm, err := store.GetAdmin(cfg.AdminUser)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := store.CreateAdmin(cfg.AdminUser, hash); err != nil {
			return err
		}
		log.Printf("admin %q recreated from PANEL_ADMIN_PASS", cfg.AdminUser)
	case err != nil:
		return err
	default:
		if err := store.SetAdminPassword(cfg.AdminUser, hash); err != nil {
			return err
		}
		log.Printf("admin %q password reset from PANEL_ADMIN_PASS; existing sessions revoked", cfg.AdminUser)
	}

	switch {
	case clear2FA:
		if err := store.SetAdminTOTP(cfg.AdminUser, "", false); err != nil {
			return err
		}
		log.Printf("two-factor authentication removed for %q", cfg.AdminUser)
	case adm != nil && adm.TOTPEnabled:
		log.Printf("note: two-factor authentication is still enabled for %q; re-run with -clear-2fa if you lost the authenticator too", cfg.AdminUser)
	}
	_ = store.AddAudit("cli", "admin.reset", cfg.AdminUser)
	return nil
}
