// Package billing 提供 swe-agent 计费服务客户端
package billing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// Client swe-agent 计费服务客户端
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// BalanceResponse 余额查询响应
type BalanceResponse struct {
	BalanceCents int64 `json:"balance_cents"`
	FrozenCents  int64 `json:"frozen_cents"`
}

// NewClient 创建计费客户端
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ValidateAPIKey 验证 API Key 并返回用户信息
func (c *Client) ValidateAPIKey(apiKey string) (*BalanceResponse, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/billing/balance", nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("invalid api key")
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var balance BalanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&balance); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}
	return &balance, nil
}

// PreAuthorize 预授权
func (c *Client) PreAuthorize(apiKey, requestID string, amountCents int64) error {
	payload := map[string]interface{}{
		"request_id":   requestID,
		"amount_cents": amountCents,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", c.baseURL+"/api/billing/preauthorize", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 402 {
		return ErrInsufficientBalance
	}
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("preauthorize failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[Billing-PreAuth] requestID=%s, amount=%d cents", requestID, amountCents)
	return nil
}

// Charge 扣费
func (c *Client) Charge(apiKey, requestID string, preAuthCents, actualCents int64, description string) error {
	payload := map[string]interface{}{
		"request_id":     requestID,
		"preauth_cents":  preAuthCents,
		"actual_cents":   actualCents,
		"description":    description,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", c.baseURL+"/api/billing/charge", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("charge failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[Billing-Charge] requestID=%s, preAuth=%d, actual=%d cents", requestID, preAuthCents, actualCents)
	return nil
}

// Release 释放预授权
func (c *Client) Release(apiKey, requestID string, amountCents int64) error {
	payload := map[string]interface{}{
		"request_id":   requestID,
		"amount_cents": amountCents,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", c.baseURL+"/api/billing/release", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("release failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[Billing-Release] requestID=%s, amount=%d cents", requestID, amountCents)
	return nil
}

// GetBalance 查询余额
func (c *Client) GetBalance(apiKey string) (*BalanceResponse, error) {
	return c.ValidateAPIKey(apiKey)
}

// IsEnabled 检查计费服务是否启用
func (c *Client) IsEnabled() bool {
	return c.baseURL != ""
}
