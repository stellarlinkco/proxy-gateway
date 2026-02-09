// Package users provides HTTP handlers for user management
package users

import (
	"net/http"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/user"
	"github.com/gin-gonic/gin"
)

// requireAdmin checks that the caller has admin role; returns false and writes 403 if not.
func requireAdmin(c *gin.Context) bool {
	role, _ := c.Get("user_role")
	if role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return false
	}
	return true
}

// ListUsers returns all users (admin only).
// GET /api/users
func ListUsers(userStore *user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAdmin(c) {
			return
		}

		users, err := userStore.ListUsers()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Mask API keys in response
		for i := range users {
			users[i].APIKey = maskAPIKey(users[i].APIKey)
		}

		c.JSON(http.StatusOK, users)
	}
}

// CreateUser creates a new user (admin only).
// POST /api/users
func CreateUser(userStore *user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAdmin(c) {
			return
		}

		var req user.CreateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		u, err := userStore.CreateUser(req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, u)
	}
}

// GetUser returns a single user by ID (admin only).
// GET /api/users/:id
func GetUser(userStore *user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAdmin(c) {
			return
		}

		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		u, err := userStore.GetUserByID(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		c.JSON(http.StatusOK, u)
	}
}

// UpdateUser updates a user by ID (admin only).
// PUT /api/users/:id
func UpdateUser(userStore *user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAdmin(c) {
			return
		}

		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		var req user.UpdateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		u, err := userStore.UpdateUser(id, req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, u)
	}
}

// DeleteUser deletes a user by ID (admin only).
// DELETE /api/users/:id
func DeleteUser(userStore *user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAdmin(c) {
			return
		}

		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		if err := userStore.DeleteUser(id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "User deleted"})
	}
}

// RegenerateAPIKey generates a new API key for a user (admin only).
// POST /api/users/:id/regenerate-key
func RegenerateAPIKey(userStore *user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAdmin(c) {
			return
		}

		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		newKey, err := userStore.RegenerateAPIKey(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"api_key": newKey})
	}
}

// GetUserChannels returns the channel permissions for a user.
// GET /api/users/:id/channels
func GetUserChannels(userStore *user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAdmin(c) {
			return
		}

		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		channels, err := userStore.GetUserChannels(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, channels)
	}
}

// SetUserChannels replaces the channel permissions for a user.
// PUT /api/users/:id/channels
func SetUserChannels(userStore *user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAdmin(c) {
			return
		}

		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		var channels []user.UserChannel
		if err := c.ShouldBindJSON(&channels); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		// Set the user_id for all channels
		for i := range channels {
			channels[i].UserID = id
		}

		if err := userStore.SetUserChannels(id, channels); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Channels updated"})
	}
}

// GetUserUsage returns usage statistics for a user (admin only).
// GET /api/users/:id/usage
func GetUserUsage(userStore *user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requireAdmin(c) {
			return
		}

		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
			return
		}

		// Get daily usage for the last 30 days
		var usageList []user.UserUsage
		now := time.Now()
		for i := 0; i < 30; i++ {
			date := now.AddDate(0, 0, -i).Format("2006-01-02")
			usage, err := userStore.GetDailyUsage(id, date)
			if err != nil {
				continue
			}
			if usage.RequestCount > 0 || usage.CostCents > 0 {
				usageList = append(usageList, *usage)
			}
		}

		c.JSON(http.StatusOK, usageList)
	}
}

// GetCurrentUser returns the authenticated user's own info.
// GET /api/me
func GetCurrentUser(userStore *user.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("user_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated"})
			return
		}

		id, ok := userID.(string)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid user ID in context"})
			return
		}

		// Handle admin user (legacy single-key mode)
		if id == "admin" {
			c.JSON(http.StatusOK, gin.H{
				"id":     "admin",
				"name":   "Administrator",
				"email":  "admin@local",
				"role":   "admin",
				"status": "active",
			})
			return
		}

		u, err := userStore.GetUserByID(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":                  u.ID,
			"email":               u.Email,
			"name":                u.Name,
			"role":                u.Role,
			"status":              u.Status,
			"api_key":             maskAPIKey(u.APIKey),
			"daily_limit_cents":   u.DailyLimitCents,
			"monthly_limit_cents": u.MonthlyLimitCents,
			"created_at":          u.CreatedAt,
			"updated_at":          u.UpdatedAt,
		})
	}
}

// maskAPIKey masks an API key, showing only the first 8 and last 4 characters.
func maskAPIKey(key string) string {
	if len(key) <= 12 {
		return "****"
	}
	return key[:8] + "****" + key[len(key)-4:]
}
