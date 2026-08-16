package main

import (
	"regexp"
	"sync"
	"time"
)

// Config holds all controller configuration.
type Config struct {
	IDRACHost       string
	IDRACUsername   string
	IDRACPassword   string
	NetworkMode     bool
	LocalIPMIDevice string
	FanSpeed        int
	CheckInterval   time.Duration
	ApplyInterval   time.Duration // When to apply PID commands
	LogInterval     time.Duration // When to log aggregated stats
	IPMILatency     time.Duration // Measured at startup

	// Threshold / target
	CPUTemperatureThreshold int
	GPUTemperatureSource    string
	GPUTemperatureThreshold int

	// PID tuning
	AutoMode                  bool
	PIDKp                     float64
	PIDKi                     float64
	PIDKd                     float64
	PIDIntegralLimit          float64
	AutoModeFanSpeedMin       int
	AutoModeFanSpeedMax       int
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
	VerboseCycleLogging              bool
	DashboardEnabled                 bool
	DashboardListenAddress           string
	DashboardSampleLimit             int
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
	hasPrevSmoothed     bool
	ipmiFailures        int
	ipmiFailuresAllowed int
	statsMu             sync.RWMutex
	window              logWindow
	lastSummaryLog      time.Time
	dashboard           dashboardState
}

// Snapshot contains a single temperature reading cycle.
type snapshot struct {
	cpuTemps map[string]int
	inlet    *int
	exhaust  *int
	gpu      *int
}

// cycleSample contains one control loop sample used for logging and dashboard updates.
type cycleSample struct {
	timestamp time.Time
	inlet     *int
	raw       int
	ema       float64
	fan       int
	profile   string
	comment   string
	source    string
}

// logWindow stores aggregate metrics between summary log flushes.
type logWindow struct {
	count       int
	rawMin      int
	rawMax      int
	rawSum      int
	emaMin      float64
	emaMax      float64
	emaSum      float64
	fanMin      int
	fanMax      int
	fanSum      int
	lastProfile string
	lastComment string
	lastSource  string
}

// dashboardSample is the lightweight time-series point exposed by the dashboard API.
type dashboardSample struct {
	TimestampUnix int64   `json:"ts"`
	Raw           int     `json:"raw"`
	EMA           float64 `json:"ema"`
	Fan           int     `json:"fan"`
	Inlet         *int    `json:"inlet,omitempty"`
	Source        string  `json:"source"`
	Profile       string  `json:"profile"`
	Comment       string  `json:"comment"`
}

// dashboardState stores current and recent controller samples.
type dashboardState struct {
	current dashboardSample
	history []dashboardSample
}

// IPMI device paths for local mode.
var ipmiDevicePaths = []string{"/dev/ipmi0", "/dev/ipmi/0", "/dev/ipmidev/0"}

// Regex to identify CPU entities in IPMI sensor output.
var entityRegex = regexp.MustCompile(`^3\.[0-9]+$`)
