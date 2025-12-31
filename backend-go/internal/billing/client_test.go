package billing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_ValidateAPIKey(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   interface{}
		wantErr    bool
	}{
		{
			name:       "valid key",
			statusCode: 200,
			response:   BalanceResponse{BalanceCents: 1000, FrozenCents: 100},
			wantErr:    false,
		},
		{
			name:       "invalid key",
			statusCode: 401,
			response:   map[string]string{"error": "unauthorized"},
			wantErr:    true,
		},
		{
			name:       "server error",
			statusCode: 500,
			response:   map[string]string{"error": "internal error"},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/billing/balance" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") == "" {
					t.Error("missing Authorization header")
				}
				w.WriteHeader(tt.statusCode)
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer server.Close()

			client := NewClient(server.URL)
			balance, err := client.ValidateAPIKey("test-key")

			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAPIKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && balance == nil {
				t.Error("ValidateAPIKey() returned nil balance")
			}
		})
	}
}

func TestClient_PreAuthorize(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
		errType    error
	}{
		{
			name:       "success",
			statusCode: 200,
			wantErr:    false,
		},
		{
			name:       "insufficient balance",
			statusCode: 402,
			wantErr:    true,
			errType:    ErrInsufficientBalance,
		},
		{
			name:       "server error",
			statusCode: 500,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/billing/preauthorize" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if r.Method != "POST" {
					t.Errorf("unexpected method: %s", r.Method)
				}
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			client := NewClient(server.URL)
			err := client.PreAuthorize("test-key", "req-123", 500)

			if (err != nil) != tt.wantErr {
				t.Errorf("PreAuthorize() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errType != nil && err != tt.errType {
				t.Errorf("PreAuthorize() error = %v, want %v", err, tt.errType)
			}
		})
	}
}

func TestClient_Charge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/billing/charge" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var payload map[string]interface{}
		json.NewDecoder(r.Body).Decode(&payload)

		if payload["request_id"] != "req-123" {
			t.Errorf("unexpected request_id: %v", payload["request_id"])
		}

		w.WriteHeader(200)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	err := client.Charge("test-key", "req-123", 500, 300, "test charge")

	if err != nil {
		t.Errorf("Charge() error = %v", err)
	}
}

func TestClient_Release(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/billing/release" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	err := client.Release("test-key", "req-123", 500)

	if err != nil {
		t.Errorf("Release() error = %v", err)
	}
}

func TestClient_IsEnabled(t *testing.T) {
	client := NewClient("http://example.com")
	if !client.IsEnabled() {
		t.Error("IsEnabled() should return true when baseURL is set")
	}

	client = NewClient("")
	if client.IsEnabled() {
		t.Error("IsEnabled() should return false when baseURL is empty")
	}
}
