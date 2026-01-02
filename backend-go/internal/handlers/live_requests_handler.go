package handlers

import (
	"net/http"
	"strings"

	"github.com/BenedictKing/claude-proxy/internal/monitor"
	"github.com/gin-gonic/gin"
)

// LiveRequestsHandler 处理实时请求 API
type LiveRequestsHandler struct {
	manager *monitor.LiveRequestManager
}

// NewLiveRequestsHandler 创建 handler
func NewLiveRequestsHandler(manager *monitor.LiveRequestManager) *LiveRequestsHandler {
	return &LiveRequestsHandler{manager: manager}
}

// GetLiveRequests 获取正在进行的请求
// GET /api/{messages|responses|gemini}/live
func (h *LiveRequestsHandler) GetLiveRequests(c *gin.Context) {
	if h == nil || h.manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "实时请求监控未启用"})
		return
	}

	apiType := apiTypeFromAdminLivePath(c.FullPath())
	if apiType == "" {
		apiType = apiTypeFromAdminLivePath(c.Request.URL.Path)
	}

	var requests []*monitor.LiveRequest
	if apiType == "" {
		requests = h.manager.GetAllRequests()
	} else {
		requests = h.manager.GetRequestsByAPIType(apiType)
	}

	c.JSON(http.StatusOK, monitor.LiveRequestsResponse{
		Requests: requests,
		Count:    len(requests),
	})
}

func apiTypeFromAdminLivePath(path string) string {
	// 期望格式：/api/{messages|responses|gemini}/live
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return ""
	}
	if parts[0] != "api" || parts[2] != "live" {
		return ""
	}

	apiType := parts[1]
	switch apiType {
	case "messages", "responses", "gemini":
		return apiType
	default:
		return ""
	}
}
