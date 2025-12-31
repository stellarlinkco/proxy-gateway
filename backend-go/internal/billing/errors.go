package billing

import "errors"

// ErrInsufficientBalance 余额不足错误
var ErrInsufficientBalance = errors.New("insufficient balance")
