package panel

import (
	"path/filepath"
	"testing"
)

func resetConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		DBPath:    filepath.Join(t.TempDir(), "t.db"),
		AdminUser: "admin",
		AdminPass: "S3cret!pass",
		JWTSecret: "0123456789abcdef0123456789abcdef",
	}
}

// withStore opens the reset target's database the way the panel would.
func withStore(t *testing.T, cfg Config, fn func(*Store)) {
	t.Helper()
	s, err := OpenStore(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	s.crypt = newCrypter(cfg.JWTSecret)
	fn(s)
}

func TestResetAdminRestoresAccess(t *testing.T) {
	cfg := resetConfig(t)

	// Fresh database: the account is created outright.
	if err := ResetAdmin(cfg, false); err != nil {
		t.Fatalf("reset on fresh db: %v", err)
	}
	var epoch int64
	withStore(t, cfg, func(s *Store) {
		a, err := s.GetAdmin(cfg.AdminUser)
		if err != nil {
			t.Fatalf("get admin: %v", err)
		}
		if !checkPassword(a.PasswordHash, cfg.AdminPass) {
			t.Fatal("admin password was not seeded from PANEL_ADMIN_PASS")
		}
		epoch = a.SessionEpoch
	})

	// The forgotten-password case: the stored password differs from the env
	// seed, and 2FA is enrolled.
	withStore(t, cfg, func(s *Store) {
		hash, err := hashPassword("Forgott3n!pw")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetAdminPassword(cfg.AdminUser, hash); err != nil {
			t.Fatal(err)
		}
		if err := s.SetAdminTOTP(cfg.AdminUser, "JBSWY3DPEHPK3PXP", true); err != nil {
			t.Fatal(err)
		}
	})

	if err := ResetAdmin(cfg, false); err != nil {
		t.Fatalf("reset: %v", err)
	}
	withStore(t, cfg, func(s *Store) {
		a, _ := s.GetAdmin(cfg.AdminUser)
		if !checkPassword(a.PasswordHash, cfg.AdminPass) {
			t.Fatal("password was not reset to PANEL_ADMIN_PASS")
		}
		if a.SessionEpoch <= epoch {
			t.Fatalf("session epoch not bumped: %d <= %d", a.SessionEpoch, epoch)
		}
		// 2FA survives a plain reset: losing a password is not a reason to
		// silently drop the second factor.
		if !a.TOTPEnabled {
			t.Fatal("2fa was cleared without -clear-2fa")
		}
	})

	if err := ResetAdmin(cfg, true); err != nil {
		t.Fatalf("reset with clear2FA: %v", err)
	}
	withStore(t, cfg, func(s *Store) {
		a, _ := s.GetAdmin(cfg.AdminUser)
		if a.TOTPEnabled || a.TOTPSecret != "" {
			t.Fatalf("2fa not cleared: %+v", a)
		}
	})
}

func TestResetAdminRecreatesDeletedAccount(t *testing.T) {
	cfg := resetConfig(t)
	withStore(t, cfg, func(s *Store) {
		if err := s.CreateAdmin("other", "hash"); err != nil {
			t.Fatal(err)
		}
	})

	if err := ResetAdmin(cfg, false); err != nil {
		t.Fatalf("reset: %v", err)
	}
	withStore(t, cfg, func(s *Store) {
		a, err := s.GetAdmin(cfg.AdminUser)
		if err != nil {
			t.Fatalf("admin not recreated: %v", err)
		}
		if !checkPassword(a.PasswordHash, cfg.AdminPass) {
			t.Fatal("recreated admin has the wrong password")
		}
		if n, _ := s.CountAdmins(); n != 2 {
			t.Fatalf("other admins should be untouched, got %d", n)
		}
	})
}

func TestResetAdminRejectsWeakPassword(t *testing.T) {
	cfg := resetConfig(t)
	cfg.AdminPass = "weak"
	if err := ResetAdmin(cfg, false); err == nil {
		t.Fatal("reset accepted a weak PANEL_ADMIN_PASS")
	}
}
