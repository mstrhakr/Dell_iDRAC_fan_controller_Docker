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
		t.Fatalf("parseFanSpeed hex failed: %v", err)
	}
	if v != 50 {
		t.Fatalf("parseFanSpeed hex = %d, want 50", v)
	}
	v, err = parseFanSpeed("20")
	if err != nil {
		t.Fatalf("parseFanSpeed decimal failed: %v", err)
	}
	if v != 20 {
		t.Fatalf("parseFanSpeed decimal = %d, want 20", v)
	}
}

func TestPIDDirection(t *testing.T) {
	c := newController(Config{
		PIDKp:                     2,
		PIDKi:                     0.5,
		PIDKd:                     1,
		AutoModeFanSpeedMin:       10,
		AutoModeFanSpeedMax:       100,
		AutoModeTemperatureMargin: 3,
		FanSpeed:                  20,
		AutoMode:                  true,
	})
	c.pid.Current = 40
	hot := c.pidStep(55, 50)
	if hot <= 40 {
		t.Fatalf("expected hot step to increase fan, got %d", hot)
	}
	cool := c.pidStep(35, 50)
	if cool > hot {
		t.Fatalf("expected cool step to not increase above hot output, got %d > %d", cool, hot)
	}
}

func TestParseTempField(t *testing.T) {
	v, ok := parseTempField("45 degrees C")
	if !ok || v != 45 {
		t.Fatalf("parseTempField failed: ok=%v v=%d", ok, v)
	}
}
