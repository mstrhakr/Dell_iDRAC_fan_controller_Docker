package main

import (
	"regexp"
	"time"
)

// Config holds all controller configuration.
type Config struct {
	IDRACHost        string
	IDRACUsername    string
	IDRACPassword    string
	NetworkMode      bool
	LocalIPMIDevice  string
	FanSpeed         int
	CheckInterval    time.Duration

	// Threshold / target
	CPUTemperatureThreshold int
	GPUTemperatureSource    string
	GPUTemperatureThreshold int

	// PID tuning
	AutoMode              bool
	PIDKp                 float64
	PIDKi                 float64
	PIDKd                 float64
	PIDIntegralLimit      float64
	AutoModeFanSpeedMin   int
	AutoModeFanSpeedMax   int
	AutoModeTemperatureMargin int

	// EMA smoothing
	EMAAlpha float64

	// Rate-of-change pre-emption
	RateOfChangeTriggerPerCycle float64
	RateOfChangeBoostGain       float64

	// Safety / misc
	MaximumIPMIUnreachableDuration   time.Duration
	DisableThirdPartyPCIeCooling     bool
	KeepThirdPartyCoolingStateOnExit bool
	MonitoringOnlyMode               bool
}

// emaState tracks a single exponential moving average.
type emaState struct {
	value  float64
	seeded bool
}

func (e *emaState) update(alpha, raw float64) float64 {
	if !e.seeded {
		e.value = raw
		e.seeded = true
	} else {
		e.value = alpha*raw + (1-alpha)*e.value
	}
	return e.value
}

// pidState keeps fully float64 controller state to preserve precision.
type pidState struct {
	prevError float64
	integral  float64
	current   float64
}

// Controller is the main fan control orchestrator.
type Controller struct {
	cfg                 Config
	pid                 pidState
	ema                 emaState
	prevSmoothed        float64
	ipmiFailures        int
	ipmiFailuresAllowed int
}

// Snapshot contains a single temperature reading cycle.
type snapshot struct {
	cpuTemps map[string]int
	inlet    *int
	exhaust  *int
	gpu      *int
}

// IPMI device paths for local mode.
var ipmiDevicePaths = []string{"/dev/ipmi0", "/dev/ipmi/0", "/dev/ipmidev/0"}

// Regex to identify CPU entities in IPMI sensor output.
var entityRegex = regexp.MustCompile(`^3\.[0-9]+$`)
