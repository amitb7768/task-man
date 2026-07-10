package main

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// SeedAdmin implements docs/AUTH_FEATURES.md decision #7 (bootstrap):
// on startup, if no login-enabled user exists anywhere in the members
// collection, provision one ADMIN from ADMIN_EMAIL (+ADMIN_PASSWORD, or a
// generated password logged once). Idempotent: a no-op once any
// login-enabled user exists (this seeded admin or anyone else), and a no-op
// entirely if ADMIN_EMAIL isn't set.
//
// If a member with that email already exists (assignable-only, no
// credentials), login is enabled on that record instead of creating a
// duplicate person.
func SeedAdmin(ctx context.Context, s *Store) error {
	adminEmail := strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))
	if adminEmail == "" {
		return nil
	}

	cnt, err := s.members.CountDocuments(ctx, bson.M{"passwordHash": bson.M{"$exists": true, "$ne": ""}})
	if err != nil {
		return err
	}
	if cnt > 0 {
		return nil // a login-enabled user already exists; never reseed
	}

	password := os.Getenv("ADMIN_PASSWORD")
	generated := false
	if password == "" {
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

	var existing Member
	err = s.members.FindOne(ctx, bson.M{"email": adminEmail}).Decode(&existing)
	now := time.Now()
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		m := Member{
			ID:                 bson.NewObjectID(),
			Name:               "Admin",
			Email:              adminEmail,
			PasswordHash:       hash,
			SystemRole:         RoleAdmin,
			MustChangePassword: true,
			CreatedAt:          now,
		}
		if _, err := s.members.InsertOne(ctx, &m); err != nil {
			return err
		}
		log.Printf("seed: created ADMIN member %s <%s>", m.ID.Hex(), adminEmail)
	case err != nil:
		return err
	default:
		_, err := s.members.UpdateOne(ctx, bson.M{"_id": existing.ID}, bson.M{"$set": bson.M{
			"passwordHash":       hash,
			"systemRole":         RoleAdmin,
			"mustChangePassword": true,
			"disabled":           false,
		}})
		if err != nil {
			return err
		}
		log.Printf("seed: enabled login for existing member %s <%s> as ADMIN", existing.ID.Hex(), adminEmail)
	}

	if generated {
		log.Printf("seed: generated ADMIN_PASSWORD for %s: %s (mustChangePassword is set; change it on first login)", adminEmail, password)
	}
	return nil
}
