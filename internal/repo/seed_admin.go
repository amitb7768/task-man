package repo

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"taskman/internal/model"
)

// SeedAdmin is the Postgres port of server/seed.go SeedAdmin
// (docs/AUTH_FEATURES.md decision #7): if ADMIN_EMAIL is set and no
// login-enabled member exists, provision an ADMIN from ADMIN_EMAIL
// (+ADMIN_PASSWORD, or a generated password logged once). Idempotent. If a
// member with that email already exists (assignable-only), login is enabled
// on that record instead of creating a duplicate person. Same env, same
// log lines, same error behavior as the Mongo version.
func SeedAdmin(ctx context.Context, s *Store) error {
	adminEmail := strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))
	if adminEmail == "" {
		return nil
	}
	db := s.db.WithContext(ctx)

	var cnt int64
	if err := db.Model(&model.Member{}).
		Where("password_hash IS NOT NULL AND password_hash <> ''").Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return nil // a login-enabled user already exists; never reseed
	}

	password := os.Getenv("ADMIN_PASSWORD")
	generated := false
	if password == "" {
		var err error
		password, err = generateTempPassword()
		if err != nil {
			return err
		}
		generated = true
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}

	var existing model.Member
	// Byte-exact match like Mongo's FindOne; creation order stands in for
	// natural order if several assignable-only members share the email.
	err = db.Where("email = ?", adminEmail).Order("created_at, id").Take(&existing).Error
	now := time.Now()
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		m := model.Member{
			ID:                 NewID(),
			Name:               "Admin",
			Email:              model.NullStr(adminEmail),
			PasswordHash:       model.NullStr(hash),
			SystemRole:         model.RoleAdmin,
			MustChangePassword: true,
			CreatedAt:          now,
		}
		if err := db.Create(&m).Error; err != nil {
			return err
		}
		log.Printf("seed: created ADMIN member %s <%s>", m.ID, adminEmail)
	case err != nil:
		return err
	default:
		err := db.Exec(`UPDATE members SET password_hash = ?, system_role = ?,
			must_change_password = TRUE, disabled = FALSE WHERE id = ?`,
			hash, model.RoleAdmin, existing.ID).Error
		if err != nil {
			return err
		}
		log.Printf("seed: enabled login for existing member %s <%s> as ADMIN", existing.ID, adminEmail)
	}

	if generated {
		log.Printf("seed: generated ADMIN_PASSWORD for %s: %s (mustChangePassword is set; change it on first login)", adminEmail, password)
	}
	return nil
}
