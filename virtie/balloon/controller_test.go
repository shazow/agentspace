package balloon

import (
	"errors"
	"testing"
	"time"
)

func TestEvaluateGrowsGuestMemory(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	config := ControllerConfig{
		MinActualMiB:             256,
		MaxActualMiB:             1024,
		GrowBelowAvailableMiB:    128,
		ReclaimAboveAvailableMiB: 768,
		StepMiB:                  128,
		PollIntervalSeconds:      5,
		ReclaimHoldoffSeconds:    30,
	}
	state := &controllerState{startedAt: now.Add(-time.Minute)}

	target, apply, err := evaluate(config, state, now, int64(512)*BytesPerMiB, guestStatsSample{
		AvailableMemoryBytes: int64(64) * BytesPerMiB,
		HasAvailableMemory:   true,
		LastUpdate:           now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !apply {
		t.Fatal("expected grow decision")
	}
	if got, want := target, int64(640)*BytesPerMiB; got != want {
		t.Fatalf("unexpected grow target: got %d want %d", got, want)
	}
}

func TestEvaluateReclaimsAfterHoldoff(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	config := ControllerConfig{
		MinActualMiB:             256,
		MaxActualMiB:             1024,
		GrowBelowAvailableMiB:    128,
		ReclaimAboveAvailableMiB: 768,
		StepMiB:                  128,
		PollIntervalSeconds:      5,
		ReclaimHoldoffSeconds:    30,
	}
	state := &controllerState{startedAt: now.Add(-time.Minute)}

	if target, apply, err := evaluate(config, state, now, int64(512)*BytesPerMiB, guestStatsSample{
		AvailableMemoryBytes: int64(900) * BytesPerMiB,
		HasAvailableMemory:   true,
		LastUpdate:           now,
	}); err != nil || apply || target != 0 {
		t.Fatalf("expected holdoff arm only, got target=%d apply=%v err=%v", target, apply, err)
	}

	target, apply, err := evaluate(config, state, now.Add(30*time.Second), int64(512)*BytesPerMiB, guestStatsSample{
		AvailableMemoryBytes: int64(900) * BytesPerMiB,
		HasAvailableMemory:   true,
		LastUpdate:           now.Add(30 * time.Second),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !apply {
		t.Fatal("expected reclaim decision")
	}
	if got, want := target, int64(384)*BytesPerMiB; got != want {
		t.Fatalf("unexpected reclaim target: got %d want %d", got, want)
	}
}

func TestEvaluateNoopsWithinHysteresis(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	config := ControllerConfig{
		MinActualMiB:             256,
		MaxActualMiB:             1024,
		GrowBelowAvailableMiB:    128,
		ReclaimAboveAvailableMiB: 768,
		StepMiB:                  128,
		PollIntervalSeconds:      5,
		ReclaimHoldoffSeconds:    30,
	}
	state := &controllerState{
		startedAt:           now.Add(-time.Minute),
		aboveThresholdSince: now.Add(-time.Second),
	}

	target, apply, err := evaluate(config, state, now, int64(512)*BytesPerMiB, guestStatsSample{
		AvailableMemoryBytes: int64(512) * BytesPerMiB,
		HasAvailableMemory:   true,
		LastUpdate:           now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if apply || target != 0 {
		t.Fatalf("expected no-op, got target=%d apply=%v", target, apply)
	}
	if !state.aboveThresholdSince.IsZero() {
		t.Fatalf("expected reclaim holdoff to reset, got %s", state.aboveThresholdSince)
	}
}

func TestEvaluateRejectsStaleStats(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	config := ControllerConfig{
		MinActualMiB:             256,
		MaxActualMiB:             1024,
		GrowBelowAvailableMiB:    128,
		ReclaimAboveAvailableMiB: 768,
		StepMiB:                  128,
		PollIntervalSeconds:      5,
		ReclaimHoldoffSeconds:    30,
	}
	state := &controllerState{startedAt: now.Add(-time.Minute)}

	_, _, err := evaluate(config, state, now, int64(512)*BytesPerMiB, guestStatsSample{
		AvailableMemoryBytes: int64(512) * BytesPerMiB,
		HasAvailableMemory:   true,
		LastUpdate:           now.Add(-11 * time.Second),
	})
	if !errors.Is(err, errGuestStatsStale) {
		t.Fatalf("expected stale stats error, got %v", err)
	}
}

func TestEvaluateRejectsUnavailableStats(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	config := ControllerConfig{
		MinActualMiB:             256,
		MaxActualMiB:             1024,
		GrowBelowAvailableMiB:    128,
		ReclaimAboveAvailableMiB: 768,
		StepMiB:                  128,
		PollIntervalSeconds:      5,
		ReclaimHoldoffSeconds:    30,
	}
	state := &controllerState{startedAt: now.Add(-11 * time.Second)}

	_, _, err := evaluate(config, state, now, int64(512)*BytesPerMiB, guestStatsSample{})
	if !errors.Is(err, errGuestStatsUnavailable) {
		t.Fatalf("expected unavailable stats error, got %v", err)
	}
}

func TestEvaluateClampsToBounds(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	config := ControllerConfig{
		MinActualMiB:             256,
		MaxActualMiB:             1024,
		GrowBelowAvailableMiB:    128,
		ReclaimAboveAvailableMiB: 768,
		StepMiB:                  256,
		PollIntervalSeconds:      5,
		ReclaimHoldoffSeconds:    30,
	}
	state := &controllerState{startedAt: now.Add(-time.Minute)}

	target, apply, err := evaluate(config, state, now, int64(960)*BytesPerMiB, guestStatsSample{
		AvailableMemoryBytes: int64(64) * BytesPerMiB,
		HasAvailableMemory:   true,
		LastUpdate:           now,
	})
	if err != nil || !apply {
		t.Fatalf("expected clamped grow, got target=%d apply=%v err=%v", target, apply, err)
	}
	if got, want := target, int64(1024)*BytesPerMiB; got != want {
		t.Fatalf("unexpected max clamp: got %d want %d", got, want)
	}

	state = &controllerState{
		startedAt:           now.Add(-time.Minute),
		aboveThresholdSince: now.Add(-31 * time.Second),
	}
	target, apply, err = evaluate(config, state, now, int64(300)*BytesPerMiB, guestStatsSample{
		AvailableMemoryBytes: int64(900) * BytesPerMiB,
		HasAvailableMemory:   true,
		LastUpdate:           now,
	})
	if err != nil || !apply {
		t.Fatalf("expected clamped reclaim, got target=%d apply=%v err=%v", target, apply, err)
	}
	if got, want := target, int64(256)*BytesPerMiB; got != want {
		t.Fatalf("unexpected min clamp: got %d want %d", got, want)
	}
}

func TestAvailableMemoryFallsBackToFreeMemory(t *testing.T) {
	available, ok := AvailableMemory(Stats{
		Stats: map[string]int64{
			"stat-free-memory": 1234,
		},
	})
	if !ok {
		t.Fatal("expected fallback to stat-free-memory")
	}
	if got, want := available, int64(1234); got != want {
		t.Fatalf("unexpected available memory: got %d want %d", got, want)
	}
}

func TestControllerResolveQOMPathFallsBackToQOMList(t *testing.T) {
	session := &fakeSession{
		listQOMProperties: map[string][]ObjectPropertyInfo{
			"/machine/peripheral": {
				{Name: "rng0", Type: "child<virtio-rng-device>"},
			},
			"/machine/peripheral-anon": {
				{Name: "device[1]", Type: "child<virtio-balloon-device>"},
			},
			"/machine/peripheral-anon/device[1]": {
				{Name: "guest-stats", Type: "dict"},
				{Name: "guest-stats-polling-interval", Type: "int"},
			},
		},
		listQOMPropertiesErr: map[string]error{
			"/machine/peripheral/balloon0": errors.New("not found"),
		},
	}

	controller := &Controller{
		Session:    session,
		DeviceID:   "balloon0",
		QMPTimeout: time.Second,
	}

	path, err := controller.resolveQOMPath()
	if err != nil {
		t.Fatalf("resolve balloon qom path: %v", err)
	}
	if got, want := path, "/machine/peripheral-anon/device[1]"; got != want {
		t.Fatalf("unexpected balloon qom path: got %q want %q", got, want)
	}
}

type fakeSession struct {
	queryBalloonInfo      Info
	queryBalloonErr       error
	setBalloonErr         error
	enableBalloonStatsErr error
	readBalloonStats      Stats
	readBalloonStatsErr   error
	listQOMProperties     map[string][]ObjectPropertyInfo
	listQOMPropertiesErr  map[string]error
}

func (f *fakeSession) QueryBalloon(timeout time.Duration) (Info, error) {
	if f.queryBalloonErr != nil {
		return Info{}, f.queryBalloonErr
	}
	return f.queryBalloonInfo, nil
}

func (f *fakeSession) SetBalloonLogicalSize(timeout time.Duration, logicalSizeBytes int64) error {
	return f.setBalloonErr
}

func (f *fakeSession) EnableBalloonStatsPolling(timeout time.Duration, qomPath string, pollIntervalSeconds int) error {
	return f.enableBalloonStatsErr
}

func (f *fakeSession) ReadBalloonStats(timeout time.Duration, qomPath string) (Stats, error) {
	if f.readBalloonStatsErr != nil {
		return Stats{}, f.readBalloonStatsErr
	}
	return f.readBalloonStats, nil
}

func (f *fakeSession) ListQOMProperties(timeout time.Duration, path string) ([]ObjectPropertyInfo, error) {
	if err, ok := f.listQOMPropertiesErr[path]; ok {
		return nil, err
	}
	if props, ok := f.listQOMProperties[path]; ok {
		return append([]ObjectPropertyInfo(nil), props...), nil
	}
	return nil, errors.New("unexpected qom-list path")
}
