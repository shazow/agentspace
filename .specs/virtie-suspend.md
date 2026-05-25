# Virtie Suspend

Reproducible design for virtie disk-backed suspend, guest-initiated suspend, and resume.

**Status**: In-Progress

## Goals

Make suspend/resume a single, explicit virtie lifecycle path that works for both host-requested suspend and guest-requested suspend.

- Preserve a running VM by saving QEMU migration state to disk through the launch process's existing QMP connection.
- Resume saved state through `virtie launch --resume=auto|force` without introducing a separate runtime owner.
- Support guests that emit a real QMP `SUSPEND` event and microvm guests where `systemctl suspend` needs a guest-to-host request path instead.
- Keep the public manifest small: QEMU remains the only backend, and guest suspend adds one optional socket field.
- Make validation deterministic enough to keep fake E2E tests in the default checks and real VM validation as an explicit heavier check.

Out of scope:

- Full guest hibernation / `SUSPEND_DISK`.
- Live pause/resume as a user-facing command.
- Reconnecting to an already-running launch process from a new virtie process.
- Relying on terminal job-control semantics for suspend.
- Making `systemctl sleep` a tested contract; use `systemctl suspend` for the real guest workflow.

Acceptance criteria:

- [ ] `virtie suspend --manifest=MANIFEST` signals the active launch process, and the launch process saves state using its owned QMP connection.
- [ ] `virtie launch --resume=force --manifest=MANIFEST` restores saved migration state and fails clearly when no valid state exists.
- [ ] `virtie launch --resume=auto --manifest=MANIFEST` restores when saved state is present and otherwise launches fresh.
- [ ] Guest ACPI suspend is detected from the QMP `SUSPEND` event when QEMU provides it.
- [ ] Microvm guest `systemctl suspend` is lowered to a write on `/dev/virtio-ports/virtie.suspend`, which virtie receives through a host Unix socket chardev.
- [ ] Saved suspend state records `status`, `vmStatePath`, `cid`, `runState`, `source`, and enough QMP/socket facts to validate and debug resume.
- [ ] Resume sends `system_wakeup` only for saved states whose guest run state was actually `suspended`; it must not wake states saved from a running microvm request.
- [ ] The runtime CID from the saved state is reused on resume.
- [ ] Default fake E2E checks cover host suspend, QMP guest suspend, microvm suspend-socket guest suspend, and resume for each saved-state shape.
- [ ] A real VM check validates `systemctl suspend` by proving a process started before suspend still exists after resume.

## Progress

- [ ] Define the final manifest migration note if `qemu.suspend_socket` becomes a documented public field.
- [ ] Implement a QMP monitor wrapper that can demultiplex asynchronous events and command responses without losing either.
- [ ] Implement a single suspend request pipeline shared by signals, QMP events, and the guest suspend socket.
- [ ] Add unit tests for QMP event demux, command timeout behavior, suspend-state source/run-state mapping, and resume wakeup decisions.
- [ ] Add fake E2E coverage for all suspend sources.
- [ ] Add real VM validation under an explicit non-default check surface.
- [ ] Remove temporary diagnostics and unrelated branch changes before landing the implementation.

## Appendix

### Lessons From The Prototype

The branch proved the overall approach, but it also mixed implementation, validation, and environment discoveries. The next implementation should be smaller and staged around the runtime contracts below.

- Do not key tests to hardcoded CID `3`. CID allocation is runtime state; tests should read the allocated CID and assert it is reused.
- Do not depend on `systemctl sleep`. The real workflow should call `sudo systemctl suspend` directly.
- Do not treat all guest suspend states the same on resume. A QMP `SUSPEND` event means QEMU reports a suspended guest and needs `system_wakeup` after `cont`; a microvm suspend-port request is saved from a running guest after `stop` and should resume with `cont` only.
- Do not start optional feature tasks by replacing the existing task group after QMP/guest-suspend watchers have been registered.
- Do not let QMP command deadlines leave a monitor in a usable-looking but ambiguous state. On command timeout, close the monitor and fail the operation.
- Keep real VM checks out of the default `checks` set unless the environment is known to expose `/dev/kvm` and `/dev/vhost-vsock`.
- Avoid incidental churn such as lockfile updates, agent-instruction edits, or debug-only flake changes in the final implementation branch.

### Ideal Runtime Path

Fresh launch:

1. Load and validate the manifest.
2. Resolve QMP, QGA, SSH readiness, guest suspend, and `virtiofs` socket paths using the same runtime-dir policy.
3. Acquire the sandbox lock and allocate a CID.
4. Remove stale managed socket paths, including the guest suspend socket when configured.
5. Start helper processes, then QEMU.
6. Connect QMP and start event watching before guest writes, SSH readiness, or user session work can block.
7. Start the guest suspend socket watcher when `qemu.suspend_socket` is configured.
8. Run write-files, SSH readiness, SSH session, and no-SSH wait steps through a shared helper that can abort immediately when a suspend request arrives.
9. On ordinary exit, tear down in the existing order: SSH, optional feature tasks, QMP quit, QEMU signal fallback, helpers, socket cleanup.

Suspend request handling:

1. Normalize every source into a `suspendRequest`:
   - host `virtie suspend` / `SIGTSTP`: source `virtie`, run state determined from `query-status`.
   - QMP `SUSPEND`: source `guest-suspend`, run state `suspended`.
   - guest suspend socket: source `guest-suspend`, run state `running`.
2. Skip write-back guest files for guest-suspend requests. The guest initiated suspend is not a normal interactive session boundary.
3. Query QMP status unless the request provides an authoritative run state.
4. If the effective run state is `running`, issue QMP `stop` before migration.
5. If the effective run state is `paused` or `suspended`, migrate directly.
6. Issue QMP `migrate` to `file:<vmstate path>`.
7. Poll `query-migrate` until migration reports completion or failure.
8. Write the suspend state JSON only after migration succeeds.
9. Notify `runtime:suspend`, remove the launch PID, and exit the launch process with the internal saved-suspend sentinel.

Resume path:

1. Read and validate saved suspend state.
2. Reuse the saved CID and lock it before launching QEMU.
3. Start QEMU with incoming migration deferred from the saved state file.
4. Connect QMP, issue `migrate-incoming`, and wait for migration completion.
5. Issue `cont` with a resume-specific timeout that is long enough for real guests.
6. If `runState == "suspended"`, issue `system_wakeup`; otherwise do not.
7. Notify `runtime:resume`, continue through SSH readiness/session, and remove saved state files after a successful restored session lifecycle.

### APIs To Rely On

Virtie CLI/API:

- `virtie launch --manifest=MANIFEST [--ssh] [--resume=no|auto|force] [-- <remote-cmd...>]`.
- `virtie suspend --manifest=MANIFEST` as the user-facing host suspend command.
- Internal launch signal handling may continue to use caught `SIGTSTP`, but it is an implementation detail, not terminal job control.

Manifest API:

- Human/generated input field: `qemu.suspend_socket`.
- Runtime field: `QEMU.GuestSuspend.SocketPath`.
- Resolution helper: `ResolvedGuestSuspendSocketPath`, using the same relative path and XDG runtime-dir policy as QMP, QGA, SSH readiness, and managed `virtiofs` sockets.

QEMU command-line API:

- Guest suspend port:
  - `-chardev socket,path=<resolved suspend socket>,server=on,wait=off,id=suspend_char`
  - `-device <virtio-serial driver>,id=suspend-serial`
  - `-device virtserialport,chardev=suspend_char,name=virtie.suspend`
- The virtio-serial driver should follow the existing transport-specific helper used for QGA and SSH readiness ports.

QMP API:

- `qmp_capabilities` during monitor connection.
- `query-status` before saving state when the request does not already define the run state.
- `stop` before migrating a running guest.
- `migrate` with `file:<path>` for saving VM state.
- `query-migrate` for migration completion polling.
- `migrate-incoming` with `file:<path>` for restore.
- `cont` after incoming migration completes.
- `system_wakeup` only after restoring a state saved from QMP run state `suspended`.
- Asynchronous `SUSPEND` events for guests where QEMU observes ACPI suspend.

Go APIs:

- Continue using `github.com/digitalocean/go-qemu/qmp` for monitor connection shape.
- Continue using `github.com/digitalocean/go-qemu/qmp/raw` for QMP commands not covered by typed helpers.
- The monitor implementation must expose both command responses and event delivery. A single blocking decoder per command is not sufficient because QMP can send asynchronous events between a command and its response.
- Use `net.Dialer` / Unix sockets for the guest suspend socket watcher, with context cancellation closing the connection.

NixOS / guest APIs:

- Install a small guest helper, `virtie-guest-suspend`, that writes a token such as `suspend\n` to `/dev/virtio-ports/virtie.suspend`.
- Override `systemd-suspend.service` `ExecStart` for generated microvm guests so `systemctl suspend` calls the helper instead of depending on unavailable ACPI S3 behavior.
- Keep `services.qemuGuest.enable = true` for the existing guest agent path.
- For real VM validation, ensure the check environment exposes `/dev/kvm` and `/dev/vhost-vsock`; do not hide that requirement in the default check surface.

### Ideal Validation

Unit tests:

- QMP monitor delivers an asynchronous `SUSPEND` event while a command response is still pending.
- QMP command timeout closes the monitor so a late response cannot corrupt a later command.
- Suspend-state save maps host suspend to source `virtie`.
- Suspend-state save maps QMP `SUSPEND` to source `guest-suspend`, run state `suspended`, and no pre-migration `stop`.
- Suspend-state save maps guest suspend socket to source `guest-suspend`, run state `running`, and performs `stop` before migration.
- Resume calls `system_wakeup` for `runState = "suspended"` and skips it for `runState = "running"`.
- QEMU argv compilation includes the suspend chardev and virtio-serial port only when `qemu.suspend_socket` is set.
- Manifest tests cover TOML/JSON input lowering and runtime-dir resolution for `qemu.suspend_socket`.

Fake E2E Nix check:

- The fake QEMU should implement enough QMP to exercise `SUSPEND`, `system_wakeup`, migration, incoming migration, and dynamic CID recording.
- Host suspend should save state with source `virtie`, preserve the allocated CID, and resume with the same CID.
- QMP guest suspend should save state with source `guest-suspend`, run state `suspended`, and resume with `system_wakeup`.
- Guest suspend socket should save state with source `guest-suspend`, run state `running`, perform `stop` before migration, and resume without `system_wakeup`.
- Assertions should inspect state files and fake-QEMU signal files instead of relying on fixed sleeps where a signal file or socket readiness can be used.

Real VM check:

- Keep this under `legacyPackages.<system>.realVMChecks` or a similarly explicit non-default surface.
- Build a minimal `mkSandbox` guest with SSH, persistence images, and enough memory to run reliably.
- Launch with `virtie launch --ssh --manifest=... -- bash -lc '<script>'`.
- In the script, start `sleep 12345 &`, print `VIRTIE_SLEEP_PID=<pid>`, call `sudo systemctl suspend`, then keep the session alive if control returns.
- After virtie exits from saved suspend, launch again with `--resume=force`.
- Verify `/proc/<pid>/cmdline` still matches `sleep 12345`.
- Capture `console.log` with `-serial file:console.log` or `-serial stdio` only as a debugging aid, not as a test oracle.

### Landing Plan

1. Start from `main` with a fresh branch/bookmark.
2. Add manifest and QEMU argv support for `qemu.suspend_socket`; validate with manifest and argv unit tests.
3. Add robust QMP event demux and timeout behavior; validate with isolated QMP tests.
4. Add the unified suspend request pipeline; validate with manager unit tests.
5. Add microvm guest helper and `systemd-suspend.service` override; validate generated manifest and module contract.
6. Add fake E2E coverage for host suspend, QMP guest suspend, and suspend-socket guest suspend.
7. Add the real VM check as opt-in validation and document its environment requirements.
8. Run `go test ./...` from `virtie/`, the default relevant Nix checks, and the real VM check when the host exposes the required devices.
