package ctl_svc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// writeJSON 写 JSON 响应，状态码 code。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr 写统一的错误响应体 {"error": msg}。
func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// errAgentNotFound 构造「找不到目标 agent」的错误，回显调用方给的定位信息。
func errAgentNotFound(agentName string, agentID int64) error {
	if agentID > 0 {
		return fmt.Errorf("agent id %d not found", agentID)
	}
	if name := strings.TrimSpace(agentName); name != "" {
		return fmt.Errorf("agent %q not found", name)
	}
	return fmt.Errorf("agent is required (set agent or agentId)")
}
