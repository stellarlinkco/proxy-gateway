package user

import (
	"fmt"
	"time"
)

// CheckSpendingLimit checks if user is within daily/monthly limits.
// Returns nil if within limits, error if exceeded.
func (s *Store) CheckSpendingLimit(userID string, estimatedCost int) error {
	user, err := s.GetUserByID(userID)
	if err != nil {
		return err
	}

	today := time.Now().Format("2006-01-02")
	yearMonth := time.Now().Format("2006-01")

	// Check daily limit (0 = unlimited)
	if user.DailyLimitCents > 0 {
		daily, _ := s.GetDailyUsage(userID, today)
		if daily != nil && daily.CostCents+estimatedCost > user.DailyLimitCents {
			return fmt.Errorf("daily spending limit exceeded (%d/%d cents)", daily.CostCents, user.DailyLimitCents)
		}
	}

	// Check monthly limit (0 = unlimited)
	if user.MonthlyLimitCents > 0 {
		monthly, _ := s.GetMonthlyUsage(userID, yearMonth)
		if monthly != nil && monthly.CostCents+estimatedCost > user.MonthlyLimitCents {
			return fmt.Errorf("monthly spending limit exceeded (%d/%d cents)", monthly.CostCents, user.MonthlyLimitCents)
		}
	}

	return nil
}
