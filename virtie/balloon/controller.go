package balloon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	errGuestStatsUnavailable = errors.New("balloon guest stats unavailable")
	errGuestStatsStale       = errors.New("balloon guest stats stale")
	errQOMPathNotFound       = errors.New("balloon qom path not found")
)

type Logger interface {
	Printf(format string, args ...any)
}

type Session interface {
	QueryBalloon(timeout time.Duration) (Info, error)
	SetBalloonLogicalSize(timeout time.Duration, logicalSizeBytes int64) error
	EnableBalloonStatsPolling(timeout time.Duration, qomPath string, pollIntervalSeconds int) error
	ReadBalloonStats(timeout time.Duration, qomPath string) (Stats, error)
	ListQOMProperties(timeout time.Duration, path string) ([]ObjectPropertyInfo, error)
}

type Info struct {
	ActualBytes int64
}

type Stats struct {
	Stats      map[string]int64
	LastUpdate time.Time
}

type ObjectPropertyInfo struct {
	Name string
	Type string
}

type Controller struct {
	Session    Session
	Logger     Logger
	DeviceID   string
	Config     ControllerConfig
	QMPTimeout time.Duration
	Now        func() time.Time
}

type controllerState struct {
	startedAt           time.Time
	aboveThresholdSince time.Time
}

type guestStatsSample struct {
	AvailableMemoryBytes int64
	HasAvailableMemory   bool
	LastUpdate           time.Time
}

func (c *Controller) Run(ctx context.Context) error {
	if c == nil || c.Session == nil {
		return nil
	}

	now := c.nowFunc()
	state := controllerState{startedAt: now()}
	qomPath, err := c.resolveQOMPath()
	if err != nil {
		return err
	}

	if err := c.Session.EnableBalloonStatsPolling(c.QMPTimeout, qomPath, c.Config.PollIntervalSeconds); err != nil {
		return err
	}

	ticker := time.NewTicker(time.Duration(c.Config.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		actual, stats, err := c.readSample(qomPath)
		if err != nil {
			return err
		}

		target, apply, err := evaluate(c.Config, &state, now(), actual.ActualBytes, stats)
		if err != nil {
			return err
		}
		if !apply {
			continue
		}

		if err := c.Session.SetBalloonLogicalSize(c.QMPTimeout, target); err != nil {
			return err
		}
		if c.Logger != nil {
			c.Logger.Printf("balloon controller set guest memory to %d MiB", target/BytesPerMiB)
		}
	}
}

func AvailableMemory(stats Stats) (int64, bool) {
	if value, ok := stats.Stats["stat-available-memory"]; ok && value >= 0 {
		return value, true
	}
	if value, ok := stats.Stats["stat-free-memory"]; ok && value >= 0 {
		return value, true
	}
	return 0, false
}

func (c *Controller) resolveQOMPath() (string, error) {
	expected := "/machine/peripheral/" + c.DeviceID
	if c.qomPathSupportsGuestStats(expected) {
		return expected, nil
	}

	candidates := []string{expected}
	for _, root := range []string{"/machine/peripheral", "/machine/peripheral-anon"} {
		props, err := c.Session.ListQOMProperties(c.QMPTimeout, root)
		if err != nil {
			continue
		}
		for _, prop := range props {
			if !strings.HasPrefix(prop.Type, "child<") {
				continue
			}
			candidate := root + "/" + prop.Name
			if prop.Name == c.DeviceID {
				candidates = append([]string{candidate}, candidates...)
				continue
			}
			candidates = append(candidates, candidate)
		}
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if c.qomPathSupportsGuestStats(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("%w for %q", errQOMPathNotFound, c.DeviceID)
}

func (c *Controller) qomPathSupportsGuestStats(path string) bool {
	props, err := c.Session.ListQOMProperties(c.QMPTimeout, path)
	if err != nil {
		return false
	}
	return hasQOMProperty(props, "guest-stats") && hasQOMProperty(props, "guest-stats-polling-interval")
}

func (c *Controller) readSample(qomPath string) (Info, guestStatsSample, error) {
	stats, err := c.Session.ReadBalloonStats(c.QMPTimeout, qomPath)
	if err != nil {
		return Info{}, guestStatsSample{}, err
	}

	actual, err := c.Session.QueryBalloon(c.QMPTimeout)
	if err != nil {
		return Info{}, guestStatsSample{}, err
	}

	available, ok := AvailableMemory(stats)
	return actual, guestStatsSample{
		AvailableMemoryBytes: available,
		HasAvailableMemory:   ok,
		LastUpdate:           stats.LastUpdate,
	}, nil
}

func (c *Controller) nowFunc() func() time.Time {
	if c.Now != nil {
		return c.Now
	}
	return time.Now
}

func evaluate(
	config ControllerConfig,
	state *controllerState,
	now time.Time,
	actualBytes int64,
	stats guestStatsSample,
) (int64, bool, error) {
	pollInterval := time.Duration(config.PollIntervalSeconds) * time.Second
	staleAfter := 2 * pollInterval

	if stats.LastUpdate.IsZero() {
		if now.Sub(state.startedAt) >= staleAfter {
			return 0, false, errGuestStatsUnavailable
		}
		return 0, false, nil
	}

	if now.Sub(stats.LastUpdate) > staleAfter {
		return 0, false, errGuestStatsStale
	}

	if !stats.HasAvailableMemory {
		if now.Sub(state.startedAt) >= staleAfter {
			return 0, false, errGuestStatsUnavailable
		}
		return 0, false, nil
	}

	minActualBytes := int64(config.MinActualMiB) * BytesPerMiB
	maxActualBytes := int64(config.MaxActualMiB) * BytesPerMiB
	stepBytes := int64(config.StepMiB) * BytesPerMiB
	growBelowBytes := int64(config.GrowBelowAvailableMiB) * BytesPerMiB
	reclaimAboveBytes := int64(config.ReclaimAboveAvailableMiB) * BytesPerMiB

	if stats.AvailableMemoryBytes < growBelowBytes {
		state.aboveThresholdSince = time.Time{}
		target := actualBytes + stepBytes
		if target > maxActualBytes {
			target = maxActualBytes
		}
		if target <= actualBytes {
			return 0, false, nil
		}
		return target, true, nil
	}

	if stats.AvailableMemoryBytes > reclaimAboveBytes {
		if actualBytes <= minActualBytes {
			state.aboveThresholdSince = time.Time{}
			return 0, false, nil
		}
		if state.aboveThresholdSince.IsZero() {
			state.aboveThresholdSince = now
			return 0, false, nil
		}
		if now.Sub(state.aboveThresholdSince) < time.Duration(config.ReclaimHoldoffSeconds)*time.Second {
			return 0, false, nil
		}

		state.aboveThresholdSince = time.Time{}
		target := actualBytes - stepBytes
		if target < minActualBytes {
			target = minActualBytes
		}
		if target >= actualBytes {
			return 0, false, nil
		}
		return target, true, nil
	}

	state.aboveThresholdSince = time.Time{}
	return 0, false, nil
}

func hasQOMProperty(props []ObjectPropertyInfo, name string) bool {
	for _, prop := range props {
		if prop.Name == name {
			return true
		}
	}
	return false
}
