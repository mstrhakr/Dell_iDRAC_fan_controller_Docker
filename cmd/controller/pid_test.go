package main

import (
	"math"
	"testing"
	"time"
)

func TestAutomaticProfilesUseGentlerGains(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kp, ki float64
	}{
		{"quiet", 0.25, 0.01},
		{"balanced", 0.35, 0.015},
		{"performance", 0.5, 0.02},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile, err := resolveFanProfile(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if profile.kp != tc.kp || profile.ki != tc.ki {
				t.Fatalf("gains = %g/%g, want %g/%g", profile.kp, profile.ki, tc.kp, tc.ki)
			}
		})
	}
}

func TestPIDNormalRampsScaleWithCheckInterval(t *testing.T) {
	for _, interval := range []time.Duration{time.Second, 5 * time.Second, 10 * time.Second, time.Minute} {
		for _, hot := range []bool{false, true} {
			c := newTestController()
			c.cfg.CheckInterval = interval
			c.cfg.PIDKp = 100 // Force the rate limiter to determine the change.
			c.pid.current = 50
			temp, rate := 30.0, defaultFanRampDown
			if hot {
				temp, rate = 49, defaultFanRampUp // Above target, below threshold.
			}
			c.pidStep(temp, 50)
			want := math.Min(rate*interval.Seconds(), maxFanSpeedChangePerCycle)
			if change := math.Abs(c.pid.current - 50); math.Abs(change-want) > 1e-9 {
				t.Fatalf("interval=%v hot=%t: change=%g, want %g", interval, hot, change, want)
			}
		}
	}
}

func TestPIDDeadbandHoldsDutyDespiteSensorNoise(t *testing.T) {
	c := newTestController()
	c.pid.current = 55
	c.pid.integral = 20
	for i := 0; i < 120; i++ {
		temp := 47.0 + 0.8*math.Sin(float64(i))
		speed, _ := c.pidStep(temp, 50)
		if speed != 55 || c.pid.current != 55 || c.pid.integral != 0 {
			t.Fatalf("sample %d: speed=%d state=%+v, want steady 55%% and cleared integral", i, speed, c.pid)
		}
	}
}

func TestPIDSlowRampRetainsFractionalProgress(t *testing.T) {
	c := newTestController()
	c.cfg.CheckInterval = time.Second
	c.pid.current = 50
	for i := 0; i < 5; i++ {
		c.pidStep(30, 50)
	}
	if math.Abs(c.pid.current-49) > 1e-9 {
		t.Fatalf("fan after 5 seconds = %g, want 49", c.pid.current)
	}
}

func TestAutomaticFanRampIncludesTrendCorrections(t *testing.T) {
	for _, trend := range []trendState{{boost: trendBoostAmount}, {baselineFloor: baselineMaximumFloor}} {
		c := newTestController()
		c.pid.current = 20
		// At target the PID holds, but trend corrections would jump by 5 or 15.
		speed, _ := c.automaticFanStep(47, 47, 50, trend)
		if speed != 22 {
			t.Fatalf("trend %+v: speed=%d, want ramp-limited 22", trend, speed)
		}
	}
}

func TestAutomaticOvertemperatureBypassesSlowRamp(t *testing.T) {
	for _, tc := range []struct {
		name          string
		smoothed, raw float64
	}{
		{"raw spike before EMA", 40, 51},
		{"EMA still hot after raw cools", 51, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestController()
			c.cfg.FanRampUp = 0.01
			c.pid.current = 20
			speed, _ := c.automaticFanStep(tc.smoothed, tc.raw, 50, trendState{})
			if speed != 30 {
				t.Fatalf("overtemp speed = %d, want 30 despite slow ramp", speed)
			}
			c.pid.current = 95
			speed, _ = c.automaticFanStep(tc.smoothed, tc.raw, 50, trendState{})
			if speed != 100 {
				t.Fatalf("overtemp near maximum = %d, want 100", speed)
			}
		})
	}
}

func TestAutomaticProfilesLimitRepeatedHeatingCooling(t *testing.T) {
	for _, name := range []string{"quiet", "balanced", "performance"} {
		t.Run(name, func(t *testing.T) {
			c := newTestController()
			profile, _ := resolveFanProfile(name)
			profile.apply(&c.cfg)
			c.pid.current = 55
			target := float64(profile.threshold - profile.margin)
			for i := 0; i < 104; i++ {
				// Eight minutes of repeated warming/cooling, without overtemperature.
				temp := target + 4*math.Sin(float64(i)*2*math.Pi/36)
				prev := c.pid.current
				c.automaticFanStep(temp, temp, float64(profile.threshold), trendState{})
				change := c.pid.current - prev
				if change > 2+1e-9 || change < -1-1e-9 {
					t.Fatalf("sample %d: change=%g, want within [-1, 2]", i, change)
				}
			}
			// This replay has no thermal feedback; do not infer equilibrium from
			// it. Verify that sustained cooling releases any accumulated duty.
			for i := 0; i < 120; i++ {
				c.automaticFanStep(target-10, target-10, float64(profile.threshold), trendState{})
			}
			if c.pid.current != float64(c.cfg.AutoModeFanSpeedMin) {
				t.Fatalf("fan after sustained cooling = %g, want configured minimum", c.pid.current)
			}
		})
	}
}

func setupRampConfigTest(t *testing.T) {
	t.Helper()
	t.Setenv("IDRAC_HOST", "test-idrac")
	t.Setenv("IDRAC_USERNAME", "test")
	t.Setenv("IDRAC_PASSWORD", "test")
	for _, key := range []string{"FAN_PROFILE", "AUTO_MODE", "PID_GAIN_PROPORTIONAL", "PID_GAIN_INTEGRAL", "PID_GAIN_DERIVATIVE", "FAN_RAMP_UP", "FAN_RAMP_DOWN", "CHECK_INTERVAL", "LOG_INTERVAL"} {
		t.Setenv(key, "")
	}
}

func TestLoadConfigRampDefaultsAndOverrides(t *testing.T) {
	setupRampConfigTest(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FanRampUp != 0.4 || cfg.FanRampDown != 0.2 || cfg.PIDKp != 0.35 || cfg.PIDKi != 0.015 {
		t.Fatalf("unexpected balanced defaults: %+v", cfg)
	}
	t.Setenv("FAN_RAMP_UP", "0.8")
	t.Setenv("FAN_RAMP_DOWN", "0.1")
	t.Setenv("PID_GAIN_PROPORTIONAL", "1.2")
	t.Setenv("PID_GAIN_INTEGRAL", "0.04")
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FanRampUp != 0.8 || cfg.FanRampDown != 0.1 || cfg.PIDKp != 1.2 || cfg.PIDKi != 0.04 {
		t.Fatalf("explicit tuning overrides ignored: %+v", cfg)
	}
}

func TestLoadConfigRejectsInvalidRampRates(t *testing.T) {
	for _, key := range []string{"FAN_RAMP_UP", "FAN_RAMP_DOWN"} {
		for _, value := range []string{"0", "-1", "101", "NaN", "Inf", "fast"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				setupRampConfigTest(t)
				t.Setenv(key, value)
				if _, err := loadConfig(); err == nil {
					t.Fatalf("accepted %s=%s", key, value)
				}
			})
		}
	}
}
