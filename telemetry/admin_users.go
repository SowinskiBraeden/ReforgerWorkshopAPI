package telemetry

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Admin roles, ordered by privilege. Server-side checks compare levels;
// the frontend only hides UI.
const (
	RoleViewer        = "viewer"
	RoleSupport       = "support"
	RoleOperator      = "operator"
	RoleAdministrator = "administrator"
)

var roleLevels = map[string]int{
	RoleViewer:        1,
	RoleSupport:       2,
	RoleOperator:      3,
	RoleAdministrator: 4,
}

// RoleAtLeast reports whether role has at least the privileges of minimum.
func RoleAtLeast(role string, minimum string) bool {
	return roleLevels[strings.ToLower(strings.TrimSpace(role))] >= roleLevels[minimum]
}

// ValidRole reports whether a role name is one of the defined roles.
func ValidRole(role string) bool {
	_, ok := roleLevels[strings.ToLower(strings.TrimSpace(role))]
	return ok
}

type AdminUser struct {
	ID          string     `json:"id"`
	Username    string     `json:"username"`
	Role        string     `json:"role"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	DisabledAt  *time.Time `json:"disabledAt,omitempty"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
	CreatedBy   string     `json:"createdBy,omitempty"`
}

const pbkdf2Iterations = 210_000

// HashAdminPassword derives a PBKDF2-HMAC-SHA256 hash with a random salt.
// Format: pbkdf2$<iterations>$<b64 salt>$<b64 derived>.
func HashAdminPassword(password string) (string, error) {
	if len(password) < 12 {
		return "", errors.New("admin passwords must be at least 12 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	derived := pbkdf2SHA256([]byte(password), salt, pbkdf2Iterations, 32)
	return fmt.Sprintf("pbkdf2$%d$%s$%s", pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derived)), nil
}

// VerifyAdminPassword checks a password against a stored hash in constant time.
func VerifyAdminPassword(password string, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1000 || iterations > 10_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	derived := pbkdf2SHA256([]byte(password), salt, iterations, len(expected))
	return subtle.ConstantTimeCompare(derived, expected) == 1
}

// pbkdf2SHA256 implements RFC 2898 with HMAC-SHA256 (stdlib only; the
// project deliberately avoids new dependencies).
func pbkdf2SHA256(password []byte, salt []byte, iterations int, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	blocks := (keyLen + hashLen - 1) / hashLen
	var out []byte
	var block [4]byte
	for i := 1; i <= blocks; i++ {
		prf.Reset()
		_, _ = prf.Write(salt)
		binary.BigEndian.PutUint32(block[:], uint32(i))
		_, _ = prf.Write(block[:])
		u := prf.Sum(nil)
		t := append([]byte(nil), u...)
		for iter := 1; iter < iterations; iter++ {
			prf.Reset()
			_, _ = prf.Write(u)
			u = prf.Sum(nil)
			for x := range t {
				t[x] ^= u[x]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func (s *Store) CreateAdminUser(ctx context.Context, username string, password string, role string, createdBy string) (AdminUser, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return AdminUser{}, errors.New("username is required")
	}
	if !ValidRole(role) {
		return AdminUser{}, errors.New("invalid role")
	}
	hash, err := HashAdminPassword(password)
	if err != nil {
		return AdminUser{}, err
	}
	now := s.now().UTC()
	user := AdminUser{
		ID:        NewID("adm"),
		Username:  username,
		Role:      strings.ToLower(role),
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: createdBy,
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO admin_users (
		id, username, password_hash, role, created_at, updated_at, created_by
	) VALUES (?,?,?,?,?,?,?)`,
		user.ID, user.Username, hash, user.Role, ms(now), ms(now), createdBy)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return AdminUser{}, errors.New("username already exists")
	}
	return user, err
}

// AuthenticateAdminUser verifies credentials against DB-backed admin users.
func (s *Store) AuthenticateAdminUser(ctx context.Context, username string, password string) (AdminUser, bool) {
	username = strings.ToLower(strings.TrimSpace(username))
	row := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, role,
		created_at, updated_at, disabled_at, last_login_at, COALESCE(created_by, '')
		FROM admin_users WHERE username = ?`, username)
	var user AdminUser
	var hash string
	var createdAt, updatedAt, disabledAt, lastLoginAt int64
	if err := row.Scan(&user.ID, &user.Username, &hash, &user.Role,
		&createdAt, &updatedAt, &disabledAt, &lastLoginAt, &user.CreatedBy); err != nil {
		return AdminUser{}, false
	}
	if disabledAt > 0 || !VerifyAdminPassword(password, hash) {
		return AdminUser{}, false
	}
	user.CreatedAt, user.UpdatedAt = fromMs(createdAt), fromMs(updatedAt)
	now := ms(s.now())
	_, _ = s.db.ExecContext(ctx, `UPDATE admin_users SET last_login_at = ? WHERE id = ?`, now, user.ID)
	return user, true
}

func (s *Store) ListAdminUsers(ctx context.Context) ([]AdminUser, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, role, created_at,
		updated_at, disabled_at, last_login_at, COALESCE(created_by, '')
		FROM admin_users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminUser
	for rows.Next() {
		var user AdminUser
		var createdAt, updatedAt, disabledAt, lastLoginAt int64
		if err := rows.Scan(&user.ID, &user.Username, &user.Role, &createdAt,
			&updatedAt, &disabledAt, &lastLoginAt, &user.CreatedBy); err != nil {
			return nil, err
		}
		user.CreatedAt, user.UpdatedAt = fromMs(createdAt), fromMs(updatedAt)
		if disabledAt > 0 {
			t := fromMs(disabledAt)
			user.DisabledAt = &t
		}
		if lastLoginAt > 0 {
			t := fromMs(lastLoginAt)
			user.LastLoginAt = &t
		}
		out = append(out, user)
	}
	return out, rows.Err()
}

// UpdateAdminUser changes role, disabled state and/or password.
func (s *Store) UpdateAdminUser(ctx context.Context, id string, role string, disabled *bool, password string) error {
	if role != "" {
		if !ValidRole(role) {
			return errors.New("invalid role")
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE admin_users SET role = ?, updated_at = ? WHERE id = ?`,
			strings.ToLower(role), ms(s.now()), id); err != nil {
			return err
		}
	}
	if disabled != nil {
		disabledAt := int64(0)
		if *disabled {
			disabledAt = ms(s.now())
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE admin_users SET disabled_at = ?, updated_at = ? WHERE id = ?`,
			disabledAt, ms(s.now()), id); err != nil {
			return err
		}
	}
	if password != "" {
		hash, err := HashAdminPassword(password)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE admin_users SET password_hash = ?, updated_at = ? WHERE id = ?`,
			hash, ms(s.now()), id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteAdminUser(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_users WHERE id = ?`, id)
	return err
}

// AuditEvent is one admin-action audit record.
type AuditEvent struct {
	ID         int64     `json:"id"`
	At         time.Time `json:"at"`
	Actor      string    `json:"actor"`
	ActorRole  string    `json:"actorRole"`
	Action     string    `json:"action"`
	TargetType string    `json:"targetType"`
	TargetID   string    `json:"targetId"`
	Details    string    `json:"details,omitempty"`
	RequestID  string    `json:"requestId,omitempty"`
}

// RecordAudit writes an audit event synchronously; audit integrity matters
// more than latency on admin mutations.
func (s *Store) RecordAudit(ctx context.Context, event AuditEvent) error {
	if event.At.IsZero() {
		event.At = s.now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO admin_audit_events (
		at, actor, actor_role, action, target_type, target_id, details, request_id
	) VALUES (?,?,?,?,?,?,?,?)`,
		ms(event.At), event.Actor, event.ActorRole, event.Action,
		event.TargetType, event.TargetID, event.Details, event.RequestID)
	return err
}

// AuditDetails marshals audit metadata; failures degrade to empty details
// rather than blocking the action.
func AuditDetails(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return truncate(string(data), 2000)
}

func (s *Store) ListAuditEvents(ctx context.Context, from time.Time, to time.Time, actor string, action string, limit int, offset int) ([]AuditEvent, int, error) {
	where := `WHERE at >= ? AND at < ?`
	args := []any{ms(from), ms(to)}
	if actor != "" {
		where += ` AND actor = ?`
		args = append(args, actor)
	}
	if action != "" {
		where += ` AND action LIKE ?`
		args = append(args, action+"%")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM admin_audit_events `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, actor, actor_role, action,
		target_type, target_id, details, request_id FROM admin_audit_events `+where+`
		ORDER BY at DESC LIMIT ? OFFSET ?`, append(args, clampLimit(limit), maxOffset(offset))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var at int64
		if err := rows.Scan(&event.ID, &at, &event.Actor, &event.ActorRole, &event.Action,
			&event.TargetType, &event.TargetID, &event.Details, &event.RequestID); err != nil {
			return nil, 0, err
		}
		event.At = fromMs(at)
		out = append(out, event)
	}
	return out, total, rows.Err()
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func maxOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}
