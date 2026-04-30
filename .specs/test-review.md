# Test Review

Review and rewrite the repo test surface so broad checks cover consumer workflows end to end while narrow tests stay focused on small contracts.

**Status**: Completed

## Goals

- Trace the current tests by the product path or contract they exercise.
- Consolidate broad checks that repeatedly build similar sandbox configurations.
- Keep tightly scoped tests for parser validation, manifest validation, QMP protocol behavior, lock behavior, balloon policy, and manager edge cases.
- Preserve default `nix flake check` coverage for the supported consumer launch path.

Out of scope:

- replacing the fake-QEMU E2E harness with a real VM boot
- changing public `mkSandbox`, `mkLaunch`, or `virtie` behavior
- adding mock-heavy tests where existing fake process or fake socket harnesses already cover the behavior

Acceptance criteria:

- [x] A single repo-level consumer workflow check covers SSH configuration, persistence, home-manager customization, direct `extraModules`, deferred `agentspace.sandbox.extraModules`, generated manifest values, and generated launch wrapper behavior.
- [x] Launch wrapper contract assertions live with the consumer workflow instead of a separate sandbox fixture.
- [x] Compatible manifest feature variants share one feature-rich fixture instead of evaluating separate sandboxes for balloon, write-files, and notifications.
- [x] Unsupported fixed vsock CID remains a narrow contract check.
- [x] The fake-tools `virtie` E2E check remains the broad launch/suspend/resume workflow check.
- [x] Go package tests remain the narrow implementation-level suite.
- [x] `nix flake check` passes with the consolidated check set.

## Progress

- [x] Reviewed repo-level Nix checks and found overlapping sandbox construction in consumer-surface, home-manager, and extra-modules checks.
- [x] Consolidated those overlapping checks into `checks/consumer-workflow.nix`.
- [x] Folded the launch wrapper contract check into `checks/consumer-workflow.nix`.
- [x] Kept the fixed-vsock-CID rejection as a narrow standalone derivation.
- [x] Consolidated compatible manifest feature checks into one feature-rich sandbox fixture.
- [x] Collapsed repetitive parser acceptance and rejection checks into table-driven narrow tests.
- [x] Run final Nix and Go validation.

## Appendix

Current broad workflow coverage:

- `checks/consumer-workflow.nix` verifies downstream-style Nix consumers can customize the sandbox, generated manifest, and generated launch wrapper together.
- `checks/virtie-manifest.nix` keeps baseline, feature-rich, disabled-balloon, and external-store-socket manifest fixtures separate by contract shape.
- `checks/virtie-e2e.nix` verifies the host-side `virtie` runtime against fake QEMU, QMP, QGA, SSH, `virtiofsd`, volume creation, no-SSH launch, suspend, resume, configured command launch, and SSH auth-failure behavior.

Current narrow coverage to keep:

- `virtie/main_test.go`: CLI parser shape and manifest working-directory persistence.
- `virtie/internal/manifest/*_test.go`: JSON loading, validation, path resolution, write-files, notifications, external sockets, and balloon defaults.
- `virtie/internal/manager/qmp_test.go`: QMP handshake and command encoding.
- `virtie/internal/manager/lock_test.go`: lock acquisition and recovery.
- `virtie/internal/balloon/*_test.go`: memory sizing policy, QMP balloon calls, and notification triggers.
- `virtie/internal/manager/manager_test.go`: launch sequencing, teardown order, guest file writes, suspend/resume, signal handling, CID allocation, QEMU argv assembly, and runtime socket resolution.
