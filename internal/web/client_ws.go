package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"r1rpc/internal/auth"
	"r1rpc/internal/rpc"
)

const wsMaxMessageBytes = 4 << 20

type wsEnvelope struct {
	Type        string         `json:"type"`
	Job         *rpc.Job       `json:"job,omitempty"`
	Result      *rpc.JobResult `json:"result,omitempty"`
	RequestID   string         `json:"requestId,omitempty"`
	OK          bool           `json:"ok,omitempty"`
	State       string         `json:"state,omitempty"`
	Error       string         `json:"error,omitempty"`
	ClientID    string         `json:"clientId,omitempty"`
	Group       string         `json:"group,omitempty"`
	ServerID    string         `json:"serverId,omitempty"`
	Time        string         `json:"time,omitempty"`
	MaxInFlight int            `json:"maxInFlight,omitempty"`
}

type clientWSSessions struct {
	mu     sync.Mutex
	nextID uint64
	items  map[string]*clientWSBinding
}

type clientWSBinding struct {
	connID uint64
	cancel context.CancelFunc
	conn   *websocket.Conn
}

func newClientWSSessions() *clientWSSessions {
	return &clientWSSessions{items: map[string]*clientWSBinding{}}
}

func (m *clientWSSessions) replace(clientID string, conn *websocket.Conn, cancel context.CancelFunc) uint64 {
	m.mu.Lock()
	m.nextID++
	connID := m.nextID
	previous := m.items[clientID]
	m.items[clientID] = &clientWSBinding{connID: connID, cancel: cancel, conn: conn}
	m.mu.Unlock()

	if previous != nil {
		previous.cancel()
		_ = previous.conn.CloseNow()
	}
	return connID
}

func (m *clientWSSessions) clearIfCurrent(clientID string, connID uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.items[clientID]
	if !ok || current.connID != connID {
		return false
	}
	delete(m.items, clientID)
	return true
}

func (s *Server) handleClientWS(w http.ResponseWriter, r *http.Request) {
	claims, err := s.verifyClientWSClaims(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if err := s.App.EnsureGroupActive(r.Context(), claims.Group); err != nil {
		if status, ok := groupErrorHTTPStatus(err); ok {
			writeError(w, status, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		// Accept 失败时已自行写过响应，这里无需再写。
		return
	}
	conn.SetReadLimit(int64(wsMaxMessageBytes))

	ip := s.App.RemoteIP(r)
	session := s.App.Hub.Register(claims.ClientID, claims.Group, claims.UserID, "websocket", claims.MaxInFlight)
	s.App.TouchClientPresence(context.Background(), claims.ClientID, claims.Group, claims.UserID, claims.MaxInFlight, "websocket", ip)

	ctx, cancel := context.WithCancel(context.Background())
	connID := s.wsClients.replace(claims.ClientID, conn, cancel)

	defer func() {
		cancel()
		_ = conn.CloseNow()
		if s.wsClients.clearIfCurrent(claims.ClientID, connID) {
			s.App.Hub.Unregister(claims.ClientID)
		}
	}()

	if err := writeWSJSON(ctx, conn, wsEnvelope{
		Type:        "welcome",
		ClientID:    claims.ClientID,
		Group:       claims.Group,
		ServerID:    s.App.Config.ServerID,
		Time:        time.Now().Format(time.RFC3339),
		MaxInFlight: session.MaxInFlight,
	}); err != nil {
		return
	}

	// 对客户端的回包（ack/heartbeatAck，均为非关键消息，客户端本就忽略）走独立的异步
	// 发送协程，绝不让读循环阻塞在写上——读循环一旦被写阻塞，就读不到客户端的心跳/pong，
	// 等于 RPC 数据流干扰了保活。这正是要隔离的：心跳归心跳，RPC 不能拖累保活。
	outCh := make(chan wsEnvelope, 256)
	senderDone := make(chan struct{})
	go func() {
		defer close(senderDone)
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-outCh:
				_ = writeWSJSONTimeout(ctx, conn, msg, 5*time.Second)
			}
		}
	}()

	writerDone := make(chan error, 1)
	go func() {
		writerDone <- s.clientWSWriterLoop(ctx, claims, conn)
	}()
	// ping/pong 是唯一的保活判定，完全独立于 RPC 应用消息；pong 超时即主动拆连接。
	pingDone := make(chan error, 1)
	go func() {
		pingDone <- s.clientWSPingLoop(ctx, conn, claims.ClientID, cancel)
	}()

	readerErr := s.clientWSReaderLoop(ctx, claims, conn, ip, outCh)
	cancel()
	writerErr := <-writerDone
	pingErr := <-pingDone
	<-senderDone
	if readerErr != nil && !isExpectedWSError(readerErr) {
		fmt.Printf("client ws reader closed: client=%s err=%v\n", claims.ClientID, readerErr)
	}
	if writerErr != nil && !isExpectedWSError(writerErr) {
		fmt.Printf("client ws writer closed: client=%s err=%v\n", claims.ClientID, writerErr)
	}
	if pingErr != nil && !isExpectedWSError(pingErr) {
		fmt.Printf("client ws ping closed: client=%s err=%v\n", claims.ClientID, pingErr)
	}
}

// writeWSJSON 序列化并写一条文本帧，带 15s 写超时。
func writeWSJSON(ctx context.Context, conn *websocket.Conn, payload wsEnvelope) error {
	return writeWSJSONTimeout(ctx, conn, payload, 15*time.Second)
}

// writeWSJSONTimeout 同 writeWSJSON，但可指定写超时（异步回包用更短超时，避免长时间占用写锁影响 ping）。
func writeWSJSONTimeout(ctx context.Context, conn *websocket.Conn, payload wsEnvelope, timeout time.Duration) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

// enqueueClientReply 把对客户端的回包投给异步发送协程；队列满则丢弃。
// 这些回包（resultAck/heartbeatAck）客户端本就忽略，且结果早已通过 SubmitClientResult 交付给调用方，
// 丢弃不影响正确性。关键是：绝不阻塞读循环——读被写拖住 = 读不到心跳/pong = 保活被 RPC 干扰。
func enqueueClientReply(outCh chan<- wsEnvelope, msg wsEnvelope) {
	select {
	case outCh <- msg:
	default:
	}
}

func (s *Server) clientWSWriterLoop(ctx context.Context, claims *auth.Claims, conn *websocket.Conn) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		acquiredSession, err := s.App.Hub.AcquireDispatchSlot(ctx, claims.ClientID)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		job, err := acquiredSession.Pending.Pop(ctx)
		if err != nil {
			s.App.Hub.ReleaseDispatchSlot(claims.ClientID)
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if job == nil {
			s.App.Hub.ReleaseDispatchSlot(claims.ClientID)
			continue
		}
		if ctx.Err() != nil {
			s.App.Hub.ReleaseDispatchSlot(claims.ClientID)
			_ = s.App.Hub.Requeue(claims.ClientID, job)
			return nil
		}
		if err := writeWSJSON(ctx, conn, wsEnvelope{Type: "job", Job: job}); err != nil {
			s.App.Hub.ReleaseDispatchSlot(claims.ClientID)
			_ = s.App.Hub.Requeue(claims.ClientID, job)
			return err
		}
	}
}

func (s *Server) clientHeartbeatTimeout() time.Duration {
	seconds := s.App.Config.DeviceOfflineSeconds
	if seconds <= 0 {
		if s.App.Config.DeviceOfflineMinutes > 0 {
			seconds = s.App.Config.DeviceOfflineMinutes * 60
		} else {
			seconds = 20
		}
	}
	return time.Duration(seconds) * time.Second
}

func (s *Server) clientWSPingInterval() time.Duration {
	seconds := s.App.Config.HeartbeatIntervalSeconds
	if seconds <= 0 {
		seconds = 5
	}
	return time.Duration(seconds) * time.Second
}

// clientWSPingLoop 是连接唯一的保活判定：每 interval 发一次 WS ping，
// 在 pongTimeout 内收不到 pong 即认定链路已死，调用 onDead 主动拆连接。
// 与 RPC 应用消息完全解耦——再大的调用流量也不会让它误判离线。
func (s *Server) clientWSPingLoop(ctx context.Context, conn *websocket.Conn, clientID string, onDead func()) error {
	ticker := time.NewTicker(s.clientWSPingInterval())
	defer ticker.Stop()

	pongTimeout := s.clientHeartbeatTimeout()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, pongTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				onDead() // ping/pong 失败 = 链路真死 → 主动拆连接（之前这里的错误没人理，连接不会被拆）
				return err
			}
			// ping 成功 = 链路活着 → 刷新在线时间。让"在线"判定脱离 RPC 应用消息：
			// 即便大流量把心跳/结果挤住，WS 保活本身就维持设备在线，不再误判离线。
			s.App.Hub.Touch(clientID)
		}
	}
}

func (s *Server) clientWSReaderLoop(ctx context.Context, claims *auth.Claims, conn *websocket.Conn, ip string, outCh chan<- wsEnvelope) error {
	for {
		// 不再用"应用消息读超时"判活：判活交给 ping/pong（clientWSPingLoop）。
		// 这样再大的 RPC 数据流量/拥塞都不会误杀健康连接——保活与调用彻底解耦。
		msgType, payload, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if msgType != websocket.MessageText {
			continue
		}
		s.App.TouchClientPresence(context.Background(), claims.ClientID, claims.Group, claims.UserID, claims.MaxInFlight, "", ip)

		var envelope wsEnvelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			enqueueClientReply(outCh, wsEnvelope{Type: "error", Error: "invalid json payload"})
			continue
		}
		switch envelope.Type {
		case "heartbeat":
			enqueueClientReply(outCh, wsEnvelope{Type: "heartbeatAck", Time: time.Now().Format(time.RFC3339)})
		case "result":
			if envelope.Result == nil {
				enqueueClientReply(outCh, wsEnvelope{Type: "resultAck", OK: false, Error: "result payload required"})
				continue
			}
			if strings.TrimSpace(envelope.Result.Status) == "" {
				envelope.Result.Status = "success"
			}
			if err := normalizeClientJobResult(envelope.Result); err != nil {
				enqueueClientReply(outCh, wsEnvelope{Type: "resultAck", RequestID: envelope.Result.RequestID, OK: false, Error: err.Error(), State: "rejected"})
				continue
			}
			submitErr := s.App.SubmitClientResult(context.Background(), claims, *envelope.Result)
			ack := wsEnvelope{Type: "resultAck", RequestID: envelope.Result.RequestID, OK: submitErr == nil}
			if submitErr != nil {
				ack.Error = submitErr.Error()
				if errors.Is(submitErr, rpc.ErrResultClientMismatch) {
					ack.State = "rejected"
				} else {
					ack.State = "error"
				}
			} else {
				ack.State = "accepted"
			}
			enqueueClientReply(outCh, ack)
		default:
			enqueueClientReply(outCh, wsEnvelope{Type: "error", Error: "unsupported message type"})
		}
	}
}

func (s *Server) verifyClientWSClaims(r *http.Request) (*auth.Claims, error) {
	var (
		claims *auth.Claims
		err    error
	)
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		claims, err = s.App.Tokens.Parse(token)
	} else {
		claims, err = s.App.VerifyTokenFromRequest(r)
	}
	if err != nil {
		return nil, err
	}
	if claims.Role != "client" {
		return nil, fmt.Errorf("权限不足")
	}
	return claims, nil
}

func (s *Server) clientWSURL(r *http.Request, token string) string {
	scheme := "ws"
	forwardedProto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	if forwardedProto == "https" || r.TLS != nil {
		scheme = "wss"
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("%s://%s/api/client/ws?token=%s", scheme, host, url.QueryEscape(token))
}

func isExpectedWSError(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusNoStatusRcvd, websocket.StatusAbnormalClosure:
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "use of closed network connection") ||
		strings.Contains(message, "eof") ||
		strings.Contains(message, "broken pipe")
}
