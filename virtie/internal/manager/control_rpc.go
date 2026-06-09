package manager

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"
)

type RuntimeState string

const (
	RuntimeStarting   RuntimeState = "starting"
	RuntimeReady      RuntimeState = "ready"
	RuntimeSuspending RuntimeState = "suspending"
	RuntimeSuspended  RuntimeState = "suspended"
	RuntimeStopping   RuntimeState = "stopping"
	RuntimeStopped    RuntimeState = "stopped"
)

type StatusRequest struct{}

type StatusResponse struct {
	State RuntimeState `json:"state"`
	CID   int          `json:"cid"`
	Paths StatusPaths  `json:"paths"`
	Stats RuntimeStats `json:"stats"`
}

type StatusPaths struct {
	ControlSocket    string `json:"controlSocket"`
	QMPSocket        string `json:"qmpSocket"`
	GuestAgentSocket string `json:"guestAgentSocket,omitempty"`
	SSHReadySocket   string `json:"sshReadySocket,omitempty"`
}

type RuntimeStats struct {
	StartedAt       time.Time `json:"startedAt,omitempty"`
	BootStartedAt   time.Time `json:"bootStartedAt,omitempty"`
	QMPReadyAt      time.Time `json:"qmpReadyAt,omitempty"`
	FilesReadyAt    time.Time `json:"filesReadyAt,omitempty"`
	SSHReadyAt      time.Time `json:"sshReadyAt,omitempty"`
	SSHStartedAt    time.Time `json:"sshStartedAt,omitempty"`
	CompletedAt     time.Time `json:"completedAt,omitempty"`
	SSHAttempts     int       `json:"sshAttempts,omitempty"`
	StartedToBoot   string    `json:"startedToBoot,omitempty"`
	BootToQMP       string    `json:"bootToQMP,omitempty"`
	FilesToSSH      string    `json:"filesToSSH,omitempty"`
	BootToCompleted string    `json:"bootToCompleted,omitempty"`
	Total           string    `json:"total,omitempty"`
}

type SuspendRequest struct{}

type SuspendResponse struct {
	Saved       bool   `json:"saved"`
	VMStatePath string `json:"vmStatePath,omitempty"`
}

type HotplugRequest struct {
	ID     string `json:"id"`
	Detach bool   `json:"detach"`
}

type HotplugResponse struct {
	ID     string `json:"id"`
	Detach bool   `json:"detach"`
}

type BalloonRequest struct {
	TargetBytes int64 `json:"targetBytes,omitempty"`
}

type BalloonResponse struct {
	ActualBytes int64 `json:"actualBytes"`
	TargetBytes int64 `json:"targetBytes,omitempty"`
}

type InfoRequest struct{}

type InfoResponse struct {
	ProcessList string `json:"processList,omitempty"`
}

type ErrorCode string

const (
	ErrInvalidRequest     ErrorCode = "invalid_request"
	ErrUnknownMethod      ErrorCode = "unknown_method"
	ErrInvalidParams      ErrorCode = "invalid_params"
	ErrUnsupported        ErrorCode = "unsupported"
	ErrFailedPrecondition ErrorCode = "failed_precondition"
	ErrInternal           ErrorCode = "internal"
)

type RPCError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type rpcMethod string

const (
	rpcStatus  rpcMethod = "status"
	rpcSuspend rpcMethod = "suspend"
	rpcHotplug rpcMethod = "hotplug"
	rpcBalloon rpcMethod = "balloon"
	rpcInfo    rpcMethod = "info"
)

type requestEnvelope struct {
	ID     int             `json:"id"`
	Method rpcMethod       `json:"method"`
	Params json.RawMessage `json:"params"`
}

type responseEnvelope struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type RuntimeCore interface {
	Status(context.Context, StatusRequest) (StatusResponse, error)
	Info(context.Context, InfoRequest) (InfoResponse, error)
}

type RuntimeSuspend interface {
	Suspend(context.Context, SuspendRequest) (SuspendResponse, error)
}

type RuntimeHotplug interface {
	Hotplug(context.Context, HotplugRequest) (HotplugResponse, error)
}

type RuntimeBalloon interface {
	Balloon(context.Context, BalloonRequest) (BalloonResponse, error)
}

type Router struct {
	Core    RuntimeCore
	Suspend RuntimeSuspend
	Hotplug RuntimeHotplug
	Balloon RuntimeBalloon
}

func NewRouter(core RuntimeCore) (*Router, error) {
	if core == nil {
		return nil, fmt.Errorf("core handler is required")
	}
	return &Router{Core: core}, nil
}

func NewRuntimeRouter(runtime any) (*Router, error) {
	core, ok := runtime.(RuntimeCore)
	if !ok {
		return nil, fmt.Errorf("runtime core handler is required")
	}
	router := &Router{Core: core}
	router.Suspend, _ = runtime.(RuntimeSuspend)
	router.Hotplug, _ = runtime.(RuntimeHotplug)
	router.Balloon, _ = runtime.(RuntimeBalloon)
	return router, nil
}

func (r *Router) Handle(ctx context.Context, req requestEnvelope) responseEnvelope {
	var result any
	var err error

	switch req.Method {
	case rpcStatus:
		var params StatusRequest
		if err = decodeParams(req.Params, &params); err == nil {
			result, err = r.Core.Status(ctx, params)
		}
	case rpcInfo:
		var params InfoRequest
		if err = decodeParams(req.Params, &params); err == nil {
			result, err = r.Core.Info(ctx, params)
		}
	case rpcSuspend:
		if r.Suspend == nil {
			err = &RPCError{Code: ErrUnsupported, Message: "suspend is not supported"}
			break
		}
		var params SuspendRequest
		if err = decodeParams(req.Params, &params); err == nil {
			result, err = r.Suspend.Suspend(ctx, params)
		}
	case rpcHotplug:
		if r.Hotplug == nil {
			err = &RPCError{Code: ErrUnsupported, Message: "hotplug is not supported"}
			break
		}
		var params HotplugRequest
		if err = decodeParams(req.Params, &params); err == nil {
			result, err = r.Hotplug.Hotplug(ctx, params)
		}
	case rpcBalloon:
		if r.Balloon == nil {
			err = &RPCError{Code: ErrUnsupported, Message: "balloon is not supported"}
			break
		}
		var params BalloonRequest
		if err = decodeParams(req.Params, &params); err == nil {
			result, err = r.Balloon.Balloon(ctx, params)
		}
	default:
		err = &RPCError{Code: ErrUnknownMethod, Message: fmt.Sprintf("unknown method %q", req.Method)}
	}

	resp := responseEnvelope{ID: req.ID}
	if err != nil {
		resp.Error = rpcError(err)
		return resp
	}
	payload, err := json.Marshal(result)
	if err != nil {
		resp.Error = &RPCError{Code: ErrInternal, Message: err.Error()}
		return resp
	}
	resp.Result = payload
	return resp
}

func decodeParams(data json.RawMessage, dst any) error {
	if len(data) == 0 {
		data = []byte("{}")
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return &RPCError{Code: ErrInvalidParams, Message: err.Error()}
	}
	return nil
}

func rpcError(err error) *RPCError {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	return &RPCError{Code: ErrInternal, Message: err.Error()}
}

type Server struct {
	Handler  *Router
	Logger   *slog.Logger
	listener net.Listener
	done     chan struct{}
}

func Listen(path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

func Serve(l net.Listener, h *Router) error {
	return (&Server{Handler: h}).Serve(l)
}

func ListenAndServe(path string, h *Router) error {
	listener, err := Listen(path)
	if err != nil {
		return err
	}
	return Serve(listener, h)
}

func (s *Server) Serve(l net.Listener) error {
	if s.Handler == nil {
		return fmt.Errorf("control handler is required")
	}
	s.listener = l
	s.done = make(chan struct{})
	defer close(s.done)
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *Server) Close() error {
	if s == nil || s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	var req requestEnvelope
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		writeResponse(conn, responseEnvelope{Error: &RPCError{Code: ErrInvalidRequest, Message: err.Error()}})
		return
	}
	resp := s.Handler.Handle(context.Background(), req)
	writeResponse(conn, resp)
}

func writeResponse(conn net.Conn, resp responseEnvelope) {
	_ = json.NewEncoder(conn).Encode(resp)
}

type Client struct {
	dial func(context.Context) (net.Conn, error)
}

func Dial(path string) *Client {
	return &Client{dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}
}

func (c *Client) Status(ctx context.Context, req StatusRequest) (StatusResponse, error) {
	var resp StatusResponse
	err := c.call(ctx, rpcStatus, req, &resp)
	return resp, err
}

func (c *Client) Suspend(ctx context.Context, req SuspendRequest) (SuspendResponse, error) {
	var resp SuspendResponse
	err := c.call(ctx, rpcSuspend, req, &resp)
	return resp, err
}

func (c *Client) Hotplug(ctx context.Context, req HotplugRequest) (HotplugResponse, error) {
	var resp HotplugResponse
	err := c.call(ctx, rpcHotplug, req, &resp)
	return resp, err
}

func (c *Client) Balloon(ctx context.Context, req BalloonRequest) (BalloonResponse, error) {
	var resp BalloonResponse
	err := c.call(ctx, rpcBalloon, req, &resp)
	return resp, err
}

func (c *Client) Info(ctx context.Context, req InfoRequest) (InfoResponse, error) {
	var resp InfoResponse
	err := c.call(ctx, rpcInfo, req, &resp)
	return resp, err
}

func (c *Client) call(ctx context.Context, method rpcMethod, params any, result any) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return fmt.Errorf("control dial: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req := requestEnvelope{ID: 1, Method: method, Params: payload}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("control request: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("control response: %w", err)
	}
	var resp responseEnvelope
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("control response: %w", err)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Result, result); err != nil {
		return fmt.Errorf("control result: %w", err)
	}
	return nil
}
