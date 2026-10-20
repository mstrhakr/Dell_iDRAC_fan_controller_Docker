package main

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"testing"
	"time"
)

func feedAdaptiveCycles(c *Controller, start time.Time, count int, spacing time.Duration, floor, peak float64) time.Time {
	var last time.Time
	for i := 0; i < count; i++ {
		cycleStart := start.Add(time.Duration(i) * spacing)
		last = cycleStart.Add(spacing / 2)
		c.recordAdaptiveFloor(cycleStart, 65, "CPU1", peak)
		c.recordAdaptiveFloor(last, 65, "CPU1", floor)
	}
	return last
}

func TestAdaptiveFloorLearnsFromOneHighDutyReturnCycle(t *testing.T) {
	c := newTestController()
	start := time.Unix(1000, 0)
	c.pid.current = 10
	c.recordAdaptiveFloor(start, 65, "CPU1", 55)
	if floor := c.recordAdaptiveFloor(start.Add(time.Minute), 65, "CPU1", 10); floor != 15 {
		t.Fatalf("floor after high-duty return = %d, want 15", floor)
	}
	for i := 0; i < 3; i++ {
		c.automaticFanStep(60, 60, 65, trendState{})
	}
	if c.pid.current != 15 {
		t.Fatalf("controller did not settle at learned floor: %g", c.pid.current)
	}
	for i := 0; i < 10; i++ {
		c.automaticFanStep(60, 60, 65, trendState{})
	}
	if c.pid.current != 15 {
		t.Fatalf("controller fell below persistent learned floor: %g", c.pid.current)
	}
}

func TestAdaptiveFloorRejectsLowDutyAndExpiredCycles(t *testing.T) {
	start := time.Unix(1000, 0)
	for _, tc := range []struct {
		name string
		peak float64
		wait time.Duration
	}{
		{"peak at or below 50 percent", 50, time.Minute},
		{"return after five minutes", 55, 6 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestController()
			c.recordAdaptiveFloor(start, 65, "CPU1", tc.peak)
			c.recordAdaptiveFloor(start.Add(tc.wait), 65, "CPU1", 10)
			if c.adaptive.floor != 0 {
				t.Fatalf("non-qualifying cycle learned floor %d", c.adaptive.floor)
			}
		})
	}
}

func TestAdaptiveFloorStepsUpAndRespectsConfiguredMaximum(t *testing.T) {
	c := newTestController()
	c.cfg.AutoModeFanSpeedMax = 23
	feedAdaptiveCycles(c, time.Unix(1000, 0), 10, time.Minute, 10, 55)
	if c.adaptive.floor != 23 {
		t.Fatalf("learned floor = %d, want configured cap 23", c.adaptive.floor)
	}
}

func TestAdaptiveFloorRequiresReturnNearFloor(t *testing.T) {
	c := newTestController()
	start := time.Unix(1000, 0)
	c.recordAdaptiveFloor(start, 65, "CPU1", 55)
	if floor := c.recordAdaptiveFloor(start.Add(time.Minute), 65, "CPU1", 13); floor != 0 {
		t.Fatalf("floor learned when duty returned 3 points above floor: %d", floor)
	}
	if floor := c.recordAdaptiveFloor(start.Add(2*time.Minute), 65, "CPU1", 12); floor != 15 {
		t.Fatalf("floor did not learn when duty returned within 2 points: %d", floor)
	}
}

func TestAdaptiveFloorPersistsWithoutNewOscillation(t *testing.T) {
	c := newTestController()
	trigger := feedAdaptiveCycles(c, time.Unix(1000, 0), 1, 2*time.Minute, 10, 55)
	for elapsed := 30 * time.Second; elapsed < 30*time.Minute; elapsed += 30 * time.Second {
		floor := c.recordAdaptiveFloor(trigger.Add(elapsed), 65, "CPU1", 15)
		if floor != 15 {
			t.Fatalf("stable samples changed learned floor: %+v", c.adaptive)
		}
	}
	if speed, _ := c.automaticFanStep(60, 60, 65, trendState{}); speed < 15 {
		t.Fatalf("controller dropped below learned floor: %d", speed)
	}
}

func TestAdaptiveFloorResetsOnSourceTargetOrObservationGap(t *testing.T) {
	for _, name := range []string{"source", "target", "gap"} {
		t.Run(name, func(t *testing.T) {
			c := newTestController()
			trigger := feedAdaptiveCycles(c, time.Unix(1000, 0), 1, 2*time.Minute, 10, 55)
			source, target, elapsed := "CPU1", 65.0, 5*time.Second
			switch name {
			case "source":
				source = "GPU"
			case "target":
				target = 70
			case "gap":
				elapsed = 2 * time.Minute
			}
			floor := c.recordAdaptiveFloor(trigger.Add(elapsed), target, source, 15)
			if name == "gap" {
				if floor != 15 || !c.adaptive.cycleStart.IsZero() {
					t.Fatalf("gap lost learned floor or retained stale cycle: %+v", c.adaptive)
				}
			} else if floor != 0 {
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
	})
	response := httptest.NewRecorder()
	c.dashboardStatsHandler(response, httptest.NewRequest("GET", "/api/stats", nil))
	var payload struct {
		Now dashboardSample `json:"now"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Now.AdaptiveFloor != 15 {
		t.Fatalf("dashboard adaptive state missing: %+v", payload.Now)
	}
}
