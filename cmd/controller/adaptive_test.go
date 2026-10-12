package main

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"
	"time"
)

func feedAdaptiveSwings(c *Controller, start time.Time, count int, spacing time.Duration, low, high, span float64) time.Time {
	for i := 0; i < count; i++ {
		fan, temp := low, 50.0
		if i%2 != 0 {
			fan, temp = high, 50+span
		}
		c.recordAdaptiveFloor(start.Add(time.Duration(i)*spacing), temp, 65, "CPU1", fan)
	}
	return start.Add(time.Duration(count-1) * spacing)
}

func TestAdaptiveFloorRequiresRepeatedThermalSwings(t *testing.T) {
	c := newTestController()
	start := time.Unix(1000, 0)
	feedAdaptiveSwings(c, start, 5, 30*time.Second, 20, 70, 10)
	if c.adaptive.floor != 0 {
		t.Fatalf("floor before four confirmed reversals = %d, want 0", c.adaptive.floor)
	}
	trigger := start.Add(150 * time.Second)
	floor := c.recordAdaptiveFloor(trigger, 60, 65, "CPU1", 70)
	if floor != 15 || !c.adaptive.expires.Equal(trigger.Add(adaptiveFloorTimeout)) {
		t.Fatalf("adaptive state = %+v, want floor 15 and 15-minute expiry", c.adaptive)
	}
}

func TestAdaptiveFloorIgnoresNoiseSpikesAndMonotonicLoads(t *testing.T) {
	for _, tc := range []struct {
		name      string
		low, high float64
		span      float64
		count     int
	}{
		{"small fan noise", 45, 50, 10, 20},
		{"small temperature noise", 20, 70, 1, 20},
		{"single load spike", 20, 70, 10, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestController()
			feedAdaptiveSwings(c, time.Unix(1000, 0), tc.count, 30*time.Second, tc.low, tc.high, tc.span)
			if c.adaptive.floor != 0 {
				t.Fatalf("spurious floor %d", c.adaptive.floor)
			}
		})
	}
	c := newTestController()
	for i := 0; i < 80; i++ {
		c.recordAdaptiveFloor(time.Unix(1000+int64(i)*5, 0), 40+float64(i)/2, 65, "CPU1", 20+float64(i))
	}
	if c.adaptive.floor != 0 {
		t.Fatalf("monotonic heating raised floor to %d", c.adaptive.floor)
	}
}

func TestAdaptiveFloorOnlyCountsRecentReversals(t *testing.T) {
	c := newTestController()
	c.cfg.CheckInterval = 3 * time.Minute
	feedAdaptiveSwings(c, time.Unix(1000, 0), 8, 4*time.Minute, 20, 70, 10)
	if c.adaptive.floor != 0 {
		t.Fatalf("reversals outside ten-minute window raised floor to %d", c.adaptive.floor)
	}
}

func TestAdaptiveFloorIncreasesWithFreshCyclesAndRespectsCaps(t *testing.T) {
	c := newTestController()
	trigger := feedAdaptiveSwings(c, time.Unix(1000, 0), 6, 30*time.Second, 20, 70, 10)
	for i := 1; i <= 4; i++ {
		fan, temp := 20.0, 50.0
		if i%2 == 0 {
			fan, temp = 70, 60
		}
		c.recordAdaptiveFloor(trigger.Add(time.Duration(i)*30*time.Second), temp, 65, "CPU1", fan)
		if i < 4 && c.adaptive.floor != 15 {
			t.Fatalf("renewed floor without four fresh reversals: %+v", c.adaptive)
		}
	}
	if c.adaptive.floor != 20 || !c.adaptive.expires.Equal(trigger.Add(2*time.Minute+adaptiveFloorTimeout)) {
		t.Fatalf("fresh oscillation did not raise and renew floor: %+v", c.adaptive)
	}
	for _, maximum := range []int{100, 23, 10} {
		c := newTestController()
		c.cfg.AutoModeFanSpeedMax = maximum
		feedAdaptiveSwings(c, time.Unix(1000, 0), 42, 30*time.Second, 20, 70, 10)
		want := adaptiveMaximumFloor
		if maximum < want {
			want = maximum
		}
		if want == c.cfg.AutoModeFanSpeedMin {
			want = 0 // No adaptive floor is needed when the allowed range is fixed.
		}
		if c.adaptive.floor != want {
			t.Fatalf("maximum=%d: floor=%d, want %d", maximum, c.adaptive.floor, want)
		}
	}
}

func TestAdaptiveFloorExpiresWithoutRenewingOnStableSamples(t *testing.T) {
	c := newTestController()
	trigger := feedAdaptiveSwings(c, time.Unix(1000, 0), 6, 30*time.Second, 20, 70, 10)
	expires := c.adaptive.expires
	for elapsed := 30 * time.Second; elapsed < adaptiveFloorTimeout; elapsed += 30 * time.Second {
		floor := c.recordAdaptiveFloor(trigger.Add(elapsed), 60, 65, "CPU1", 70)
		if floor != 15 || !c.adaptive.expires.Equal(expires) {
			t.Fatalf("stable samples renewed/changed floor: %+v", c.adaptive)
		}
	}
	if floor := c.recordAdaptiveFloor(expires, 60, 65, "CPU1", 70); floor != 0 || !c.adaptive.expires.IsZero() {
		t.Fatalf("floor failed to expire: %+v", c.adaptive)
	}
	for i := 1; i <= 60; i++ {
		if floor := c.recordAdaptiveFloor(expires.Add(time.Duration(i)*5*time.Second), 60, 65, "CPU1", 70); floor != 0 {
			t.Fatalf("stale history reactivated floor %d", floor)
		}
	}
}

func TestAdaptiveFloorResetsOnSourceTargetOrObservationGap(t *testing.T) {
	for _, name := range []string{"source", "target", "gap"} {
		t.Run(name, func(t *testing.T) {
			c := newTestController()
			trigger := feedAdaptiveSwings(c, time.Unix(1000, 0), 6, 30*time.Second, 20, 70, 10)
			source, target, elapsed := "CPU1", 65.0, 5*time.Second
			switch name {
			case "source":
				source = "GPU"
			case "target":
				target = 70
			case "gap":
				elapsed = 2 * time.Minute
			}
			if floor := c.recordAdaptiveFloor(trigger.Add(elapsed), 60, target, source, 70); floor != 0 {
				t.Fatalf("%s did not reset adaptive floor: %+v", name, c.adaptive)
			}
		})
	}
}

func TestAdaptiveFloorAppliesThroughRampAndReleasesGradually(t *testing.T) {
	c := newTestController()
	c.adaptive.floor = 30
	c.pid.current = 20
	speed, _ := c.automaticFanStep(30, 30, 50, trendState{})
	if speed != 22 {
		t.Fatalf("floor activation jumped to %d, want 22", speed)
	}
	for i := 0; i < 30; i++ {
		c.automaticFanStep(30, 30, 50, trendState{})
	}
	if math.Abs(c.pid.current-30) > 1e-9 {
		t.Fatalf("fan below adaptive minimum: %g", c.pid.current)
	}
	c.adaptive.floor = 0
	speed, _ = c.automaticFanStep(30, 30, 50, trendState{})
	if speed != 29 {
		t.Fatalf("floor release jumped to %d, want 29", speed)
	}
	for i := 0; i < 30; i++ {
		c.automaticFanStep(30, 30, 50, trendState{})
	}
	if c.pid.current != float64(c.cfg.AutoModeFanSpeedMin) {
		t.Fatalf("released floor left fan stuck at %g", c.pid.current)
	}
}

func TestAdaptiveFloorDashboardTelemetry(t *testing.T) {
	c := newTestController()
	c.cfg.DashboardSampleLimit = 30
	c.appendDashboardSample(cycleSample{
		timestamp:     time.Unix(1000, 0),
		adaptiveFloor: 15,
		adaptiveUntil: 1900,
	})
	response := httptest.NewRecorder()
	c.dashboardStatsHandler(response, httptest.NewRequest("GET", "/api/stats", nil))
	var payload struct {
		Now dashboardSample `json:"now"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Now.AdaptiveFloor != 15 || payload.Now.AdaptiveUntil != 1900 {
		t.Fatalf("dashboard adaptive state missing: %+v", payload.Now)
	}
}
