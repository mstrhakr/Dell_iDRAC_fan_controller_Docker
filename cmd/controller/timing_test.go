// SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"math"
	"testing"
	"time"
)

func TestEMAHasSameTimeConstantAcrossPollingIntervals(t *testing.T) {
	for _, interval := range []time.Duration{time.Second, 5 * time.Second, 15 * time.Second} {
		e := emaState{}
		e.update(0.3, 50)
		for elapsed := time.Duration(0); elapsed < time.Minute; elapsed += interval {
			e.update(scaledEMAAlpha(0.3, interval), 60)
		}
		want := 60 - 10*math.Pow(0.7, 12)
		if math.Abs(e.value-want) > 1e-9 {
			t.Fatalf("interval=%v EMA=%g, want %g", interval, e.value, want)
		}
	}
}

func TestPIDConstantErrorHasSameCorrectionPerElapsedMinute(t *testing.T) {
	for _, interval := range []time.Duration{time.Second, 5 * time.Second, 15 * time.Second} {
		c := newTestController()
		c.cfg.PIDKp, c.cfg.PIDKi, c.cfg.PIDKd = 0.35, 0, 0
		c.pid.current = 40
		for elapsed := time.Duration(0); elapsed < time.Minute; elapsed += interval {
			c.automaticFanStepOverInterval(49, 49, 50, trendState{}, interval)
		}
		if math.Abs(c.pid.current-48.4) > 1e-9 {
			t.Fatalf("interval=%v duty=%g, want 48.4", interval, c.pid.current)
		}
	}
}

func TestPIDIntegralAndRiseDetectionUseElapsedTime(t *testing.T) {
	for _, interval := range []time.Duration{time.Second, 5 * time.Second} {
		c := newTestController()
		for elapsed := time.Duration(0); elapsed < 10*time.Second; elapsed += interval {
			c.automaticFanStepOverInterval(49, 49, 50, trendState{}, interval)
		}
		if math.Abs(c.pid.integral-4) > 1e-9 {
			t.Fatalf("interval=%v integral=%g, want 4", interval, c.pid.integral)
		}
	}
	for _, interval := range []time.Duration{5 * time.Second, 10 * time.Second} {
		c := newTestController()
		c.prevSmoothed, c.hasPrevSmoothed = 45, true
		temp := 45 + 2*float64(interval)/float64(controlReferenceInterval)
		_, roc := c.automaticFanStepOverInterval(temp, temp, 50, trendState{}, interval)
		if roc != 2 {
			t.Fatalf("interval=%v rate=%g, want 2C/5s", interval, roc)
		}
	}
}

func TestActualSampleElapsedAndBlindGapReset(t *testing.T) {
	c := newTestController()
	c.cfg.CheckInterval = 5 * time.Second
	start := time.Unix(1000, 0)
	if c.sampleElapsed(start) != 5*time.Second {
		t.Fatal("first sample should use configured interval")
	}
	if c.sampleElapsed(start.Add(8*time.Second)) != 8*time.Second {
		t.Fatal("actual polling delay was ignored")
	}
	c.adaptive.floor = 20
	c.pid.integral = 20
	if c.sampleElapsed(start.Add(3*time.Minute)) != 5*time.Second || c.adaptive.floor != 0 || c.pid.integral != 0 {
		t.Fatal("blind period was integrated instead of resetting evidence")
	}
}
