package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// RuntimeState is the lifecycle state reported by the control socket.
type RuntimeState string

const (
	// RuntimeStarting means the manager is still preparing the VM.
	RuntimeStarting RuntimeState = "starting"
	// RuntimeReady means the runtime is available for control requests.
	RuntimeReady RuntimeState = "ready"
	// RuntimeSuspending means a suspend request is in progress.
	RuntimeSuspending RuntimeState = "suspending"
	// RuntimeSuspended means VM state has been saved.
	RuntimeSuspended RuntimeState = "suspended"
	// RuntimeStopping means teardown has started.
	RuntimeStopping RuntimeState = "stopping"
	// RuntimeStopped means teardown has completed.
	RuntimeStopped RuntimeState = "stopped"
)

// StatusRequest asks for the current runtime status.
type StatusRequest struct{}

// StatusResponse reports runtime status and connection paths.
type StatusResponse struct {
	State RuntimeState `json:"state"`
	CID   int          `json:"cid"`
	Paths StatusPaths  `json:"paths"`
	Stats RuntimeStats `json:"stats"`
}

// StatusPaths are host-side sockets associated with the runtime.
type StatusPaths struct {
	ControlSocket    string `json:"controlSocket"`
	QMPSocket        string `json:"qmpSocket"`
	GuestAgentSocket string `json:"guestAgentSocket,omitempty"`
	SSHReadySocket   string `json:"sshReadySocket,omitempty"`
}

// RuntimeStats reports lifecycle timing captured during launch and teardown.
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

// SuspendRequest asks the runtime to save VM state and exit.
type SuspendRequest struct{}

// SuspendResponse reports whether suspend state was saved.
type SuspendResponse struct {
	Saved       bool   `json:"saved"`
	VMStatePath string `json:"vmStatePath,omitempty"`
}

// HotplugRequest asks the runtime to attach or detach a configured device.
type HotplugRequest struct {
	ID     string `json:"id"`
	Detach bool   `json:"detach"`
}

// HotplugResponse identifies the hotplug operation that completed.
type HotplugResponse struct {
	ID     string `json:"id"`
	Detach bool   `json:"detach"`
}

// BalloonRequest asks the runtime to resize or query the memory balloon.
type BalloonRequest struct {
	TargetBytes int64 `json:"targetBytes,omitempty"`
}

// BalloonResponse reports the current and requested balloon sizes.
type BalloonResponse struct {
	ActualBytes int64 `json:"actualBytes"`
	TargetBytes int64 `json:"targetBytes,omitempty"`
}

// InfoRequest asks for guest runtime information.
type InfoRequest struct{}

// InfoResponse reports guest runtime information.
type InfoResponse struct {
	ProcessList string `json:"processList,omitempty"`
}

// ErrorCode classifies a control socket RPC failure.
type ErrorCode string

const (
	// ErrInvalidRequest means the request envelope could not be decoded.
	ErrInvalidRequest ErrorCode = "invalid_request"
	// ErrUnknownMethod means the requested RPC method is not implemented.
	ErrUnknownMethod ErrorCode = "unknown_method"
	// ErrInvalidParams means the request parameters did not match the method.
	ErrInvalidParams ErrorCode = "invalid_params"
	// ErrUnsupported means the runtime was built or configured without a capability.
	ErrUnsupported ErrorCode = "unsupported"
	// ErrFailedPrecondition means the runtime is not ready for the requested operation.
	ErrFailedPrecondition ErrorCode = "failed_precondition"
	// ErrInternal means the request failed with an unexpected internal error.
	ErrInternal ErrorCode = "internal"
)

// RPCError is the structured error returned over the control socket.
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

// RuntimeCore is the minimum runtime surface required by a control router.
type RuntimeCore interface {
	Status(context.Context, StatusRequest) (StatusResponse, error)
	Info(context.Context, InfoRequest) (InfoResponse, error)
}

// RuntimeSuspend is implemented by runtimes that can save VM state.
type RuntimeSuspend interface {
	Suspend(context.Context, SuspendRequest) (SuspendResponse, error)
}

// RuntimeHotplug is implemented by runtimes that can attach and detach devices.
type RuntimeHotplug interface {
	Hotplug(context.Context, HotplugRequest) (HotplugResponse, error)
}

// RuntimeBalloon is implemented by runtimes that can control a memory balloon.
type RuntimeBalloon interface {
	Balloon(context.Context, BalloonRequest) (BalloonResponse, error)
}

// Router dispatches typed control socket requests to runtime capabilities.
type Router struct {
	core    RuntimeCore
	suspend RuntimeSuspend
	hotplug RuntimeHotplug
	balloon RuntimeBalloon
}

// NewRouter creates a router with only core status and info methods.
func NewRouter(core RuntimeCore) (*Router, error) {
	if core == nil {
		return nil, fmt.Errorf("core handler is required")
	}
	return &Router{core: core}, nil
}

// NewRuntimeRouter creates a router from the capabilities implemented by runtime.
func NewRuntimeRouter(runtime any) (*Router, error) {
	core, ok := runtime.(RuntimeCore)
	if !ok {
		return nil, fmt.Errorf("runtime core handler is required")
	}
	router := &Router{core: core}
	router.suspend, _ = runtime.(RuntimeSuspend)
	router.hotplug, _ = runtime.(RuntimeHotplug)
	router.balloon, _ = runtime.(RuntimeBalloon)
	return router, nil
}

func (r *Router) handle(ctx context.Context, req requestEnvelope) responseEnvelope {
	var result any
	var err error

	switch req.Method {
	case rpcStatus:
		var params StatusRequest
		if err = decodeParams(req.Params, &params); err == nil {
			result, err = r.core.Status(ctx, params)
		}
	case rpcInfo:
		var params InfoRequest
		if err = decodeParams(req.Params, &params); err == nil {
			result, err = r.core.Info(ctx, params)
		}
	case rpcSuspend:
		if r.suspend == nil {
			err = &RPCError{Code: ErrUnsupported, Message: "suspend is not supported"}
			break
		}
		var params SuspendRequest
		if err = decodeParams(req.Params, &params); err == nil {
			result, err = r.suspend.Suspend(ctx, params)
		}
	case rpcHotplug:
		if r.hotplug == nil {
			err = &RPCError{Code: ErrUnsupported, Message: "hotplug is not supported"}
			break
		}
		var params HotplugRequest
		if err = decodeParams(req.Params, &params); err == nil {
			result, err = r.hotplug.Hotplug(ctx, params)
		}
	case rpcBalloon:
		if r.balloon == nil {
			err = &RPCError{Code: ErrUnsupported, Message: "balloon is not supported"}
			break
		}
		var params BalloonRequest
		if err = decodeParams(req.Params, &params); err == nil {
			result, err = r.balloon.Balloon(ctx, params)
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

// Server serves control socket requests for a router.
type Server struct {
	handler  *Router
	listener net.Listener
	done     chan struct{}
}

// NewServer returns a closable control server for router.
func NewServer(h *Router) (*Server, error) {
	if h == nil {
		return nil, fmt.Errorf("control handler is required")
	}
	return &Server{handler: h}, nil
}

// Listen opens a private Unix socket at path for control requests.
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

// Serve handles control requests from l until the listener closes.
func Serve(l net.Listener, h *Router) error {
	server, err := NewServer(h)
	if err != nil {
		return err
	}
	return server.Serve(l)
}

// ListenAndServe opens path and serves control requests for h.
func ListenAndServe(path string, h *Router) error {
	listener, err := Listen(path)
	if err != nil {
		return err
	}
	return Serve(listener, h)
}

// Serve handles control requests from l until the listener closes.
func (s *Server) Serve(l net.Listener) error {
	if s.handler == nil {
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

// Close stops accepting new control socket connections.
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
	resp := s.handler.handle(context.Background(), req)
	writeResponse(conn, resp)
}

func writeResponse(conn net.Conn, resp responseEnvelope) {
	_ = json.NewEncoder(conn).Encode(resp)
}

// Client sends typed requests to a control socket.
type Client struct {
	dial func(context.Context) (net.Conn, error)
}

// Dial returns a client for the control socket at path.
func Dial(path string) *Client {
	return &Client{dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}
}

// Status sends a status request.
func (c *Client) Status(ctx context.Context, req StatusRequest) (StatusResponse, error) {
	var resp StatusResponse
	err := c.call(ctx, rpcStatus, req, &resp)
	return resp, err
}

// Suspend sends a suspend request.
func (c *Client) Suspend(ctx context.Context, req SuspendRequest) (SuspendResponse, error) {
	var resp SuspendResponse
	err := c.call(ctx, rpcSuspend, req, &resp)
	return resp, err
}

// Hotplug sends a hotplug request.
func (c *Client) Hotplug(ctx context.Context, req HotplugRequest) (HotplugResponse, error) {
	var resp HotplugResponse
	err := c.call(ctx, rpcHotplug, req, &resp)
	return resp, err
}

// Balloon sends a balloon request.
func (c *Client) Balloon(ctx context.Context, req BalloonRequest) (BalloonResponse, error) {
	var resp BalloonResponse
	err := c.call(ctx, rpcBalloon, req, &resp)
	return resp, err
}

// Info sends an info request.
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
