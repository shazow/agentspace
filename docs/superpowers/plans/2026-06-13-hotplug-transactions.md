# Hotplug Transactions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make hotplug a deep but removable feature module while preserving the consumer `virtie hotplug` commandline behavior.

**Architecture:** Control routing will register optional feature handlers explicitly instead of discovering hotplug on the runtime core. Runtime will expose generic lifecycle, QMP, and control facilities; manager will assemble hotplug at the feature periphery. The hotplug module will own attach/detach transactions, typed QMP device operations, guest mount intent, state, and rollback.

**Tech Stack:** Go 1.x, standard `testing`, internal `qmpclient`, `qga`, `executor`, `manifest`, `manager/control`, `manager/runtime`, and `hotplugtypes` packages.

---

## File Structure

- Modify `virtie/internal/manager/control/control_rpc.go`: replace capability auto-discovery with explicit `RouterOption` registration.
- Modify `virtie/internal/manager/control/control_rpc_transport_test.go`: prove hotplug is unsupported unless registered explicitly.
- Modify `virtie/internal/manager/runtime/control_server.go`: start a control server from an explicit router.
- Modify `virtie/internal/manager/runtime/control_server_test.go`: build routers explicitly in tests.
- Modify `virtie/internal/manager/runtime/concrete.go`: remove hotplug-named fields from runtime core and register only generic runtime handlers.
- Modify `virtie/internal/manager/runtime/dependencies.go`: remove hotplug-named dependency fields.
- Modify `virtie/internal/manager/runtime/api_surface_test.go`: add an isolation test that rejects hotplug-named runtime dependencies.
- Delete `virtie/internal/manager/runtime/concrete_hotplug.go`: runtime core will not implement hotplug.
- Delete `virtie/internal/manager/runtime/hotplug.go`: hotplug QMP adapter moves to the hotplug feature module.
- Delete `virtie/internal/manager/runtime/hotplug_test.go`: replacement coverage lives in hotplug and manager tests.
- Modify `virtie/internal/hotplug/hotplug.go`: replace exported runtime shape with transaction runner semantics.
- Create `virtie/internal/hotplug/qmp_adapter.go`: typed QMP adapter for hotplug device operations.
- Modify `virtie/internal/hotplug/hotplug_test.go`: test transactions through typed adapters, not raw QMP strings at the caller seam.
- Modify `virtie/internal/manager/hotplug.go`: keep commandline flow, but build a hotplug runner instead of exposing a runtime-shaped hotplug module.
- Create `virtie/internal/manager/hotplug_feature.go`: assemble manager-side hotplug adapters and runtime control handler.
- Modify `virtie/internal/manager/manager.go`: register hotplug handler when starting the runtime control server.
- Modify `virtie/internal/manager/manager_test.go`: keep commandline behavior tests and remove duplicate transaction expectations where hotplug package coverage is stronger.

---

### Task 1: Make Control Capabilities Explicit

**Files:**
- Modify: `virtie/internal/manager/control/control_rpc.go`
- Modify: `virtie/internal/manager/control/control_rpc_transport_test.go`

- [ ] **Step 1: Write the failing control-router test**

Add this test to `virtie/internal/manager/control/control_rpc_transport_test.go` after `TestControlRouterUnsupportedCapability`:

```go
func TestControlRouterRequiresExplicitHotplugRegistration(t *testing.T) {
	handler := &fakeControlHandler{}
	router, err := NewRouter(handler)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	serverPath := filepath.Join(t.TempDir(), "virtie.sock")
	startTestControlRouterAt(t, serverPath, router)

	_, err = Dial(serverPath).Hotplug(context.Background(), HotplugRequest{ID: "disk0"})
	var rpcErr *RPCError
	if err == nil || !errors.As(err, &rpcErr) || rpcErr.Code != ErrUnsupported {
		t.Fatalf("expected unregistered hotplug to be unsupported, got %v", err)
	}

	router, err = NewRouter(handler, WithHotplug(handler))
	if err != nil {
		t.Fatalf("router with hotplug: %v", err)
	}
	registeredPath := filepath.Join(t.TempDir(), "virtie.sock")
	startTestControlRouterAt(t, registeredPath, router)

	resp, err := Dial(registeredPath).Hotplug(context.Background(), HotplugRequest{ID: "disk0", Detach: true})
	if err != nil {
		t.Fatalf("registered hotplug: %v", err)
	}
	if resp.ID != "disk0" || !resp.Detach {
		t.Fatalf("unexpected hotplug response: %#v", resp)
	}
}
```

Change the test helper at the bottom of the same file to support explicit routers:

```go
func startTestControlServer(t *testing.T, runtime any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "virtie.sock")

	core, ok := runtime.(RuntimeCore)
	if !ok {
		t.Fatalf("runtime core handler is required")
	}
	options := []RouterOption{}
	if suspend, ok := runtime.(RuntimeSuspend); ok {
		options = append(options, WithSuspend(suspend))
	}
	if hotplug, ok := runtime.(RuntimeHotplug); ok {
		options = append(options, WithHotplug(hotplug))
	}
	if balloon, ok := runtime.(RuntimeBalloon); ok {
		options = append(options, WithBalloon(balloon))
	}
	router, err := NewRouter(core, options...)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	startTestControlRouterAt(t, path, router)
	return path
}

func startTestControlRouterAt(t *testing.T, path string, router *Router) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create control socket directory: %v", err)
	}
	listener, err := Listen(path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server, err := NewServer(router)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		if err := listener.Close(); err != nil && !strings.Contains(err.Error(), "use of closed") {
			t.Errorf("close server: %v", err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
}
```

- [ ] **Step 2: Run the focused failing test**

Run:

```bash
cd virtie && go test ./internal/manager/control -run TestControlRouterRequiresExplicitHotplugRegistration -count=1
```

Expected: FAIL because `RouterOption`, `WithHotplug`, and the variadic `NewRouter` do not exist.

- [ ] **Step 3: Implement explicit router options**

In `virtie/internal/manager/control/control_rpc.go`, replace `NewRouter` and `NewRuntimeRouter` with this implementation:

```go
// RouterOption registers an optional control method handler.
type RouterOption func(*Router)

// WithSuspend registers suspend handling for a router.
func WithSuspend(handler RuntimeSuspend) RouterOption {
	return func(router *Router) {
		router.suspend = handler
	}
}

// WithHotplug registers hotplug handling for a router.
func WithHotplug(handler RuntimeHotplug) RouterOption {
	return func(router *Router) {
		router.hotplug = handler
	}
}

// WithBalloon registers balloon handling for a router.
func WithBalloon(handler RuntimeBalloon) RouterOption {
	return func(router *Router) {
		router.balloon = handler
	}
}

// NewRouter creates a router with core status and info methods plus explicit optional handlers.
func NewRouter(core RuntimeCore, options ...RouterOption) (*Router, error) {
	if core == nil {
		return nil, fmt.Errorf("core handler is required")
	}
	router := &Router{core: core}
	for _, option := range options {
		if option != nil {
			option(router)
		}
	}
	return router, nil
}
```

Remove the old `NewRuntimeRouter` function.

- [ ] **Step 4: Run control package tests**

Run:

```bash
cd virtie && go test ./internal/manager/control -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add virtie/internal/manager/control/control_rpc.go virtie/internal/manager/control/control_rpc_transport_test.go
git commit -m "virtie: Make control capabilities explicit" \
  -m "Control routing now registers optional handlers directly instead of discovering feature methods on the runtime core." \
  -m "Validation performed:
- cd virtie && go test ./internal/manager/control -count=1" \
  -m "Assisted-by: codex:gpt-5"
```

---

### Task 2: Remove Hotplug From Runtime Core

**Files:**
- Modify: `virtie/internal/manager/runtime/control_server.go`
- Modify: `virtie/internal/manager/runtime/control_server_test.go`
- Modify: `virtie/internal/manager/runtime/concrete.go`
- Modify: `virtie/internal/manager/runtime/dependencies.go`
- Modify: `virtie/internal/manager/runtime/api_surface_test.go`
- Delete: `virtie/internal/manager/runtime/concrete_hotplug.go`
- Delete: `virtie/internal/manager/runtime/hotplug.go`
- Delete: `virtie/internal/manager/runtime/hotplug_test.go`
- Modify callers in: `virtie/internal/manager/manager.go`

- [ ] **Step 1: Write the failing runtime isolation test**

Add imports to `virtie/internal/manager/runtime/api_surface_test.go`:

```go
import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)
```

Add this test after `TestRuntimePackageDoesNotExportConstructionAndTeardownInternals`:

```go
func TestRuntimeDependenciesDoNotNameHotplug(t *testing.T) {
	depsType := reflect.TypeOf(Dependencies{})
	for i := 0; i < depsType.NumField(); i++ {
		field := depsType.Field(i)
		if strings.Contains(strings.ToLower(field.Name), "hotplug") {
			t.Fatalf("runtime dependency %s embeds hotplug feature policy in runtime core", field.Name)
		}
	}
}
```

- [ ] **Step 2: Run the focused failing test**

Run:

```bash
cd virtie && go test ./internal/manager/runtime -run TestRuntimeDependenciesDoNotNameHotplug -count=1
```

Expected: FAIL naming `HotplugStart`, `HotplugSockets`, or `HotplugGuest`.

- [ ] **Step 3: Make runtime control startup take explicit options**

Replace `virtie/internal/manager/runtime/control_server.go` with:

```go
package runtime

import (
	"context"
	"log/slog"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

func StartControl(ctx context.Context, socketPath string, router *control.Router, logger *slog.Logger) (*control.Server, error) {
	if socketPath == "" {
		return nil, nil
	}
	listener, err := control.Listen(socketPath)
	if err != nil {
		return nil, err
	}
	server, err := control.NewServer(router)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	go func() {
		if err := server.Serve(listener); err != nil && ctx.Err() == nil && logger != nil {
			logger.Info("control socket stopped", "err", err)
		}
	}()
	return server, nil
}
```

In `virtie/internal/manager/runtime/concrete.go`, replace `StartControl` with:

```go
func (r *Runtime) StartControl(ctx context.Context, options ...control.RouterOption) (*control.Server, error) {
	routerOptions := []control.RouterOption{
		control.WithSuspend(r),
		control.WithBalloon(r),
	}
	routerOptions = append(routerOptions, options...)
	router, err := control.NewRouter(r, routerOptions...)
	if err != nil {
		return nil, err
	}
	controlServer, err := StartControl(ctx, r.paths.ControlSocket, router, r.logger)
	if err == nil {
		r.control = controlServer
	}
	return controlServer, err
}
```

- [ ] **Step 4: Remove hotplug fields from runtime core**

In `virtie/internal/manager/runtime/dependencies.go`, replace `Dependencies` with:

```go
type Dependencies struct {
	QMPTimeout       time.Duration
	Logger           *slog.Logger
	SavedSuspendExit func(error) bool
	CollectInfo      func(context.Context, string, executor.Group) (GuestInfo, error)
}
```

In `virtie/internal/manager/runtime/concrete.go`, remove these fields from `Runtime`:

```go
	hotplugStart     HotplugStarter
	hotplugSockets   HotplugSocketWaiter
	hotplugGuest     HotplugGuest
```

Also remove these assignments from `New`:

```go
		hotplugStart:     deps.HotplugStart,
		hotplugSockets:   deps.HotplugSockets,
		hotplugGuest:     deps.HotplugGuest,
```

Delete these files:

```bash
rm virtie/internal/manager/runtime/concrete_hotplug.go
rm virtie/internal/manager/runtime/hotplug.go
rm virtie/internal/manager/runtime/hotplug_test.go
```

- [ ] **Step 5: Update runtime control server tests**

In `virtie/internal/manager/runtime/control_server_test.go`, update `TestStartControlServesRuntimeHandler` to build a router:

```go
func TestStartControlServesRuntimeHandler(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "virtie.sock")
	handler := fakeRuntimeHandler{}
	router, err := control.NewRouter(handler)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	server, err := StartControl(context.Background(), socketPath, router, nil)
	if err != nil {
		t.Fatalf("start control: %v", err)
	}
	defer server.Close()

	resp, err := control.Dial(socketPath).Status(context.Background(), control.StatusRequest{})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if resp.State != control.RuntimeReady || resp.CID != 7 {
		t.Fatalf("unexpected status response: %#v", resp)
	}
}
```

Update `TestStartControlEmptySocketPath` to pass a router:

```go
func TestStartControlEmptySocketPath(t *testing.T) {
	router, err := control.NewRouter(fakeRuntimeHandler{})
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	server, err := StartControl(context.Background(), "", router, nil)
	if err != nil {
		t.Fatalf("empty start control: %v", err)
	}
	if server != nil {
		t.Fatalf("expected nil server for empty socket path, got %#v", server)
	}
}
```

- [ ] **Step 6: Remove hotplug dependencies from manager runtime construction**

In `virtie/internal/manager/manager.go`, remove these fields from the `runtimepkg.Dependencies` literal:

```go
		HotplugStart:     managerHotplugStarter{m: m},
		HotplugSockets:   managerHotplugSocketWaiter{m: m},
		HotplugGuest:     managerHotplugGuest{m: m, manifest: plan.Manifest},
```

Leave `runtime.StartControl(launchCtx)` unchanged for this task. Task 4 will register the hotplug handler.

- [ ] **Step 7: Run runtime and manager tests for this seam**

Run:

```bash
cd virtie && go test ./internal/manager/runtime ./internal/manager -run 'TestRuntimeDependenciesDoNotNameHotplug|TestStartControl|TestRuntime|TestManagerHotplug' -count=1
```

Expected: runtime tests pass. Manager hotplug tests can fail because runtime control hotplug registration is not implemented yet; if they fail only on unsupported control hotplug during launched runtime tests, continue to Task 4 before committing. If unrelated manager tests fail, fix them before moving on.

- [ ] **Step 8: Commit if manager hotplug tests are not broken**

If Task 2 tests pass without needing Task 4, commit:

```bash
git add virtie/internal/manager/runtime virtie/internal/manager/manager.go
git commit -m "virtie: Remove hotplug from runtime core" \
  -m "Runtime control startup now accepts explicit control options and runtime dependencies no longer carry hotplug-named feature adapters." \
  -m "Validation performed:
- cd virtie && go test ./internal/manager/runtime ./internal/manager -run 'TestRuntimeDependenciesDoNotNameHotplug|TestStartControl|TestRuntime|TestManagerHotplug' -count=1" \
  -m "Assisted-by: codex:gpt-5"
```

If manager hotplug tests are broken only because the handler is not registered, delay this commit until Task 4 and commit Tasks 2-4 together.

---

### Task 3: Move QMP Hotplug Policy Into The Hotplug Module

**Files:**
- Create: `virtie/internal/hotplug/qmp_adapter.go`
- Modify: `virtie/internal/hotplug/hotplug.go`
- Modify: `virtie/internal/hotplug/hotplug_test.go`

- [ ] **Step 1: Write failing typed-adapter tests**

Add these tests to `virtie/internal/hotplug/hotplug_test.go`:

```go
func TestQMPDeviceAdapterAttachesVirtioFSWithoutCallerRawJSON(t *testing.T) {
	client := &fakeQMPClient{}
	adapter := QMPDeviceAdapter{Client: client, Timeout: time.Second}
	device := testVirtioFSDevice(t.TempDir())

	rollback, err := adapter.AttachDevice(context.Background(), device, "pcie.hotplug.0")
	if err != nil {
		t.Fatalf("attach device: %v", err)
	}
	if rollback == nil {
		t.Fatal("expected rollback function")
	}
	if got, want := client.events, []string{"run:chardev-add", "run:device_add"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %#v want %#v", got, want)
	}
}

func TestQMPDeviceAdapterDetachWaitsBeforeCleanup(t *testing.T) {
	client := &fakeQMPClient{}
	adapter := QMPDeviceAdapter{Client: client, Timeout: time.Second}

	if err := adapter.DetachDevice(context.Background(), testVirtioFSDevice(t.TempDir())); err != nil {
		t.Fatalf("detach device: %v", err)
	}
	if got, want := client.events, []string{"device_del:dev-cache", "run:chardev-remove"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %#v want %#v", got, want)
	}
}
```

Update the test imports to include:

```go
	"time"
```

Replace `fakeQMP` with `fakeQMPClient` in the same test file:

```go
type fakeQMPClient struct {
	commands   []string
	deviceDels []string
	events     []string
	errAt      int
}

func (q *fakeQMPClient) RunRaw(timeout time.Duration, command string) error {
	q.commands = append(q.commands, command)
	var message struct {
		Execute string `json:"execute"`
	}
	_ = jsonUnmarshal(command, &message)
	q.events = append(q.events, "run:"+message.Execute)
	if q.errAt > 0 && len(q.commands) == q.errAt {
		return errors.New("qmp failed")
	}
	return nil
}

func (q *fakeQMPClient) DeviceDelAndWait(timeout time.Duration, id string) error {
	q.deviceDels = append(q.deviceDels, id)
	q.events = append(q.events, "device_del:"+id)
	return nil
}
```

- [ ] **Step 2: Run failing hotplug adapter tests**

Run:

```bash
cd virtie && go test ./internal/hotplug -run 'TestQMPDeviceAdapter' -count=1
```

Expected: FAIL because `QMPDeviceAdapter`, `AttachDevice`, and `DetachDevice` do not exist.

- [ ] **Step 3: Create the typed QMP adapter**

Create `virtie/internal/hotplug/qmp_adapter.go`:

```go
package hotplug

import (
	"context"
	"time"

	"github.com/shazow/agentspace/virtie/internal/hotplugtypes"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

// QMPDeviceAdapter adapts a generic QMP client to hotplug device operations.
type QMPDeviceAdapter struct {
	Client  interface {
		qmpclient.RawRunner
		qmpclient.DeviceController
	}
	Timeout time.Duration
}

func (a QMPDeviceAdapter) AttachDevice(ctx context.Context, device hotplugtypes.Device, bus string) (func(context.Context), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	successful := 0
	for _, command := range attachCommands(device, bus) {
		if err := a.Client.RunRaw(a.Timeout, command); err != nil {
			a.rollbackAttach(ctx, device, successful)
			return nil, err
		}
		successful++
	}
	return func(ctx context.Context) {
		a.rollbackAttach(ctx, device, successful)
	}, nil
}

func (a QMPDeviceAdapter) DetachDevice(ctx context.Context, device hotplugtypes.Device) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deviceID := qemuDeviceID(device.ID)
	if err := a.Client.DeviceDelAndWait(a.Timeout, deviceID); err != nil {
		return err
	}
	for _, command := range detachPostDeviceDelCommands(device) {
		if err := a.Client.RunRaw(a.Timeout, command); err != nil {
			return err
		}
	}
	return nil
}

func (a QMPDeviceAdapter) rollbackAttach(ctx context.Context, device hotplugtypes.Device, successful int) {
	if ctx.Err() != nil || successful == 0 {
		return
	}
	for _, command := range rollbackAttachCommands(device, successful) {
		_ = a.Client.RunRaw(a.Timeout, command)
	}
}
```

- [ ] **Step 4: Replace raw hotplug QMP interface with typed device interface**

In `virtie/internal/hotplug/hotplug.go`, replace:

```go
type QMPClient interface {
	Run(ctx context.Context, command string) error
	DeviceDel(ctx context.Context, id string) error
}
```

with:

```go
type DeviceQMP interface {
	AttachDevice(context.Context, hotplugtypes.Device, string) (func(context.Context), error)
	DetachDevice(context.Context, hotplugtypes.Device) error
}
```

Change the runtime field:

```go
	QMP      DeviceQMP
```

Replace `attachQMP`, `rollbackAttachQMP`, and `detachQMP` usage in `hotplugBase.attach`:

```go
	rollbackQMP, err := h.runtime.QMP.AttachDevice(h.ctx, device, h.bus)
	if err != nil {
		detachHost(proc)
		return err
	}
	if rollbackQMP == nil {
		rollbackQMP = func(context.Context) {}
	}
	if err := h.runtime.attachGuest(h.ctx, device); err != nil {
		rollbackQMP(h.ctx)
		detachHost(proc)
		return err
	}
	if err := hotplugtypes.WriteState(statePath, state); err != nil {
		_ = h.runtime.detachGuest(h.ctx, device)
		rollbackQMP(h.ctx)
		detachHost(proc)
		return err
	}
```

Replace QMP detach in `hotplugBase.detach`:

```go
	if err := h.runtime.QMP.DetachDevice(h.ctx, device); err != nil {
		return err
	}
```

Delete these methods from `hotplug.go` after all callers are gone:

```go
func (r Runtime) attachQMP(ctx context.Context, device hotplugtypes.Device, bus string) error
func (r Runtime) rollbackAttachQMP(ctx context.Context, device hotplugtypes.Device, successful int)
func (r Runtime) detachQMP(ctx context.Context, device hotplugtypes.Device) error
```

- [ ] **Step 5: Update transaction tests to use typed QMP**

In `testRuntimeDevices`, initialize QMP with the typed adapter:

```go
	client := &fakeQMPClient{}
	return Runtime{
		StateDir: filepath.Join(tmpDir, "state"),
		WorkDir:  tmpDir,
		Devices:  devices,
		Start:    starter,
		Sockets:  fakeSockets{},
		QMP:      QMPDeviceAdapter{Client: client, Timeout: time.Second},
		Guest:    guest,
	}, starter, client, guest
```

Keep assertions that inspect `client.events`, `client.deviceDels`, and parsed command names. Do not require callers to pass raw JSON.

- [ ] **Step 6: Run hotplug package tests**

Run:

```bash
cd virtie && go test ./internal/hotplug -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add virtie/internal/hotplug
git commit -m "virtie: Type hotplug QMP transactions" \
  -m "Hotplug now talks to QMP through device-level operations owned by the hotplug module instead of exposing raw QMP strings at the caller seam." \
  -m "Validation performed:
- cd virtie && go test ./internal/hotplug -count=1" \
  -m "Assisted-by: codex:gpt-5"
```

---

### Task 4: Assemble Hotplug At The Manager Periphery

**Files:**
- Modify: `virtie/internal/manager/hotplug.go`
- Create: `virtie/internal/manager/hotplug_feature.go`
- Modify: `virtie/internal/manager/manager.go`
- Modify: `virtie/internal/manager/manager_test.go`

- [ ] **Step 1: Write failing manager registration test**

Add this test near the existing hotplug tests in `virtie/internal/manager/manager_test.go`:

```go
func TestLaunchRuntimeRegistersHotplugAtControlPeriphery(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	cfg.Persistence.StateDir = ".virtie"
	cfg.Paths.RuntimeDir = manifest.RuntimeDir{Mode: manifest.RuntimeDirPath, Path: ".virtie"}
	cfg.QEMU.Hotplug.PCIEPorts = 1
	cfg.Hotplug = []hotplugtypes.Device{
		{
			Kind: hotplugtypes.KindNet,
			ID:   "vpn",
			Net:  hotplugtypes.Net{Backend: "user", MAC: "02:02:00:00:00:10"},
		},
	}

	runner := &launchRunner{}
	qmp := &fakeQMPClient{}
	manager := &manager{
		runner:            runner,
		qmpDialer:         &fakeQMPDialer{client: qmp},
		socketWaiter:      &fakeSocketWaiter{},
		qmpConnectTimeout: time.Second,
		qmpRetryDelay:     time.Millisecond,
	}
	plan, err := manager.planLaunch(launch.Spec{Manifest: cfg, Options: LaunchOptions{Resume: ResumeModeNo, SSH: false}})
	if err != nil {
		t.Fatalf("plan launch: %v", err)
	}

	runtime, err := manager.startWithPlan(context.Background(), plan)
	if err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	defer runtime.Close()

	_, err = control.Dial(plan.Paths.ControlSocket).Hotplug(context.Background(), control.HotplugRequest{ID: "vpn"})
	if err != nil {
		t.Fatalf("control hotplug: %v", err)
	}
	if got := strings.Join(qmp.rawCommands, "\n"); !strings.Contains(got, `"execute":"netdev_add"`) {
		t.Fatalf("expected netdev_add command, got %#v", qmp.rawCommands)
	}
}
```

- [ ] **Step 2: Run the focused failing manager test**

Run:

```bash
cd virtie && go test ./internal/manager -run TestLaunchRuntimeRegistersHotplugAtControlPeriphery -count=1
```

Expected: FAIL with unsupported hotplug or compile errors from the removed runtime hotplug method.

- [ ] **Step 3: Create manager-side hotplug feature assembly**

Create `virtie/internal/manager/hotplug_feature.go`:

```go
package manager

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/hotplug"
	controlpkg "github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type managerHotplugFeature struct {
	runner hotplug.Runtime
}

func (f managerHotplugFeature) Hotplug(ctx context.Context, req controlpkg.HotplugRequest) (controlpkg.HotplugResponse, error) {
	if req.Detach {
		if err := f.runner.Detach(ctx, req.ID); err != nil {
			return controlpkg.HotplugResponse{}, launch.WrapHotplugError(err)
		}
		return controlpkg.HotplugResponse{ID: req.ID, Detach: true}, nil
	}
	if err := f.runner.Attach(ctx, req.ID); err != nil {
		return controlpkg.HotplugResponse{}, launch.WrapHotplugError(err)
	}
	return controlpkg.HotplugResponse{ID: req.ID}, nil
}

func (m *manager) hotplugFeature(launchManifest *manifest.Manifest, client qmpclient.Client) managerHotplugFeature {
	return managerHotplugFeature{runner: m.hotplugRunner(launchManifest, client)}
}

func (m *manager) hotplugRunner(launchManifest *manifest.Manifest, client qmpclient.Client) hotplug.Runtime {
	return hotplug.Runtime{
		StateDir: launchManifest.ResolvedPersistenceStateDir(),
		WorkDir:  launchManifest.Paths.WorkingDir,
		Devices:  launchManifest.Hotplug,
		Start:    managerHotplugStarter{m: m},
		Sockets:  managerHotplugSocketWaiter{m: m},
		QMP:      hotplug.QMPDeviceAdapter{Client: client, Timeout: m.effectiveQMPCommandTimeout()},
		Guest:    managerHotplugGuest{m: m, manifest: launchManifest},
	}
}
```

This uses the current `hotplug.Runtime` name. If Task 5 renames it to `Runner`, update this file during Task 5.

- [ ] **Step 4: Register hotplug only when starting runtime control**

In `virtie/internal/manager/manager.go`, replace:

```go
	if _, err := runtime.StartControl(launchCtx); err != nil {
		return nil, launch.WrapFixedStage("control startup")(err)
	}
```

with:

```go
	hotplugFeature := m.hotplugFeature(plan.Manifest, qmpClient)
	if _, err := runtime.StartControl(launchCtx, controlpkg.WithHotplug(hotplugFeature)); err != nil {
		return nil, launch.WrapFixedStage("control startup")(err)
	}
```

Add the existing `controlpkg` import if it is not already present in `manager.go`:

```go
	controlpkg "github.com/shazow/agentspace/virtie/internal/manager/control"
```

- [ ] **Step 5: Update direct hotplug fallback to share feature assembly**

In `virtie/internal/manager/hotplug.go`, replace `hotplugRuntime` with:

```go
func (m *manager) directHotplugFeature(ctx context.Context, launchManifest *manifest.Manifest) (managerHotplugFeature, qmpclient.Client, error) {
	socketPath, err := launchManifest.ResolvedQMPSocketPath()
	if err != nil {
		return managerHotplugFeature{}, nil, err
	}
	client, err := m.waitForQMP(ctx, socketPath, executor.Group{})
	if err != nil {
		return managerHotplugFeature{}, nil, err
	}
	return m.hotplugFeature(launchManifest, client), client, nil
}
```

In `hotplug`, replace:

```go
	runtime, client, err := m.hotplugRuntime(ctx, launchManifest)
```

with:

```go
	feature, client, err := m.directHotplugFeature(ctx, launchManifest)
```

Replace direct attach/detach calls with:

```go
	if detach {
		_, err := feature.Hotplug(ctx, controlpkg.HotplugRequest{ID: id, Detach: true})
		return err
	}
	_, err := feature.Hotplug(ctx, controlpkg.HotplugRequest{ID: id})
	return err
```

Remove the old `runtimepkg` import from `manager/hotplug.go`.

- [ ] **Step 6: Run manager hotplug tests**

Run:

```bash
cd virtie && go test ./internal/manager -run 'TestManagerHotplug|TestLaunchRuntimeRegistersHotplugAtControlPeriphery' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2 through Task 4 together if Task 2 was not committed**

If Task 2 was not committed, commit all runtime isolation and manager feature assembly changes now:

```bash
git add virtie/internal/manager/runtime virtie/internal/manager/manager.go virtie/internal/manager/hotplug.go virtie/internal/manager/hotplug_feature.go virtie/internal/manager/manager_test.go
git commit -m "virtie: Isolate hotplug control handling" \
  -m "Runtime core no longer implements hotplug. Manager registers a hotplug feature handler at control startup and direct hotplug fallback shares the same feature assembly." \
  -m "Validation performed:
- cd virtie && go test ./internal/manager/runtime ./internal/manager -run 'TestRuntimeDependenciesDoNotNameHotplug|TestStartControl|TestManagerHotplug|TestLaunchRuntimeRegistersHotplugAtControlPeriphery' -count=1" \
  -m "Assisted-by: codex:gpt-5"
```

If Task 2 was already committed, commit only manager feature assembly changes:

```bash
git add virtie/internal/manager/manager.go virtie/internal/manager/hotplug.go virtie/internal/manager/hotplug_feature.go virtie/internal/manager/manager_test.go
git commit -m "virtie: Register hotplug at the manager periphery" \
  -m "Manager now assembles the hotplug feature for runtime control and direct fallback without making runtime core depend on hotplug policy." \
  -m "Validation performed:
- cd virtie && go test ./internal/manager -run 'TestManagerHotplug|TestLaunchRuntimeRegistersHotplugAtControlPeriphery' -count=1" \
  -m "Assisted-by: codex:gpt-5"
```

---

### Task 5: Rename The Hotplug Runtime Shape To Transaction Runner

**Files:**
- Modify: `virtie/internal/hotplug/hotplug.go`
- Modify: `virtie/internal/hotplug/hotplug_test.go`
- Modify: `virtie/internal/manager/hotplug_feature.go`

- [ ] **Step 1: Write the public-shape test**

Add this check to `TestHotplugPackageDoesNotReExportDataTypes` in `virtie/internal/hotplug/api_surface_test.go`:

```go
		"Runtime":             {},
```

- [ ] **Step 2: Run the focused failing test**

Run:

```bash
cd virtie && go test ./internal/hotplug -run TestHotplugPackageDoesNotReExportDataTypes -count=1
```

Expected: FAIL because `Runtime` is still exported.

- [ ] **Step 3: Rename runtime shape**

In `virtie/internal/hotplug/hotplug.go`, rename:

```go
type Runtime struct {
```

to:

```go
type Runner struct {
```

Rename all method receivers from `Runtime` to `Runner`.

In `virtie/internal/hotplug/hotplug_test.go`, update helper return types and literals from `Runtime` to `Runner`.

In `virtie/internal/manager/hotplug_feature.go`, update:

```go
	runner hotplug.Runtime
```

to:

```go
	runner hotplug.Runner
```

and update:

```go
func (m *manager) hotplugRunner(launchManifest *manifest.Manifest, client qmpclient.Client) hotplug.Runtime {
	return hotplug.Runtime{
```

to:

```go
func (m *manager) hotplugRunner(launchManifest *manifest.Manifest, client qmpclient.Client) hotplug.Runner {
	return hotplug.Runner{
```

- [ ] **Step 4: Run hotplug and manager package tests**

Run:

```bash
cd virtie && go test ./internal/hotplug ./internal/manager -run 'TestHotplugPackageDoesNotReExportDataTypes|TestManagerHotplug|TestLaunchRuntimeRegistersHotplugAtControlPeriphery' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add virtie/internal/hotplug virtie/internal/manager/hotplug_feature.go
git commit -m "virtie: Name hotplug transactions as a runner" \
  -m "The hotplug package no longer exports a runtime-shaped type; callers use a feature runner while runtime core stays independent of hotplug policy." \
  -m "Validation performed:
- cd virtie && go test ./internal/hotplug ./internal/manager -run 'TestHotplugPackageDoesNotReExportDataTypes|TestManagerHotplug|TestLaunchRuntimeRegistersHotplugAtControlPeriphery' -count=1" \
  -m "Assisted-by: codex:gpt-5"
```

---

### Task 6: Full Verification And Cleanup

**Files:**
- Modify only files touched by previous tasks if verification reveals compile or behavior issues.

- [ ] **Step 1: Search for stale hotplug coupling**

Run:

```bash
rg -n "HotplugStart|HotplugSockets|HotplugGuest|NewRuntimeRouter|runtimepkg\\.HotplugQMP|hotplugRuntime|concrete_hotplug|type Runtime struct" virtie/internal
```

Expected: no matches except test names or comments that intentionally describe removed symbols. Remove stale comments if they appear.

- [ ] **Step 2: Run Go tests**

Run:

```bash
cd virtie && go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run diff check**

Run:

```bash
git diff --check
```

Expected: no output.

- [ ] **Step 4: Commit cleanup if needed**

If Step 1 or Step 3 required edits, commit them:

```bash
git add virtie docs/superpowers/plans/2026-06-13-hotplug-transactions.md
git commit -m "virtie: Clean up hotplug transaction refactor" \
  -m "Remove stale names and formatting issues after isolating hotplug as a feature-periphery transaction module." \
  -m "Validation performed:
- cd virtie && go test ./...
- git diff --check" \
  -m "Assisted-by: codex:gpt-5"
```

If no cleanup edits were needed, do not create an empty commit.
