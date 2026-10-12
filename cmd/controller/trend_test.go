// SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
	"time"
)

func TestTrendWarmupAndBaselineToleratePollingJitter(t *testing.T) {
	c := newTestController()
	start := time.Unix(1000, 0)
	boosted := false
	for i := 0; i < 30; i++ {
		timestamp := start.Add(time.Duration(i)*5*time.Second + time.Duration(i%7)*time.Millisecond)
		temp := 50.0
		if i >= 12 {
			temp = 52
		}
		trend := c.recordTrend(timestamp, temp, 65, 30)
		if i < 18 && trend.boost != 0 {
			t.Fatal("boost before a full observation window")
		}
		boosted = boosted || trend.boost > 0
	}
	if !boosted {
		t.Fatal("polling jitter prevented sustained-warming boost")
	}
	c = newTestController()
	for i := 0; i < 150; i++ {
		c.recordTrend(start.Add(time.Duration(i)*5*time.Second+time.Duration(i%7)*time.Millisecond), 50, 65, 30)
	}
	if !c.trend.baselineReady {
		t.Fatal("polling jitter prevented baseline learning")
	}
	if len(c.trend.samples) > 20 {
		t.Fatalf("trend history is unbounded: %d samples", len(c.trend.samples))
	}
}

func TestTrendGapRestartsLearning(t *testing.T) {
	c := newTestController()
	start := time.Unix(1000, 0)
	for i := 0; i < 150; i++ {
		c.recordTrend(start.Add(time.Duration(i)*5*time.Second), 50, 65, 30)
	}
	if !c.trend.baselineReady {
		t.Fatal("expected learned baseline before gap")
	}
	trend := c.recordTrend(start.Add(20*time.Minute), 60, 65, 30)
	if trend.baselineReady || trend.boost != 0 || len(trend.samples) != 1 {
		t.Fatalf("stale thermal history survived gap: %+v", trend)
	}
}
