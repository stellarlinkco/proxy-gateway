package user

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL,
			api_key TEXT UNIQUE NOT NULL,
			role TEXT DEFAULT 'user',
			status TEXT DEFAULT 'active',
			daily_limit_cents INTEGER DEFAULT 0,
			monthly_limit_cents INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS user_channels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			channel_type TEXT NOT NULL,
			channel_index INTEGER NOT NULL,
			UNIQUE(user_id, channel_type, channel_index),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS user_usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			date TEXT NOT NULL,
			request_count INTEGER DEFAULT 0,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			cost_cents INTEGER DEFAULT 0,
			UNIQUE(user_id, date),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
	}
	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("exec %q: %w", q[:40], err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// generateAPIKey creates a "cpk_" prefixed random key.
func generateAPIKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "cpk_" + hex.EncodeToString(b), nil
}

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var createdAt, updatedAt int64
	err := row.Scan(
		&u.ID, &u.Email, &u.Name, &u.APIKey,
		&u.Role, &u.Status,
		&u.DailyLimitCents, &u.MonthlyLimitCents,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	u.CreatedAt = time.Unix(createdAt, 0)
	u.UpdatedAt = time.Unix(updatedAt, 0)
	return &u, nil
}

// CreateUser creates a new user with an auto-generated API key.
func (s *Store) CreateUser(req CreateUserRequest) (*User, error) {
	apiKey, err := generateAPIKey()
	if err != nil {
		return nil, fmt.Errorf("generate api key: %w", err)
	}

	role := req.Role
	if role == "" {
		role = "user"
	}

	id := uuid.New().String()
	now := time.Now().Unix()

	_, err = s.db.Exec(
		`INSERT INTO users (id, email, name, api_key, role, status, daily_limit_cents, monthly_limit_cents, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
		id, req.Email, req.Name, apiKey, role,
		req.DailyLimitCents, req.MonthlyLimitCents,
		now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	return s.GetUserByID(id)
}

// CreateUserWithKey creates a user with a specified API key (for migration).
func (s *Store) CreateUserWithKey(req CreateUserRequest, apiKey string) (*User, error) {
	role := req.Role
	if role == "" {
		role = "user"
	}

	id := uuid.New().String()
	now := time.Now().Unix()

	_, err := s.db.Exec(
		`INSERT INTO users (id, email, name, api_key, role, status, daily_limit_cents, monthly_limit_cents, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
		id, req.Email, req.Name, apiKey, role,
		req.DailyLimitCents, req.MonthlyLimitCents,
		now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	return s.GetUserByID(id)
}

func (s *Store) GetUserByID(id string) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, name, api_key, role, status, daily_limit_cents, monthly_limit_cents, created_at, updated_at
		 FROM users WHERE id = ?`, id,
	)
	return scanUser(row)
}

func (s *Store) GetUserByAPIKey(apiKey string) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, name, api_key, role, status, daily_limit_cents, monthly_limit_cents, created_at, updated_at
		 FROM users WHERE api_key = ?`, apiKey,
	)
	return scanUser(row)
}

func (s *Store) GetUserByEmail(email string) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, email, name, api_key, role, status, daily_limit_cents, monthly_limit_cents, created_at, updated_at
		 FROM users WHERE email = ?`, email,
	)
	return scanUser(row)
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(
		`SELECT id, email, name, api_key, role, status, daily_limit_cents, monthly_limit_cents, created_at, updated_at
		 FROM users ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

func (s *Store) UpdateUser(id string, req UpdateUserRequest) (*User, error) {
	existing, err := s.GetUserByID(id)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Role != nil {
		existing.Role = *req.Role
	}
	if req.Status != nil {
		existing.Status = *req.Status
	}
	if req.DailyLimitCents != nil {
		existing.DailyLimitCents = *req.DailyLimitCents
	}
	if req.MonthlyLimitCents != nil {
		existing.MonthlyLimitCents = *req.MonthlyLimitCents
	}

	apiKey := existing.APIKey
	if req.RegenerateAPIKey {
		apiKey, err = generateAPIKey()
		if err != nil {
			return nil, fmt.Errorf("generate api key: %w", err)
		}
	}

	now := time.Now().Unix()
	_, err = s.db.Exec(
		`UPDATE users SET name=?, role=?, status=?, api_key=?, daily_limit_cents=?, monthly_limit_cents=?, updated_at=?
		 WHERE id=?`,
		existing.Name, existing.Role, existing.Status, apiKey,
		existing.DailyLimitCents, existing.MonthlyLimitCents,
		now, id,
	)
	if err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}

	return s.GetUserByID(id)
}

func (s *Store) DeleteUser(id string) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RegenerateAPIKey(id string) (string, error) {
	apiKey, err := generateAPIKey()
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`UPDATE users SET api_key=?, updated_at=? WHERE id=?`, apiKey, now, id)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return "", sql.ErrNoRows
	}
	return apiKey, nil
}

// --- Channel access ---

func (s *Store) GetUserChannels(userID string) ([]UserChannel, error) {
	rows, err := s.db.Query(
		`SELECT user_id, channel_type, channel_index FROM user_channels WHERE user_id = ?`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []UserChannel
	for rows.Next() {
		var ch UserChannel
		if err := rows.Scan(&ch.UserID, &ch.ChannelType, &ch.ChannelIndex); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (s *Store) SetUserChannels(userID string, channels []UserChannel) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM user_channels WHERE user_id = ?`, userID); err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO user_channels (user_id, channel_type, channel_index) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ch := range channels {
		if _, err := stmt.Exec(userID, ch.ChannelType, ch.ChannelIndex); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) UserHasChannelAccess(userID, channelType string, channelIndex int) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM user_channels WHERE user_id = ? AND channel_type = ? AND channel_index = ?`,
		userID, channelType, channelIndex,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// --- Usage tracking ---

func (s *Store) RecordUsage(userID string, inputTokens, outputTokens, costCents int) error {
	date := time.Now().Format("2006-01-02")
	_, err := s.db.Exec(
		`INSERT INTO user_usage (user_id, date, request_count, input_tokens, output_tokens, cost_cents)
		 VALUES (?, ?, 1, ?, ?, ?)
		 ON CONFLICT(user_id, date) DO UPDATE SET
		   request_count = request_count + 1,
		   input_tokens = input_tokens + ?,
		   output_tokens = output_tokens + ?,
		   cost_cents = cost_cents + ?`,
		userID, date, inputTokens, outputTokens, costCents,
		inputTokens, outputTokens, costCents,
	)
	return err
}

func (s *Store) GetDailyUsage(userID, date string) (*UserUsage, error) {
	var u UserUsage
	err := s.db.QueryRow(
		`SELECT user_id, date, request_count, input_tokens, output_tokens, cost_cents
		 FROM user_usage WHERE user_id = ? AND date = ?`,
		userID, date,
	).Scan(&u.UserID, &u.Date, &u.RequestCount, &u.InputTokens, &u.OutputTokens, &u.CostCents)
	if err == sql.ErrNoRows {
		return &UserUsage{UserID: userID, Date: date}, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetMonthlyUsage(userID, yearMonth string) (*UserUsage, error) {
	var u UserUsage
	u.UserID = userID
	u.Date = yearMonth

	err := s.db.QueryRow(
		`SELECT COALESCE(SUM(request_count),0), COALESCE(SUM(input_tokens),0),
		        COALESCE(SUM(output_tokens),0), COALESCE(SUM(cost_cents),0)
		 FROM user_usage WHERE user_id = ? AND date LIKE ?`,
		userID, yearMonth+"%",
	).Scan(&u.RequestCount, &u.InputTokens, &u.OutputTokens, &u.CostCents)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
