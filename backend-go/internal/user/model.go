package user

import "time"

type User struct {
	ID                string    `json:"id"`
	Email             string    `json:"email"`
	Name              string    `json:"name"`
	APIKey            string    `json:"api_key,omitempty"`
	Role              string    `json:"role"`               // "admin" | "user"
	Status            string    `json:"status"`             // "active" | "disabled"
	DailyLimitCents   int       `json:"daily_limit_cents"`
	MonthlyLimitCents int       `json:"monthly_limit_cents"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type UserChannel struct {
	UserID       string `json:"user_id"`
	ChannelType  string `json:"channel_type"`  // "messages" | "responses" | "gemini"
	ChannelIndex int    `json:"channel_index"`
}

type UserUsage struct {
	UserID       string `json:"user_id"`
	Date         string `json:"date"`
	RequestCount int    `json:"request_count"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CostCents    int    `json:"cost_cents"`
}

type CreateUserRequest struct {
	Email             string `json:"email" binding:"required,email"`
	Name              string `json:"name" binding:"required"`
	Role              string `json:"role"`
	DailyLimitCents   int    `json:"daily_limit_cents"`
	MonthlyLimitCents int    `json:"monthly_limit_cents"`
}

type UpdateUserRequest struct {
	Name              *string `json:"name"`
	Role              *string `json:"role"`
	Status            *string `json:"status"`
	DailyLimitCents   *int    `json:"daily_limit_cents"`
	MonthlyLimitCents *int    `json:"monthly_limit_cents"`
	RegenerateAPIKey  bool    `json:"regenerate_api_key"`
}
