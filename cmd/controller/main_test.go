package main

import (
	"testing"
	"time"
)

func TestParseInterval(t *testing.T) {
	cases := map[string]time.Duration{
		"15":  15 * time.Second,
		"15s": 15 * time.Second,
		"2m":  2 * time.Minute,
		"1h":  time.Hour,
		"1d":  24 * time.Hour,
	}
	for in, want := range cases {
		got, err := parseInterval(in)
		if err != nil {
			t.Fatalf("parseInterval(%q) returned error: %v", in, err)
		}
		if got != want {
			t.Fatalf("parseInterval(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseFanSpeed(t *testing.T) {
	v, err := parseFanSpeed("0x32")
	if err != nil {
		t.Fatalf("parseFanSpeed hex: %v", err)
	}
	if v != 50 {
		t.Fatalf("parseFanSpeed hex = %d, want 50", v)
	}
	v, err = parseFanSpeed("20")
	if err != nil {
		t.Fatalf("parseFanSpeed decimal: %v", err)
	}
	if v != 20 {
		t.Fatalf("parseFanSpeed decimal = %d, want 20", v)
	}
}

func TestParseBoolStrict(t *testing.T) {
	if b, err := parseBoolStrict("true"); err != nil || !b {
		t.Fatalf("expected true, got %v %v", b, err)
	}
	if b, err := parseBoolStrict("false"); err != nil || b {
		t.Fatalf("expected false, got %v %v", b, err)
	}
	if _, err := parseBoolStrict("yes"); err == nil {
		t.Fatal("expected error for 'yes'")
	}
}

// newTestController creates a controller with realistic defaults,
// no IPMI device check (NetworkMode=false would check device path).
func newTestController() *Controller {
	cfg := Config{
		PIDKp:                       2.0,
		PIDKi:                       0.5,
		PIDKd:                       1.0,
		PIDIntegralLimit:            50.0,
		AutoModeFanSpeedMin:         10,
		AutoModeFanSpeedMax:         100,
		AutoModeTemperatureMargin:   5,
		EMAAlpha:                    0.3,
		RateOfChangeTriggerPerCycle: 2.0,
		RateOfChangeBoostGain:       2.0,
		FanSpeed:                    20,
		AutoMode:                    true,
		NetworkMode:                 true, // skip local device check
	}
	c := &Controller{
		cfg: cfg,
		pid: pidState{current: 40},
	}
	return c
}

func TestPIDDirectionHot(t *testing.T) {
	c := newTestController()
	c.pid.current = 40
	c.ema.value = 45
	c.ema.seeded = true
	c.prevSmoothed = 45

	// 55°C, threshold 50 => overtemp, fan must go up
	speed, _ := c.pidStep(55.0, 50.0)
	if speed <= 40 {
		t.Fatalf("overtemp: expected fan > 40, got %d", speed)
	}
}

func TestPIDDirectionCool(t *testing.T) {
	c := newTestController()
	c.pid.current = 70
	c.ema.value = 30
	c.ema.seeded = true
	c.prevSmoothed = 30

	// 32°C, threshold 50, margin 5 => well below target 45°C, fan must go down
	speed, _ := c.pidStep(32.0, 50.0)
	if speed >= 70 {
		t.Fatalf("cool: expected fan < 70, got %d", speed)
	}
}

func TestPIDRateOfChangeBoost(t *testing.T) {
	c := newTestController()
	c.pid.current = 30
	c.ema.value = 40
	c.ema.seeded = true
	// Large rise in EMA this cycle: prevSmoothed was 37, now 40 => roc=3 > trigger 2
	c.prevSmoothed = 37.0
	c.hasPrevSmoothed = true

	speedWithROC, roc := c.pidStep(41.0, 50.0)
	if roc < c.cfg.RateOfChangeTriggerPerCycle {
		t.Fatalf("expected roc >= %.1f, got %.1f", c.cfg.RateOfChangeTriggerPerCycle, roc)
	}

	// Without the rate spike the speed should be lower
	c2 := newTestController()
	c2.pid.current = 30
	c2.ema.value = 40
	c2.ema.seeded = true
	c2.prevSmoothed = 40.0 // no roc
	speedWithoutROC, _ := c2.pidStep(41.0, 50.0)

	if speedWithROC <= speedWithoutROC {
		t.Fatalf("roc boost: expected %d > %d", speedWithROC, speedWithoutROC)
	}
}

func TestPIDFirstSampleDoesNotTriggerRateOfChangeBoost(t *testing.T) {
	c := newTestController()
	c.pid.current = 10

	speed, roc := c.pidStep(47.0, 50.0)
	if roc != 0 {
		t.Fatalf("first sample rate of change = %v, want 0", roc)
	}
	if speed >= 100 {
		t.Fatalf("first sample fan speed = %d, want below 100", speed)
	}
}

func TestPIDLimitsFanSpeedChangesPerCycle(t *testing.T) {
	c := newTestController()
	c.pid.current = 10
	c.prevSmoothed = 45
	c.hasPrevSmoothed = true

	speed, _ := c.pidStep(60.0, 50.0)
	if speed > 20 {
		t.Fatalf("fan speed changed from 10%% to %d%% in one cycle, want at most 20%%", speed)
	}
}

func TestPIDClearsIntegralAtTarget(t *testing.T) {
	c := newTestController()
	c.pid.current = 60
	c.pid.integral = c.cfg.PIDIntegralLimit
	c.pid.prevError = 2
	c.prevSmoothed = 48
	c.hasPrevSmoothed = true

	target := 50.0 - float64(c.cfg.AutoModeTemperatureMargin)
	speed, _ := c.pidStep(target, 50.0)
	if c.pid.integral != 0 {
		t.Fatalf("integral at target = %v, want 0", c.pid.integral)
	}
	if speed >= 60 {
		t.Fatalf("fan speed at target = %d%%, want it to start decreasing", speed)
	}
}

func TestEMASmoothing(t *testing.T) {
	var e emaState
	first := e.update(0.3, 50.0)
	if first != 50.0 {
		t.Fatalf("first EMA value should equal seed, got %f", first)
	}
	second := e.update(0.3, 60.0)
	// Should be 0.3*60 + 0.7*50 = 18 + 35 = 53
	if second != 53.0 {
		t.Fatalf("EMA(0.3, 60 after 50) = %f, want 53.0", second)
	}
}

func TestParseTempField(t *testing.T) {
	v, ok := parseTempField("45 degrees C")
	if !ok || v != 45 {
		t.Fatalf("parseTempField: ok=%v v=%d", ok, v)
	}
}
