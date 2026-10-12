// SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
	"time"
)

func TestHottestCPUSwapPreservesThermalLearning(t *testing.T) {
	c := newTestController()
	c.prepareThermalControl("CPU 1", 70)
	c.ema.update(0.3, 60)
	c.pid.integral = 12
	c.trend.baselineReady = true
	c.adaptive.floor = 20
	if c.prepareThermalControl("CPU 2", 70) {
		t.Fatal("CPU swap changed control domain")
	}
	if !c.ema.seeded || c.pid.integral != 12 || !c.trend.baselineReady || c.adaptive.floor != 20 {
		t.Fatal("CPU swap discarded thermal learning")
	}
}

func TestControlDomainOrThresholdChangeReseedsThermalEvidence(t *testing.T) {
	for _, tc := range []struct {
		source    string
		threshold int
	}{
		{"GPU", 80},
		{"CPU 2", 75},
	} {
		c := newTestController()
		c.prepareThermalControl("CPU 1", 70)
		c.ema.update(0.3, 50)
		c.pid.integral = 20
		c.hasPrevSmoothed = true
		c.trend.baselineReady = true
		c.adaptive.floor = 20
		if !c.prepareThermalControl(tc.source, tc.threshold) {
			t.Fatal("control change was not reported")
		}
		if c.pid.current != 40 || c.pid.integral != 0 || c.hasPrevSmoothed || c.trend.baselineReady || c.adaptive.floor != 0 {
			t.Fatalf("incompatible thermal evidence retained: %+v", c)
		}
		if got := c.ema.update(0.3, 76); got != 76 {
			t.Fatalf("EMA mixed physical domains: %g", got)
		}
	}
}

func TestAdaptiveFloorLearnsAcrossAlternatingCPUSockets(t *testing.T) {
	c := newTestController()
	for i := 0; i < 6; i++ {
		source, fan, temp := "CPU 1", 20.0, 50.0
		if i%2 != 0 {
			source, fan, temp = "CPU 2", 70, 60
		}
		c.recordAdaptiveFloor(time.Unix(1000+int64(i)*30, 0), temp, 65, source, fan)
	}
	if c.adaptive.floor != 15 {
		t.Fatalf("CPU swaps prevented oscillation detection: floor=%d", c.adaptive.floor)
	}
}
