package manager

import control "github.com/shazow/agentspace/virtie/internal/manager/control"

type RuntimeState = control.RuntimeState

const (
	RuntimeStarting   = control.RuntimeStarting
	RuntimeReady      = control.RuntimeReady
	RuntimeSuspending = control.RuntimeSuspending
	RuntimeSuspended  = control.RuntimeSuspended
	RuntimeStopping   = control.RuntimeStopping
	RuntimeStopped    = control.RuntimeStopped
)

type StatusRequest = control.StatusRequest
type StatusResponse = control.StatusResponse
type StatusPaths = control.StatusPaths
type RuntimeStats = control.RuntimeStats
type SuspendRequest = control.SuspendRequest
type SuspendResponse = control.SuspendResponse
type HotplugRequest = control.HotplugRequest
type HotplugResponse = control.HotplugResponse
type BalloonRequest = control.BalloonRequest
type BalloonResponse = control.BalloonResponse
type InfoRequest = control.InfoRequest
type InfoResponse = control.InfoResponse
type ErrorCode = control.ErrorCode

const (
	ErrInvalidRequest     = control.ErrInvalidRequest
	ErrUnknownMethod      = control.ErrUnknownMethod
	ErrInvalidParams      = control.ErrInvalidParams
	ErrUnsupported        = control.ErrUnsupported
	ErrFailedPrecondition = control.ErrFailedPrecondition
	ErrInternal           = control.ErrInternal
)

type RPCError = control.RPCError
type RuntimeCore = control.RuntimeCore
type RuntimeSuspend = control.RuntimeSuspend
type RuntimeHotplug = control.RuntimeHotplug
type RuntimeBalloon = control.RuntimeBalloon
type Router = control.Router
type Server = control.Server
type Client = control.Client

var NewRouter = control.NewRouter
var NewRuntimeRouter = control.NewRuntimeRouter
var Listen = control.Listen
var Serve = control.Serve
var ListenAndServe = control.ListenAndServe
var Dial = control.Dial
