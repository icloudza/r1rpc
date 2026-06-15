package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"r1rpc/internal/auth"
	"r1rpc/internal/config"
	"r1rpc/internal/model"
	"r1rpc/internal/rpc"
	"r1rpc/internal/store"
)

const adminAuthCookieName = "r1rpc_admin_token"

func AdminAuthCookieName() string {
	return adminAuthCookieName
}

const (
	defaultPresenceFlushInterval = 15 * time.Second
	defaultMaintenanceInterval   = 5 * time.Minute
	defaultPersistQueueSize      = 4096
	defaultPersistWorkerCount    = 2
	defaultClientQueueSize       = 256
	persistTaskTimeout           = 15 * time.Second
	persistEnqueueWait           = 250 * time.Millisecond
)

var (
	ErrGroupNotFound = errors.New("分组不存在")
	ErrGroupDisabled = errors.New("分组已禁用")
)

type GroupError struct {
	Kind  error
	Group string
}

func (e *GroupError) Error() string {
	switch e.Kind {
	case ErrGroupNotFound:
		return fmt.Sprintf("分组 %q 不存在，请先在分组管理中创建", e.Group)
	case ErrGroupDisabled:
		return fmt.Sprintf("分组 %q 已禁用，请先在分组管理中启用", e.Group)
	default:
		return fmt.Sprintf("分组 %q 不可用", e.Group)
	}
}

func (e *GroupError) Unwrap() error {
	return e.Kind
}

type persistTaskKind string

const (
	persistTaskCompleteRequest persistTaskKind = "complete_request"
	persistTaskMetric          persistTaskKind = "metric"
)

type persistTask struct {
	Kind               persistTaskKind
	RequestID          string
	ClientID           string
	GroupName          string
	ActionName         string
	Status             string
	HTTPCode           int
	RequestPayload     string
	ResponsePayload    string
	LatencyMS          int64
	ErrorMessage       string
	RequesterUserID    int64
	HasRequesterUserID bool
	StatTime           time.Time
}

type App struct {
	Config *config.Config
	Store  *store.Store
	Tokens *auth.TokenManager
	Hub    *rpc.Hub

	presenceMu        sync.Mutex
	lastPresenceFlush map[string]time.Time
	persistCh         chan persistTask
	ProbeHistory      *probeHistory
}

type InvokeRequest struct {
	APIKey   string          `json:"apiKey"`
	ClientID string          `json:"clientId"`
	Payload  json.RawMessage `json:"payload"`
	Timeout  int             `json:"timeoutSeconds"`
}

func New(cfg config.Config, st *store.Store) *App {
	queueSize := cfg.PersistQueueSize
	if queueSize < defaultPersistQueueSize {
		queueSize = defaultPersistQueueSize
	}
	clientQueueSize := cfg.ClientQueueSize
	if clientQueueSize < defaultClientQueueSize {
		clientQueueSize = defaultClientQueueSize
	}
	hubMaxInFlight := cfg.ClientMaxInFlight
	if hubMaxInFlight < 1 {
		hubMaxInFlight = 1
	}
	return &App{
		Config:            &cfg,
		Store:             st,
		Tokens:            auth.NewTokenManager(cfg.JWTSecret),
		Hub:               rpc.NewHub(clientQueueSize, hubMaxInFlight),
		lastPresenceFlush: map[string]time.Time{},
		persistCh:         make(chan persistTask, queueSize),
		ProbeHistory:      newProbeHistory(st),
	}
}

func (a *App) StartBackgroundJobs(ctx context.Context) {
	workerCount := a.Config.PersistWorkers
	if workerCount < defaultPersistWorkerCount {
		workerCount = defaultPersistWorkerCount
	}
	for i := 0; i < workerCount; i++ {
		go a.persistWorker(ctx, i+1)
	}

	go func() {
		presenceTicker := time.NewTicker(a.cleanupInterval())
		defer presenceTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-presenceTicker.C:
				a.cleanupPresenceCache(time.Now())
			}
		}
	}()

	go func() {
		maintenanceTicker := time.NewTicker(defaultMaintenanceInterval)
		defer maintenanceTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-maintenanceTicker.C:
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				_ = a.Store.CleanupOldRequests(cleanupCtx, a.Config.RawRetentionDays)
				_ = a.Store.TrimAllRPCRequestScopes(cleanupCtx, a.Config.RawRequestKeepLatest)
				_ = a.Store.CleanupOldMetrics(cleanupCtx, a.Config.AggregateRetentionDays)
				cancel()
			}
		}
	}()

	go func() {
		probeTicker := time.NewTicker(30 * time.Second)
		defer probeTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-probeTicker.C:
				a.ProbeHistory.Cleanup()
				scanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				groups, err := a.Store.ListGroups(scanCtx)
				cancel()
				if err != nil {
					continue
				}
				for _, g := range groups {
					online := a.Hub.GroupOnlineCount(g.GroupName)
					a.ProbeHistory.RecordOnline(g.GroupName, online)
				}
			}
		}
	}()
}

func (a *App) LoginAdmin(ctx context.Context, username, password string) (string, *model.User, error) {
	user, err := a.Store.AuthenticateUser(ctx, username, password)
	if err != nil {
		return "", nil, err
	}
	if user.Role != "admin" {
		return "", nil, fmt.Errorf("需要管理员权限")
	}
	token, err := a.Tokens.Issue(auth.Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
	}, 12*time.Hour)
	return token, user, err
}

// LoginClient 用分组的 device key 鉴权设备（不再用用户账号）。
func (a *App) LoginClient(ctx context.Context, deviceKey, clientID, groupName, platform string, maxInFlight int, extra map[string]any, actions []string, ip string) (string, error) {
	clientID = strings.TrimSpace(clientID)
	groupName = strings.TrimSpace(groupName)
	if clientID == "" || groupName == "" {
		return "", fmt.Errorf("clientId 和 group 不能为空")
	}
	group, err := a.Store.GetGroupByName(ctx, groupName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &GroupError{Kind: ErrGroupNotFound, Group: groupName}
	}
	if err != nil {
		return "", err
	}
	if !group.Enabled {
		return "", &GroupError{Kind: ErrGroupDisabled, Group: groupName}
	}
	if strings.TrimSpace(group.DeviceKey) == "" || strings.TrimSpace(deviceKey) != group.DeviceKey {
		return "", fmt.Errorf("设备密钥无效")
	}
	if platform == "" {
		platform = "frida"
	}
	if maxInFlight <= 0 {
		maxInFlight = a.Config.ClientMaxInFlight
	}
	if maxInFlight < 1 {
		maxInFlight = 1
	}
	if maxInFlight > 1024 {
		maxInFlight = 1024
	}
	if err := a.Store.UpsertDevice(ctx, clientID, groupName, platform, ip, extra, actions); err != nil {
		return "", err
	}
	a.Hub.Register(clientID, groupName, 0, platform, maxInFlight)
	a.markPresenceFlushed(clientID, time.Now())
	token, err := a.Tokens.Issue(auth.Claims{
		Username:    clientID,
		Role:        "client",
		ClientID:    clientID,
		Group:       groupName,
		MaxInFlight: maxInFlight,
	}, 24*time.Hour)
	return token, err
}

func (a *App) SubmitClientResult(ctx context.Context, claims *auth.Claims, result rpc.JobResult) error {
	if err := a.EnsureGroupActive(ctx, claims.Group); err != nil {
		return err
	}
	a.TouchClientPresence(ctx, claims.ClientID, claims.Group, claims.UserID, claims.MaxInFlight, "", "")

	outcome, err := a.Hub.SubmitResult(claims.ClientID, result)
	if err != nil {
		if errors.Is(err, rpc.ErrResultClientMismatch) {
			log.Printf("reject mismatched result: client=%s request=%s", claims.ClientID, result.RequestID)
		}
		return err
	}

	switch {
	case outcome.Delivered:
		return nil
	case outcome.Duplicate:
		log.Printf("duplicate result ignored: client=%s request=%s", claims.ClientID, result.RequestID)
		return nil
	case outcome.Late:
		log.Printf("late result ignored after timeout: client=%s request=%s", claims.ClientID, result.RequestID)
		return nil
	default:
		return nil
	}
}

func (a *App) InvokeRPC(ctx context.Context, claims *auth.Claims, groupName, actionName string, req InvokeRequest) (rpc.JobResult, string, string, error) {
	requestID := randomID()
	groupName = strings.TrimSpace(groupName)
	actionName = strings.TrimSpace(actionName)
	if err := a.EnsureGroupActive(ctx, groupName); err != nil {
		return rpc.JobResult{}, requestID, "", err
	}
	timeout := a.Config.RequestTimeout
	if req.Timeout > 0 && req.Timeout < 120 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	requestPayload := buildStoredInvokeRequest(req)
	requestRecord := &model.RPCRequest{
		RequestID:          requestID,
		GroupName:          groupName,
		ActionName:         actionName,
		ClientID:           req.ClientID,
		RequestPayloadJSON: requestPayload,
		Status:             "pending",
		HTTPCode:           200,
	}
	if claims != nil {
		requestRecord.RequesterUserID = &claims.UserID
	}

	// 同步调用模型下不写 pending 行：完成时（成功/失败/超时）一次性落库。
	baseTask := persistTask{
		Kind:           persistTaskCompleteRequest,
		RequestID:      requestID,
		ClientID:       requestRecord.ClientID,
		GroupName:      groupName,
		ActionName:     actionName,
		HTTPCode:       requestRecord.HTTPCode,
		RequestPayload: requestPayload,
	}
	if requestRecord.RequesterUserID != nil {
		baseTask.RequesterUserID = *requestRecord.RequesterUserID
		baseTask.HasRequesterUserID = true
	}

	job := &rpc.Job{
		RequestID:  requestID,
		Group:      groupName,
		Action:     actionName,
		ClientID:   req.ClientID,
		Payload:    req.Payload,
		CreatedAt:  time.Now(),
		DeadlineAt: time.Now().Add(timeout),
	}

	invokeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, actualClientID, err := a.Hub.Invoke(invokeCtx, groupName, req.ClientID, job)
	if actualClientID != "" {
		requestRecord.ClientID = actualClientID
		baseTask.ClientID = actualClientID
	}
	if err != nil {
		status := "timeout"
		httpCode := http.StatusGatewayTimeout
		switch {
		case errors.Is(err, rpc.ErrNoOnlineClient), errors.Is(err, rpc.ErrPreferredClientDown):
			status = "no_client"
			httpCode = http.StatusBadGateway
		case errors.Is(err, rpc.ErrClientQueueFull), errors.Is(err, rpc.ErrGroupSaturated):
			status = "rejected"
			httpCode = http.StatusTooManyRequests
		}
		usedClientID := requestRecord.ClientID
		rawResponse := buildStoredInvokeResponse(requestID, groupName, actionName, usedClientID, req.Payload, rpc.JobResult{
			RequestID: requestID,
			Status:    status,
			HTTPCode:  httpCode,
			Error:     err.Error(),
		})
		now := time.Now()
		completeTask := baseTask
		completeTask.ClientID = usedClientID
		completeTask.Status = status
		completeTask.HTTPCode = httpCode
		completeTask.ResponsePayload = rawResponse
		completeTask.LatencyMS = 0
		completeTask.ErrorMessage = err.Error()
		a.enqueuePersist(completeTask)
		a.enqueuePersist(persistTask{
			Kind:       persistTaskMetric,
			StatTime:   now,
			ClientID:   usedClientID,
			GroupName:  groupName,
			ActionName: actionName,
			Status:     status,
			LatencyMS:  0,
		})
		return rpc.JobResult{}, requestID, usedClientID, err
	}

	if result.HTTPCode == 0 {
		result.HTTPCode = 200
	}
	if result.Status == "" {
		result.Status = "success"
	}

	completeTask := baseTask
	completeTask.ClientID = requestRecord.ClientID
	completeTask.Status = result.Status
	completeTask.HTTPCode = result.HTTPCode
	completeTask.ResponsePayload = buildStoredInvokeResponse(requestID, groupName, actionName, requestRecord.ClientID, req.Payload, result)
	completeTask.LatencyMS = result.LatencyMS
	completeTask.ErrorMessage = result.Error
	a.enqueuePersist(completeTask)
	a.enqueuePersist(persistTask{
		Kind:       persistTaskMetric,
		StatTime:   time.Now(),
		ClientID:   requestRecord.ClientID,
		GroupName:  groupName,
		ActionName: actionName,
		Status:     result.Status,
		LatencyMS:  result.LatencyMS,
	})
	return result, requestID, requestRecord.ClientID, nil
}

func buildStoredInvokeResponse(requestID, groupName, actionName, clientID string, requestPayload json.RawMessage, result rpc.JobResult) string {
	isOK := strings.EqualFold(result.Status, "success") && strings.TrimSpace(result.Error) == ""
	payload := map[string]any{
		"is_ok":          isOK,
		"requestId":      requestID,
		"group":          groupName,
		"action":         actionName,
		"clientId":       clientID,
		"requestPayload": jsonBodyOrObject(requestPayload),
		"status":         result.Status,
		"httpCode":       result.HTTPCode,
		"data":           jsonBodyOrObject(result.Payload),
		"latencyMs":      result.LatencyMS,
		"error":          result.Error,
	}
	if result.PayloadEncoding != "" {
		payload["payloadEncoding"] = result.PayloadEncoding
	}
	if result.PayloadRawSize > 0 {
		payload["payloadRawSize"] = result.PayloadRawSize
	}
	if result.PayloadCompressedSize > 0 {
		payload["payloadCompressedSize"] = result.PayloadCompressedSize
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return string(result.Payload)
	}
	return string(raw)
}

func buildStoredInvokeRequest(req InvokeRequest) string {
	payload := map[string]any{
		"clientId":       strings.TrimSpace(req.ClientID),
		"timeoutSeconds": req.Timeout,
		"payload":        jsonBodyOrObject(req.Payload),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return string(req.Payload)
	}
	return string(raw)
}

func jsonBodyOrObject(raw json.RawMessage) any {
	trimmed := string(raw)
	if strings.TrimSpace(trimmed) == "" {
		return map[string]any{}
	}
	return json.RawMessage(trimmed)
}

func (a *App) EnsureGroupActive(ctx context.Context, groupName string) error {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return fmt.Errorf("分组不能为空")
	}
	group, err := a.Store.GetGroupByName(ctx, groupName)
	if errors.Is(err, sql.ErrNoRows) {
		return &GroupError{Kind: ErrGroupNotFound, Group: groupName}
	}
	if err != nil {
		return err
	}
	if !group.Enabled {
		return &GroupError{Kind: ErrGroupDisabled, Group: groupName}
	}
	return nil
}

func (a *App) VerifyTokenFromRequest(r *http.Request) (*auth.Claims, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header != "" {
		const prefix = "Bearer "
		if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
			return nil, fmt.Errorf("授权头格式错误")
		}
		return a.Tokens.Parse(header[len(prefix):])
	}

	cookie, err := r.Cookie(adminAuthCookieName)
	if err == nil && strings.TrimSpace(cookie.Value) != "" {
		return a.Tokens.Parse(strings.TrimSpace(cookie.Value))
	}
	if err != nil && !errors.Is(err, http.ErrNoCookie) {
		return nil, err
	}

	return nil, fmt.Errorf("未登录或登录已过期")
}

func (a *App) RemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (a *App) MustBootstrap(ctx context.Context) error {
	if err := store.BootstrapSchema(ctx, *a.Config); err != nil {
		return err
	}
	return a.Store.EnsureBootstrapAdmin(ctx, a.Config.BootstrapAdminUser, a.Config.BootstrapAdminPass)
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func (a *App) TouchClientPresence(ctx context.Context, clientID, groupName string, userID int64, maxInFlight int, platform, ip string) {
	if clientID == "" {
		return
	}
	if groupName != "" {
		a.Hub.Register(clientID, groupName, userID, platform, maxInFlight)
	}
	a.Hub.Touch(clientID)
	if !a.shouldFlushPresence(clientID, time.Now()) {
		return
	}
	if err := a.Store.TouchDevice(ctx, clientID, ip); err != nil {
		a.resetPresenceFlush(clientID)
		log.Printf("touch device failed: client=%s err=%v", clientID, err)
	}
}

func (a *App) presenceFlushInterval() time.Duration {
	seconds := a.Config.PresenceFlushSeconds
	if seconds <= 0 {
		seconds = int(defaultPresenceFlushInterval / time.Second)
	}
	return time.Duration(seconds) * time.Second
}

func (a *App) shouldFlushPresence(clientID string, now time.Time) bool {
	interval := a.presenceFlushInterval()
	a.presenceMu.Lock()
	defer a.presenceMu.Unlock()
	if last, ok := a.lastPresenceFlush[clientID]; ok && now.Sub(last) < interval {
		return false
	}
	a.lastPresenceFlush[clientID] = now
	return true
}

func (a *App) markPresenceFlushed(clientID string, ts time.Time) {
	a.presenceMu.Lock()
	defer a.presenceMu.Unlock()
	a.lastPresenceFlush[clientID] = ts
}

func (a *App) resetPresenceFlush(clientID string) {
	a.presenceMu.Lock()
	defer a.presenceMu.Unlock()
	delete(a.lastPresenceFlush, clientID)
}

func (a *App) cleanupInterval() time.Duration {
	interval := a.deviceOfflineGrace() / 4
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	if interval > time.Minute {
		interval = time.Minute
	}
	return interval
}

func (a *App) deviceOfflineGrace() time.Duration {
	seconds := a.Config.DeviceOfflineSeconds
	if seconds <= 0 {
		if a.Config.DeviceOfflineMinutes > 0 {
			seconds = a.Config.DeviceOfflineMinutes * 60
		} else {
			seconds = 20
		}
	}
	return time.Duration(seconds) * time.Second
}

func (a *App) cleanupPresenceCache(now time.Time) {
	keepFor := a.deviceOfflineGrace() + a.presenceFlushInterval()*2
	a.presenceMu.Lock()
	defer a.presenceMu.Unlock()
	for clientID, ts := range a.lastPresenceFlush {
		if now.Sub(ts) > keepFor {
			delete(a.lastPresenceFlush, clientID)
		}
	}
}

func (a *App) enqueuePersist(task persistTask) {
	select {
	case a.persistCh <- task:
		return
	default:
	}

	go func(task persistTask) {
		timer := time.NewTimer(persistEnqueueWait)
		defer timer.Stop()
		select {
		case a.persistCh <- task:
			return
		case <-timer.C:
			if err := a.runPersistTask(task); err != nil {
				log.Printf("persist overflow fallback failed: kind=%s request=%s client=%s err=%v", task.Kind, task.RequestID, task.ClientID, err)
			}
		}
	}(task)
}

func (a *App) persistWorker(ctx context.Context, workerID int) {
	const persistBatchSize = 256

	for {
		select {
		case <-ctx.Done():
			return
		case task := <-a.persistCh:
			batch := make([]persistTask, 0, persistBatchSize)
			batch = append(batch, task)
		drainLoop:
			for len(batch) < persistBatchSize {
				select {
				case nextTask := <-a.persistCh:
					batch = append(batch, nextTask)
				default:
					break drainLoop
				}
			}
			if err := a.runPersistBatch(batch); err != nil {
				log.Printf("persist worker batch failed: worker=%d batch=%d err=%v", workerID, len(batch), err)
				if len(batch) > 1 {
					for _, item := range batch {
						if itemErr := a.runPersistTask(item); itemErr != nil {
							log.Printf("persist worker fallback failed: worker=%d kind=%s request=%s client=%s err=%v", workerID, item.Kind, item.RequestID, item.ClientID, itemErr)
						}
					}
				}
			}
		}
	}
}

func (a *App) runPersistTask(task persistTask) error {
	ctx, cancel := context.WithTimeout(context.Background(), persistTaskTimeout)
	defer cancel()

	var requesterUserID *int64
	if task.HasRequesterUserID {
		requesterUserID = &task.RequesterUserID
	}

	switch task.Kind {
	case persistTaskCompleteRequest:
		return a.Store.CompleteRPCRequest(ctx, &model.RPCRequest{
			RequestID:           task.RequestID,
			GroupName:           task.GroupName,
			ActionName:          task.ActionName,
			ClientID:            task.ClientID,
			RequesterUserID:     requesterUserID,
			RequestPayloadJSON:  task.RequestPayload,
			ResponsePayloadJSON: task.ResponsePayload,
			Status:              task.Status,
			HTTPCode:            task.HTTPCode,
			LatencyMS:           task.LatencyMS,
			ErrorMessage:        task.ErrorMessage,
		})
	case persistTaskMetric:
		return a.Store.IncrementDailyMetric(ctx, task.StatTime, task.GroupName, task.ActionName, task.ClientID, task.Status, task.LatencyMS)
	default:
		return fmt.Errorf("未知的持久化任务类型: %s", task.Kind)
	}
}

func randomID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
