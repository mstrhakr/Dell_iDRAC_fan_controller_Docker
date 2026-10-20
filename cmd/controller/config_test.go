// SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"testing"
	"time"
)

func TestFanMinimumDefaultsAndExplicitManualOverride(t *testing.T) {
	setupRampConfigTest(t)
	t.Setenv("FAN_SPEED", "")
	t.Setenv("AUTO_MODE_FAN_SPEED_MIN", "")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FanSpeed != 10 || cfg.AutoModeFanSpeedMin != 10 {
		t.Fatalf("unexpected duty defaults: static=%d minimum=%d", cfg.FanSpeed, cfg.AutoModeFanSpeedMin)
	}
	t.Setenv("FAN_PROFILE", "manual")
	t.Setenv("FAN_SPEED", "5")
	cfg, err = loadConfig()
	if err != nil || cfg.FanSpeed != 5 || cfg.AutoMode {
		t.Fatalf("explicit manual choice ignored: %+v %v", cfg, err)
	}
}

func TestConfiguredAutomaticMinimumAppliesToAllPolicies(t *testing.T) {
	setupRampConfigTest(t)
	t.Setenv("AUTO_MODE_FAN_SPEED_MIN", "20")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	c := newController(cfg)
	for i := 0; i < 100; i++ {
		c.automaticFanStep(30, 30, 70, trendState{})
	}
	if c.pid.current != 20 {
		t.Fatalf("PID ignored minimum: duty=%g", c.pid.current)
	}
	feedAdaptiveCycles(c, time.Unix(1000, 0), 3, controlReferenceInterval, 10, 55)
	if c.adaptive.floor != 35 {
		t.Fatalf("three completed cycles did not raise configured minimum: %d", c.adaptive.floor)
	}
}

func TestAutomaticMinimumRejectsInvalidPercentages(t *testing.T) {
	setupRampConfigTest(t)
	for _, value := range []string{"0", "-1", "101", "20.5", "quiet"} {
		t.Setenv("AUTO_MODE_FAN_SPEED_MIN", value)
		if _, err := loadConfig(); err == nil {
			t.Fatalf("accepted invalid minimum %s", value)
		}
	}
}
