package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"r1rpc/internal/app"
	"r1rpc/internal/auth"
	"r1rpc/internal/model"
	"r1rpc/internal/rpc"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	App       *app.App
	wsClients *clientWSSessions
}

func New(app *app.App) *Server {
	return &Server{
		App:       app,
		wsClients: newClientWSSessions(),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /static/{file...}", s.handleStatic)
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /api/auth/login", s.handleAdminLogin)
	mux.HandleFunc("GET /api/users", s.requireRole("admin", s.handleListUsers))
	mux.HandleFunc("POST /api/users", s.requireRole("admin", s.handleCreateUser))
	mux.HandleFunc("PATCH /api/users/{id}", s.requireRole("admin", s.handlePatchUser))
	mux.HandleFunc("PATCH /api/users/{id}/status", s.requireRole("admin", s.handlePatchUserStatus))
	mux.HandleFunc("PATCH /api/users/{id}/password", s.requireRole("admin", s.handlePatchUserPassword))
	mux.HandleFunc("GET /api/groups", s.requireRole("admin", s.handleGroups))
	mux.HandleFunc("POST /api/groups", s.requireRole("admin", s.handleCreateGroup))
	mux.HandleFunc("GET /api/groups/{name}/actions", s.requireRole("admin", s.handleGroupActions))
	mux.HandleFunc("PATCH /api/groups/{name}", s.requireRole("admin", s.handleUpdateGroup))
	mux.HandleFunc("PATCH /api/groups/{name}/status", s.requireRole("admin", s.handlePatchGroupStatus))
	mux.HandleFunc("POST /api/groups/{name}/device-key", s.requireRole("admin", s.handleRotateGroupDeviceKey))
	mux.HandleFunc("POST /api/groups/{name}/api-key", s.requireRole("admin", s.handleRotateGroupAPIKey))
	mux.HandleFunc("DELETE /api/groups/{name}", s.requireRole("admin", s.handleDeleteGroup))
	mux.HandleFunc("GET /api/devices", s.requireRole("admin", s.handleDevices))
	mux.HandleFunc("DELETE /api/devices/{clientId}", s.requireRole("admin", s.handleDeleteDevice))
	mux.HandleFunc("GET /api/monitor/requests", s.requireRole("admin", s.handleMonitorRequests))
	mux.HandleFunc("GET /api/monitor/request-options", s.requireRole("admin", s.handleMonitorRequestOptions))
	mux.HandleFunc("GET /api/monitor/groups/summary", s.requireRole("admin", s.handleGroupSummary))
	mux.HandleFunc("GET /api/metrics/clients/weekly", s.requireRole("admin", s.handleWeeklyMetrics))
	mux.HandleFunc("GET /api/metrics/clients/{clientId}/daily", s.requireRole("admin", s.handleClientDailyMetrics))
	mux.HandleFunc("GET /api/metrics/trends", s.requireRole("admin", s.handleTrendMetrics))
	mux.HandleFunc("GET /api/metrics/realtime", s.requireRole("admin", s.handleRealtimeMetrics))
	mux.HandleFunc("GET /api/health/probes", s.requireRole("admin", s.handleHealthProbes))
	mux.HandleFunc("POST /api/client/login", s.handleClientLogin)
	mux.HandleFunc("GET /api/client/ws", s.handleClientWS)
	mux.HandleFunc("POST /api/client/result", s.requireRole("client", s.handleClientResult))
	mux.HandleFunc("POST /api/client/logout", s.requireRole("client", s.handleClientLogout))
	mux.HandleFunc("GET /rpc/clientQueue", s.handleRPCClientQueue)
	mux.HandleFunc("POST /rpc/{group}/{action}", s.handleInvoke)
	return mux
}

func (s *Server) requireRole(role string, next func(http.ResponseWriter, *http.Request, *auth.Claims)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, err := s.App.VerifyTokenFromRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err)
			return
		}
		if claims.Role != role && !(role == "client" && claims.Role == "admin") {
			writeError(w, http.StatusForbidden, fmt.Errorf("权限不足"))
			return
		}
		next(w, r, claims)
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	if name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(uiFS, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(name, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	default:
		w.Header().Set("Content-Type", http.DetectContentType(data))
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(uiFS, "index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"name":     s.App.Config.AppName,
		"serverId": s.App.Config.ServerID,
		"time":     time.Now().Format(time.RFC3339),
	})
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	token, user, err := s.App.LoginAdmin(r.Context(), req.Username, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     app.AdminAuthCookieName(),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((12 * time.Hour) / time.Second),
	})
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	users, err := s.App.Store.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": users})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
		Enabled  bool   `json:"enabled"`
		Notes    string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Role == "" {
		req.Role = "admin"
	}
	if req.Role != "admin" && req.Role != "client" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("角色必须是 admin 或 client"))
		return
	}
	user, err := s.App.Store.CreateUser(r.Context(), req.Username, req.Password, req.Role, req.Enabled, req.Notes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) handlePatchUser(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Username string `json:"username"`
		Notes    string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.App.Store.UpdateUserProfile(r.Context(), userID, req.Username, req.Notes); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePatchUserStatus(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.App.Store.UpdateUserStatus(r.Context(), userID, req.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePatchUserPassword(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("请输入密码"))
		return
	}
	if err := s.App.Store.UpdateUserPassword(r.Context(), userID, req.Password); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	items, err := s.App.Store.ListGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	items = s.applyRealtimeGroupStates(items)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	var req struct {
		Name        string `json:"name"`
		Group       string `json:"group"`
		DisplayName string `json:"displayName"`
		AuthMode    string `json:"authMode"`
		Enabled     *bool  `json:"enabled"`
		Notes       string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(req.Group)
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	group, err := s.App.Store.CreateGroup(r.Context(), name, req.DisplayName, req.AuthMode, enabled, req.Notes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, group)
}

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	name := strings.TrimSpace(r.PathValue("name"))
	var req struct {
		DisplayName string  `json:"displayName"`
		Notes       string  `json:"notes"`
		AuthMode    *string `json:"authMode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.App.Store.UpdateGroupProfile(r.Context(), name, req.DisplayName, req.Notes); err != nil {
		if app.IsNotFound(err) {
			writeError(w, http.StatusNotFound, &app.GroupError{Kind: app.ErrGroupNotFound, Group: name})
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if req.AuthMode != nil {
		if err := s.App.Store.SetGroupAuthMode(r.Context(), name, *req.AuthMode); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRotateGroupAPIKey(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	name := strings.TrimSpace(r.PathValue("name"))
	key, err := s.App.Store.RotateGroupAPIKey(r.Context(), name)
	if err != nil {
		if app.IsNotFound(err) {
			writeError(w, http.StatusNotFound, &app.GroupError{Kind: app.ErrGroupNotFound, Group: name})
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"apiKey": key})
}

func (s *Server) handleGroupActions(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	name := strings.TrimSpace(r.PathValue("name"))
	actions, err := s.App.Store.GroupActions(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions})
}

func (s *Server) handlePatchGroupStatus(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	groupName := strings.TrimSpace(r.PathValue("name"))
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.App.Store.UpdateGroupStatus(r.Context(), groupName, req.Enabled); err != nil {
		if app.IsNotFound(err) {
			writeError(w, http.StatusNotFound, &app.GroupError{Kind: app.ErrGroupNotFound, Group: groupName})
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	name := strings.TrimSpace(r.PathValue("name"))
	for _, sess := range s.realtimeSessions() {
		if sess.Group == name {
			writeError(w, http.StatusConflict, fmt.Errorf("分组下有在线设备，无法删除"))
			return
		}
	}
	if err := s.App.Store.DeleteGroup(r.Context(), name); err != nil {
		if app.IsNotFound(err) {
			writeError(w, http.StatusNotFound, &app.GroupError{Kind: app.ErrGroupNotFound, Group: name})
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRotateGroupDeviceKey(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	name := strings.TrimSpace(r.PathValue("name"))
	key, err := s.App.Store.RotateGroupDeviceKey(r.Context(), name)
	if err != nil {
		if app.IsNotFound(err) {
			writeError(w, http.StatusNotFound, &app.GroupError{Kind: app.ErrGroupNotFound, Group: name})
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deviceKey": key})
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	items, err := s.App.Store.ListDevices(r.Context(), r.URL.Query().Get("group"), r.URL.Query().Get("client"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	items = s.applyRealtimeDeviceStates(items)
	if statusFilter != "" {
		filtered := items[:0]
		for _, item := range items {
			if strings.ToLower(strings.TrimSpace(item.Status)) == statusFilter {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	clientID := strings.TrimSpace(r.PathValue("clientId"))
	if clientID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("clientId 不能为空"))
		return
	}
	if _, online := s.realtimeSessions()[clientID]; online {
		writeError(w, http.StatusConflict, fmt.Errorf("在线设备无法删除"))
		return
	}
	if err := s.App.Store.DeleteDevice(r.Context(), clientID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRPCClientQueue(w http.ResponseWriter, r *http.Request) {
	groupName := strings.TrimSpace(r.URL.Query().Get("group"))
	if groupName == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("分组不能为空"))
		return
	}
	if err := s.App.EnsureGroupActive(r.Context(), groupName); err != nil {
		if status, ok := groupErrorHTTPStatus(err); ok {
			writeError(w, status, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))

	items, err := s.App.Store.ListDevices(r.Context(), groupName, "", limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	items = s.applyRealtimeDeviceStates(items)
	sessions := s.realtimeSessions()

	queue := make([]map[string]any, 0, len(items))
	clientIDs := make([]string, 0, len(items))
	for _, item := range items {
		currentStatus := strings.ToLower(strings.TrimSpace(item.Status))
		if status == "" {
			if currentStatus != "online" {
				continue
			}
		} else if currentStatus != strings.ToLower(status) {
			continue
		}

		session := sessions[item.ClientID]
		queue = append(queue, map[string]any{
			"clientId":     item.ClientID,
			"group":        item.GroupName,
			"platform":     item.Platform,
			"status":       item.Status,
			"lastSeenAt":   item.LastSeenAt.Format(time.RFC3339),
			"lastIp":       item.LastIP,
			"pendingCount": session.Pending.Len(),
			"inFlight":     session.InFlight,
			"maxInFlight":  session.MaxInFlight,
		})
		clientIDs = append(clientIDs, item.ClientID)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"group":     groupName,
		"count":     len(queue),
		"clientIds": clientIDs,
		"items":     queue,
	})
}

func (s *Server) handleMonitorRequests(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	items, total, err := s.App.Store.ListRPCRequests(r.Context(), r.URL.Query().Get("group"), r.URL.Query().Get("action"), r.URL.Query().Get("client"), r.URL.Query().Get("status"), page, pageSize)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	writeJSON(w, http.StatusOK, model.RPCRequestPage{Items: items, Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages})
}

func (s *Server) handleMonitorRequestOptions(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	items, err := s.App.Store.ListRPCRequestFilterOptions(r.Context(), r.URL.Query().Get("group"), r.URL.Query().Get("action"), r.URL.Query().Get("client"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *Server) handleGroupSummary(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	items, err := s.App.Store.GroupSummary(r.Context(), hours)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleWeeklyMetrics(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	items, err := s.App.Store.WeeklyMetrics(r.Context(), r.URL.Query().Get("group"), r.URL.Query().Get("client"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleClientDailyMetrics(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	items, err := s.App.Store.ClientDailyMetrics(r.Context(), r.PathValue("clientId"), days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleTrendMetrics(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	items, err := s.App.Store.TrendMetrics(r.Context(), r.URL.Query().Get("group"), r.URL.Query().Get("action"), r.URL.Query().Get("client"), days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleRealtimeMetrics(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	minutes, _ := strconv.Atoi(r.URL.Query().Get("minutes"))
	buckets, err := s.App.Store.RealtimeBuckets(r.Context(), minutes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	groups, err := s.App.Store.RealtimeBucketsByGroup(r.Context(), minutes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": buckets, "groups": groups})
}

func (s *Server) handleHealthProbes(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	type groupProbe struct {
		Group   string           `json:"group"`
		Buckets []app.ProbeBucket `json:"buckets"`
	}
	groups, err := s.App.Store.ListGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	result := make([]groupProbe, 0, len(groups))
	for _, g := range groups {
		result = append(result, groupProbe{
			Group:   g.GroupName,
			Buckets: s.App.ProbeHistory.Last60(g.GroupName),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": result})
}

func (s *Server) handleClientLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceKey   string         `json:"deviceKey"`
		ClientID    string         `json:"clientId"`
		Group       string         `json:"group"`
		Platform    string         `json:"platform"`
		MaxInFlight int            `json:"maxInFlight"`
		Extra       map[string]any `json:"extra"`
		Actions     []string       `json:"actions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.MaxInFlight <= 0 {
		req.MaxInFlight = s.App.Config.ClientMaxInFlight
	}
	if req.MaxInFlight < 1 {
		req.MaxInFlight = 1
	}
	if req.MaxInFlight > 1024 {
		req.MaxInFlight = 1024
	}
	token, err := s.App.LoginClient(r.Context(), req.DeviceKey, req.ClientID, req.Group, req.Platform, req.MaxInFlight, req.Extra, req.Actions, s.App.RemoteIP(r))
	if err != nil {
		if status, ok := groupErrorHTTPStatus(err); ok {
			writeError(w, status, err)
			return
		}
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "group": req.Group, "maxInFlight": req.MaxInFlight, "transport": "websocket", "wsUrl": s.clientWSURL(r, token)})
}

func (s *Server) handleClientResult(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	var req struct {
		RequestID             string          `json:"requestId"`
		Status                string          `json:"status"`
		HTTPCode              int             `json:"httpCode"`
		Payload               json.RawMessage `json:"payload"`
		PayloadEncoding       string          `json:"payloadEncoding"`
		PayloadRawSize        int             `json:"payloadRawSize"`
		PayloadCompressedSize int             `json:"payloadCompressedSize"`
		Error                 string          `json:"error"`
		LatencyMS             int64           `json:"latencyMs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.RequestID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("requestId 不能为空"))
		return
	}
	if strings.TrimSpace(req.Status) == "" {
		req.Status = "success"
	}
	result := rpc.JobResult{RequestID: req.RequestID, Status: req.Status, HTTPCode: req.HTTPCode, Payload: req.Payload, PayloadEncoding: req.PayloadEncoding, PayloadRawSize: req.PayloadRawSize, PayloadCompressedSize: req.PayloadCompressedSize, Error: req.Error, LatencyMS: req.LatencyMS}
	if err := normalizeClientJobResult(&result); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	err := s.App.SubmitClientResult(r.Context(), claims, result)
	if err != nil {
		if status, ok := groupErrorHTTPStatus(err); ok {
			writeError(w, status, err)
			return
		}
		if errors.Is(err, rpc.ErrResultClientMismatch) {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleClientLogout(w http.ResponseWriter, r *http.Request, claims *auth.Claims) {
	s.App.Hub.Unregister(claims.ClientID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleInvoke(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("group")
	actionName := r.PathValue("action")
	var req app.InvokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// 取分组的鉴权配置（找不到则置 nil，由 InvokeRPC 返回分组不存在）
	group, _ := s.App.Store.GetGroupByName(r.Context(), groupName)
	claims, err := s.authenticateInvokeRequest(r, req, group)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	result, _, _, err := s.App.InvokeRPC(r.Context(), claims, groupName, actionName, req)
	if err != nil {
		httpCode := http.StatusBadGateway
		switch {
		case errors.Is(err, app.ErrGroupNotFound):
			httpCode = http.StatusNotFound
		case errors.Is(err, app.ErrGroupDisabled):
			httpCode = http.StatusForbidden
		case errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.TrimSpace(err.Error()), "context deadline exceeded"):
			httpCode = http.StatusGatewayTimeout
		case errors.Is(err, rpc.ErrNoOnlineClient), errors.Is(err, rpc.ErrPreferredClientDown):
			httpCode = http.StatusBadGateway
		case errors.Is(err, rpc.ErrClientQueueFull), errors.Is(err, rpc.ErrGroupSaturated):
			httpCode = http.StatusTooManyRequests
		}
		writeEnvelope(w, httpCode, false, err.Error(), nil)
		return
	}
	isOK := strings.EqualFold(result.Status, "success") && strings.TrimSpace(result.Error) == ""
	// data 直接为设备返回的 payload；success/msg 反映调用结果
	writeEnvelope(w, http.StatusOK, isOK, result.Error, jsonBodyOrEmpty(result.Payload))
}

// authenticateInvokeRequest 对外调用鉴权（鉴权模式按分组决定）：
//   - 面板 admin JWT（后台 RPC 调用页用）：存在且有效即放行
//   - 分组 auth_mode=none：受信内网，匿名 caller
//   - 分组 auth_mode=apikey：校验 X-API-Key（header 或 body apiKey 字段）等于该分组的 api_key
//
// group 为 nil（分组不存在）时按 none 放行，由后续 InvokeRPC 返回「分组不存在」。
func (s *Server) authenticateInvokeRequest(r *http.Request, req app.InvokeRequest, group *model.GroupInfo) (*auth.Claims, error) {
	// 面板 admin JWT（可选）
	if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		if claims, err := s.App.VerifyTokenFromRequest(r); err == nil && claims.Role == "admin" {
			return claims, nil
		}
	}

	if group == nil || group.AuthMode != "apikey" {
		return &auth.Claims{Username: "anonymous", Role: "caller"}, nil
	}

	// 分组要求 apikey
	key := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if key == "" {
		key = strings.TrimSpace(req.APIKey)
	}
	if key == "" {
		return nil, fmt.Errorf("缺少 X-API-Key 请求头或 apiKey 字段")
	}
	if key != group.APIKey {
		return nil, fmt.Errorf("API 密钥无效")
	}
	return &auth.Claims{Username: "apikey", Role: "caller"}, nil
}

func (s *Server) realtimeSessions() map[string]rpc.ClientSession {
	seconds := s.App.Config.DeviceOfflineSeconds
	if seconds <= 0 {
		if s.App.Config.DeviceOfflineMinutes > 0 {
			seconds = s.App.Config.DeviceOfflineMinutes * 60
		} else {
			seconds = 20
		}
	}
	cutoff := time.Now().Add(-time.Duration(seconds) * time.Second)
	result := map[string]rpc.ClientSession{}
	for _, session := range s.App.Hub.OnlineClients() {
		if session.LastSeenAt.Before(cutoff) {
			continue
		}
		result[session.ClientID] = session
	}
	return result
}

func (s *Server) applyRealtimeDeviceStates(items []model.Device) []model.Device {
	sessions := s.realtimeSessions()
	for index := range items {
		session, ok := sessions[items[index].ClientID]
		if !ok {
			items[index].Status = "offline"
			continue
		}

		items[index].Status = "online"
		if session.LastSeenAt.After(items[index].LastSeenAt) {
			items[index].LastSeenAt = session.LastSeenAt
		}
		if items[index].GroupName == "" {
			items[index].GroupName = session.Group
		}
		if items[index].Platform == "" {
			items[index].Platform = session.Platform
		}
	}
	return items
}

func (s *Server) applyRealtimeGroupStates(items []model.GroupInfo) []model.GroupInfo {
	sessions := s.realtimeSessions()
	groupOnlineCounts := map[string]int64{}
	groupLastSeen := map[string]time.Time{}
	for _, session := range sessions {
		groupOnlineCounts[session.Group]++
		if session.LastSeenAt.After(groupLastSeen[session.Group]) {
			groupLastSeen[session.Group] = session.LastSeenAt
		}
	}

	for index := range items {
		if count, ok := groupOnlineCounts[items[index].GroupName]; ok && count > 0 {
			items[index].OnlineDevices = count
			items[index].Status = "online"
			items[index].StatusLabel = "Online"
			lastSeen := groupLastSeen[items[index].GroupName]
			if !lastSeen.IsZero() {
				items[index].LastSeenAt = &lastSeen
			}
		}
	}
	return items
}

// 统一响应信封：所有 JSON 响应均为 {success, msg, data}
// 成功且未指定 msg 时默认 "ok"
func writeEnvelope(w http.ResponseWriter, status int, success bool, msg string, data any) {
	if success && msg == "" {
		msg = "ok"
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": success,
		"msg":     msg,
		"data":    data,
	})
}

// 成功响应：payload 作为 data 包进信封
func writeJSON(w http.ResponseWriter, status int, payload any) {
	writeEnvelope(w, status, true, "", payload)
}

// 失败响应：错误信息进 msg，data 为 null
func writeError(w http.ResponseWriter, status int, err error) {
	writeEnvelope(w, status, false, err.Error(), nil)
}

func groupErrorHTTPStatus(err error) (int, bool) {
	switch {
	case errors.Is(err, app.ErrGroupNotFound):
		return http.StatusNotFound, true
	case errors.Is(err, app.ErrGroupDisabled):
		return http.StatusForbidden, true
	default:
		return 0, false
	}
}

func jsonBodyOrEmpty(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return json.RawMessage("{}")
	}
	return raw
}
