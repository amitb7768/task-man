package repo

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"taskman/internal/model"
)

// Ported verbatim from server/auth.go (the pure helpers the Store methods
// depend on). The cookie helpers and loginLimiter stay in the HTTP layer.

const (
	// SessionTTL is the sliding idle TTL (docs/AUTH_FEATURES.md decision
	// #9): every successful LoadSessionUser pushes expires_at to now+TTL.
	SessionTTL = 12 * time.Hour
	bcryptCost = 10
)

func generateSessionToken() (string, error) {
	b := make([]byte, 32) // 256 bits
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func checkPassword(hash, pw string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// dummyPasswordHash: Authenticate burns one bcrypt compare against it on an
// unknown email so that path costs the same wall-clock time as a real
// wrong-password rejection (no email-enumeration timing oracle).
var dummyPasswordHash = mustHashForTiming()

func mustHashForTiming() string {
	h, err := hashPassword("timing-oracle-mitigation-fixed-dummy")
	if err != nil {
		panic(err)
	}
	return h
}

// tempPasswordChars excludes visually-confusable characters (0/O, 1/l/I).
const tempPasswordChars = "ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"

// generateTempPassword returns a random 12-character password from
// tempPasswordChars (enable-login / reset-password / env-seed bootstrap).
func generateTempPassword() (string, error) {
	const n = 12
	out := make([]byte, n)
	max := big.NewInt(int64(len(tempPasswordChars)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = tempPasswordChars[idx.Int64()]
	}
	return string(out), nil
}

func validSystemRole(r string) bool {
	return r == model.RoleAdmin || r == model.RoleUser
}

// memberToCtxUser mirrors server/auth.go's memberToCtxUser; m.TeamIDs must
// already be loaded from member_teams.
func memberToCtxUser(m *model.Member) *model.CtxUser {
	return &model.CtxUser{
		ID:                 m.ID,
		Name:               m.Name,
		Email:              string(m.Email),
		SystemRole:         m.SystemRole,
		TeamIDs:            append([]string(nil), m.TeamIDs...),
		MustChangePassword: m.MustChangePassword,
		Disabled:           m.Disabled,
	}
}

// SQLSTATEs the member/team/auth repo maps back to Mongo-era messages.
const (
	sqlstateFKViolation     = "23503"
	sqlstateUniqueViolation = "23505"
	sqlstateCheckViolation  = "23514"
)

// violatedConstraint returns the constraint name if err is a Postgres error
// with the given SQLSTATE, else "". Replaces mongo.IsDuplicateKeyError:
// the constraint name picks which Mongo-era message applies.
func violatedConstraint(err error, sqlstate string) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == sqlstate {
		return pe.ConstraintName
	}
	return ""
}
