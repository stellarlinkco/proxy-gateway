package common

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandleAllChannelsFailed_CoversBranches(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("fuzzy mode returns generic 503", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		HandleAllChannelsFailed(c, true, &FailoverError{Status: 401, Body: []byte(`{}`)}, errors.New("boom"), "Messages")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("non-fuzzy passthrough JSON failover error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		HandleAllChannelsFailed(c, false, &FailoverError{Status: 401, Body: []byte(`{"error":{"message":"x"}}`)}, nil, "Messages")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if w.Body.String() == "" {
			t.Fatalf("empty body")
		}
	})

	t.Run("non-fuzzy passthrough non-JSON failover error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		HandleAllChannelsFailed(c, false, &FailoverError{Status: 503, Body: []byte(`not-json`)}, nil, "Messages")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("non-fuzzy lastError branch", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		HandleAllChannelsFailed(c, false, nil, errors.New("boom"), "Messages")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestHandleAllKeysFailed_CoversBranches(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("fuzzy mode returns generic 503", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		HandleAllKeysFailed(c, true, &FailoverError{Status: 401, Body: []byte(`{}`)}, errors.New("boom"), "Responses")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("non-fuzzy passthrough JSON failover error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		HandleAllKeysFailed(c, false, &FailoverError{Status: 500, Body: []byte(`{"error":{"message":"x"}}`)}, nil, "Responses")
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("non-fuzzy passthrough non-JSON failover error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		HandleAllKeysFailed(c, false, &FailoverError{Status: 0, Body: []byte(`not-json`)}, nil, "Responses")
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("non-fuzzy lastError branch", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		HandleAllKeysFailed(c, false, nil, errors.New("boom"), "Responses")
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
}
