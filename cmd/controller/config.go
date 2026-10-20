package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultAutoTemperatureThreshold = 60
const defaultMinimumFanSpeed = 10

type fanProfile struct {
	name      string
	threshold int
	margin    int
	kp        float64
	ki        float64
	kd        float64
	boost     float64
	autoMode  bool
}

func resolveFanProfile(value string) (fanProfile, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "balanced":
		return fanProfile{"balanced", 70, 5, 0.35, 0.015, 0.4, 0.75, true}, nil
	case "quiet":
		return fanProfile{"quiet", 75, 5, 0.25, 0.01, 0.25, 0.5, true}, nil
	case "performance":
		return fanProfile{"performance", 65, 5, 0.5, 0.02, 0.5, 1.0, true}, nil
	case "manual":
		return fanProfile{"manual", 60, 3, 1.5, 0.1, 0.5, 1.0, false}, nil
	default:
		return fanProfile{}, errors.New("FAN_PROFILE must be quiet, balanced, performance, or manual")
	}
}

func (p fanProfile) apply(cfg *Config) {
	cfg.FanProfile = p.name
	cfg.CPUTemperatureThreshold = p.threshold
	cfg.AutoModeTemperatureMargin = p.margin
	cfg.PIDKp = p.kp
	cfg.PIDKi = p.ki
	cfg.PIDKd = p.kd
	cfg.RateOfChangeBoostGain = p.boost
	cfg.AutoMode = p.autoMode
}

// loadConfig parses environment variables and returns a Config struct.
func loadConfig() (Config, error) {
	overrides, err := readSettingsFile(settingsFilePath())
	if err != nil {
		return Config{}, err
	}
	return loadConfigFrom(settingsLookup(overrides))
}

// loadConfigFrom also validates UI changes without modifying process environment.
func loadConfigFrom(lookup func(string) (string, bool)) (Config, error) {
	getEnv := func(name string) string { value, _ := lookup(name); return value }
	envOrDefault := func(name, fallback string) string {
		if value := strings.TrimSpace(getEnv(name)); value != "" {
			return value
		}
		return fallback
	}
	optionalEnv := func(name string) (string, bool) {
		value, ok := lookup(name)
		value = strings.TrimSpace(value)
		return value, ok && value != ""
	}
	cfg := Config{
		IDRACHost:                        envOrDefault("IDRAC_HOST", "local"),
		IDRACUsername:                    getEnv("IDRAC_USERNAME"),
		IDRACPassword:                    getEnv("IDRAC_PASSWORD"),
		FanSpeed:                         defaultMinimumFanSpeed,
		CheckInterval:                    0, // Will be auto-calculated
		ApplyInterval:                    0, // Will be auto-calculated
		LogInterval:                      0, // Will be auto-calculated
		MaximumIPMIUnreachableDuration:   60 * time.Second,
		IPMICommandTimeout:               defaultIPMICommandTimeout,
		DisableThirdPartyPCIeCooling:     false,
		KeepThirdPartyCoolingStateOnExit: false,
		MonitoringOnlyMode:               false,
		AutoMode:                         true,
		PIDKp:                            1.5,
		PIDKi:                            0.1,
		PIDKd:                            0.5,
		PIDIntegralLimit:                 20.0,
		AutoModeFanSpeedMin:              defaultMinimumFanSpeed,
		AutoModeFanSpeedMax:              100,
		AutoModeTemperatureMargin:        3,
		FanRampUp:                        defaultFanRampUp,
		FanRampDown:                      defaultFanRampDown,
		EMAAlpha:                         0.3,
		RateOfChangeTriggerPerCycle:      2.0,
		RateOfChangeBoostGain:            1.0,
		GPUTemperatureSource:             "disabled",
		GPUTemperatureThreshold:          80,
		VerboseCycleLogging:              false,
		DashboardEnabled:                 false,
		DashboardListenAddress:           ":8080",
		DashboardSampleLimit:             300,
	}

	var err error
	profile, err := resolveFanProfile(envOrDefault("FAN_PROFILE", "balanced"))
	if err != nil {
		return cfg, err
	}
	profile.apply(&cfg)

	// Boolean flags
	cfg.MonitoringOnlyMode, err = parseBoolStrict(envOrDefault("MONITORING_ONLY_MODE", "false"))
	if err != nil {
		return cfg, fmt.Errorf("MONITORING_ONLY_MODE: %w", err)
	}
	if value, ok := optionalEnv("AUTO_MODE"); ok {
		cfg.AutoMode, err = parseBoolStrict(value)
		if err != nil {
			return cfg, fmt.Errorf("AUTO_MODE: %w", err)
		}
	}
	cfg.DisableThirdPartyPCIeCooling, err = parseBoolStrict(envOrDefault("DISABLE_THIRD_PARTY_PCIE_CARD_DELL_DEFAULT_COOLING_RESPONSE", "false"))
	if err != nil {
		return cfg, fmt.Errorf("DISABLE_THIRD_PARTY_PCIE_CARD_DELL_DEFAULT_COOLING_RESPONSE: %w", err)
	}
	cfg.KeepThirdPartyCoolingStateOnExit, err = parseBoolStrict(envOrDefault("KEEP_THIRD_PARTY_PCIE_CARD_COOLING_RESPONSE_STATE_ON_EXIT", "false"))
	if err != nil {
		return cfg, fmt.Errorf("KEEP_THIRD_PARTY_PCIE_CARD_COOLING_RESPONSE_STATE_ON_EXIT: %w", err)
	}
	cfg.VerboseCycleLogging, err = parseBoolStrict(envOrDefault("VERBOSE_CYCLE_LOGGING", "false"))
	if err != nil {
		return cfg, fmt.Errorf("VERBOSE_CYCLE_LOGGING: %w", err)
	}
	cfg.DashboardEnabled, err = parseBoolStrict(envOrDefault("ENABLE_DASHBOARD", "false"))
	if err != nil {
		return cfg, fmt.Errorf("ENABLE_DASHBOARD: %w", err)
	}

	// Fan speed
	if value, ok := optionalEnv("FAN_SPEED"); ok {
		cfg.FanSpeed, err = parseFanSpeed(value)
		if err != nil {
			return cfg, fmt.Errorf("FAN_SPEED: %w", err)
		}
	}

	// Automatic duty floor (independent of the static FAN_SPEED selection).
	if value, ok := optionalEnv("AUTO_MODE_FAN_SPEED_MAX"); ok {
		cfg.AutoModeFanSpeedMax, err = parsePositiveIntInRange(value, 1, 100)
		if err != nil {
			return cfg, fmt.Errorf("AUTO_MODE_FAN_SPEED_MAX: %w", err)
		}
	}
	if value, ok := optionalEnv("AUTO_MODE_FAN_SPEED_MIN"); ok {
		cfg.AutoModeFanSpeedMin, err = parsePositiveIntInRange(value, 1, cfg.AutoModeFanSpeedMax)
		if err != nil {
			return cfg, fmt.Errorf("AUTO_MODE_FAN_SPEED_MIN: %w", err)
		}
	}

	// Temperature thresholds
	if value, ok := optionalEnv("CPU_TEMPERATURE_THRESHOLD"); ok {
		cfg.CPUTemperatureThreshold, err = parseTemperatureThreshold(value)
		if err != nil {
			return cfg, fmt.Errorf("CPU_TEMPERATURE_THRESHOLD: %w", err)
		}
	}

	cfg.GPUTemperatureSource = normalizeGPUSource(envOrDefault("GPU_TEMPERATURE_SOURCE", "disabled"))
	if cfg.GPUTemperatureSource != "disabled" && cfg.GPUTemperatureSource != "nvidia-smi" {
		return cfg, errors.New("GPU_TEMPERATURE_SOURCE must be disabled or nvidia-smi")
	}
	cfg.GPUTemperatureThreshold, err = parsePositiveIntInRange(envOrDefault("GPU_TEMPERATURE_THRESHOLD", "80"), 20, 125)
	if err != nil {
		return cfg, fmt.Errorf("GPU_TEMPERATURE_THRESHOLD: %w", err)
	}
	for _, setting := range []struct {
		name      string
		threshold int
		dest      *int
	}{
		{"CPU_CRITICAL_TEMPERATURE", cfg.CPUTemperatureThreshold, &cfg.CPUCriticalTemperature},
		{"GPU_CRITICAL_TEMPERATURE", cfg.GPUTemperatureThreshold, &cfg.GPUCriticalTemperature},
	} {
		*setting.dest = defaultCriticalTemperature(setting.threshold)
		if value, ok := optionalEnv(setting.name); ok {
			*setting.dest, err = parsePositiveIntInRange(value, setting.threshold, 125)
			if err != nil {
				return cfg, fmt.Errorf("%s: %w", setting.name, err)
			}
		}
	}

	// PID gains
	if value, ok := optionalEnv("PID_GAIN_PROPORTIONAL"); ok {
		cfg.PIDKp, err = parseFloat(value)
		if err != nil {
			return cfg, fmt.Errorf("PID_GAIN_PROPORTIONAL: %w", err)
		}
	}
	if value, ok := optionalEnv("PID_GAIN_INTEGRAL"); ok {
		cfg.PIDKi, err = parseFloat(value)
		if err != nil {
			return cfg, fmt.Errorf("PID_GAIN_INTEGRAL: %w", err)
		}
	}
	if value, ok := optionalEnv("PID_GAIN_DERIVATIVE"); ok {
		cfg.PIDKd, err = parseFloat(value)
		if err != nil {
			return cfg, fmt.Errorf("PID_GAIN_DERIVATIVE: %w", err)
		}
	}

	// PID margins and limits
	if value, ok := optionalEnv("PID_INTEGRAL_LIMIT"); ok {
		cfg.PIDIntegralLimit, err = parseFloat(value)
		if err != nil || cfg.PIDIntegralLimit < 0 {
			return cfg, errors.New("PID_INTEGRAL_LIMIT must be non-negative")
		}
	}
	if value, ok := optionalEnv("AUTO_MODE_TEMPERATURE_MARGIN"); ok {
		cfg.AutoModeTemperatureMargin, err = parsePositiveIntInRange(value, 0, 20)
		if err != nil {
			return cfg, fmt.Errorf("AUTO_MODE_TEMPERATURE_MARGIN: %w", err)
		}
	}

	// EMA and rate-of-change
	for _, setting := range []struct {
		name string
		dest *float64
	}{
		{"FAN_RAMP_UP", &cfg.FanRampUp},
		{"FAN_RAMP_DOWN", &cfg.FanRampDown},
	} {
		if value, ok := optionalEnv(setting.name); ok {
			*setting.dest, err = parseFloat(value)
			if err != nil || math.IsNaN(*setting.dest) || math.IsInf(*setting.dest, 0) || *setting.dest <= 0 || *setting.dest > 100 {
				return cfg, fmt.Errorf("%s must be a finite number in (0, 100] percentage points per second", setting.name)
			}
		}
	}

	cfg.EMAAlpha, err = parseFloat(envOrDefault("EMA_ALPHA", "0.3"))
	if err != nil {
		return cfg, fmt.Errorf("EMA_ALPHA: %w", err)
	}
	if cfg.EMAAlpha <= 0 || cfg.EMAAlpha > 1 {
		return cfg, errors.New("EMA_ALPHA must be in (0, 1]")
	}

	cfg.RateOfChangeTriggerPerCycle, err = parseFloat(envOrDefault("RATE_OF_CHANGE_TRIGGER", "2.0"))
	if err != nil {
		return cfg, fmt.Errorf("RATE_OF_CHANGE_TRIGGER: %w", err)
	}
	if value, ok := optionalEnv("RATE_OF_CHANGE_BOOST"); ok {
		cfg.RateOfChangeBoostGain, err = parseFloat(value)
		if err != nil {
			return cfg, fmt.Errorf("RATE_OF_CHANGE_BOOST: %w", err)
		}
	}

	// Local mode: verify IPMI device is available
	cfg.NetworkMode = cfg.IDRACHost != "local"
	if !cfg.NetworkMode {
		device, err := detectLocalIPMIDevice()
		if err != nil {
			return cfg, err
		}
		cfg.LocalIPMIDevice = device
	}

	// Network mode: verify credentials
	if cfg.NetworkMode {
		if cfg.IDRACUsername == "" || cfg.IDRACPassword == "" {
			return cfg, errors.New("IDRAC_USERNAME and IDRAC_PASSWORD are required in network mode")
		}
	}

	// Check interval (optional override for testing; otherwise auto-calculated)
	checkIntervalEnv := strings.TrimSpace(getEnv("CHECK_INTERVAL"))
	if checkIntervalEnv != "" {
		checkInterval, err := parseInterval(checkIntervalEnv)
		if err != nil {
			return cfg, fmt.Errorf("CHECK_INTERVAL: %w", err)
		}
		if checkInterval <= 0 {
			return cfg, errors.New("CHECK_INTERVAL must be greater than zero")
		}
		cfg.CheckInterval = checkInterval
	}

	// Log interval (optional user override; otherwise auto-calculated as 5× CHECK_INTERVAL)
	logIntervalEnv := strings.TrimSpace(getEnv("LOG_INTERVAL"))
	if logIntervalEnv != "" {
		logInterval, err := parseInterval(logIntervalEnv)
		if err != nil {
			return cfg, fmt.Errorf("LOG_INTERVAL: %w", err)
		}
		if logInterval <= 0 {
			return cfg, errors.New("LOG_INTERVAL must be greater than zero")
		}
		cfg.LogInterval = logInterval
	}

	// Network mode cap for CHECK_INTERVAL
	if cfg.NetworkMode && cfg.CheckInterval > 0 && cfg.CheckInterval > 15*time.Minute {
		return cfg, errors.New("CHECK_INTERVAL must be at most 15 minutes in network mode")
	}

	// Maximum IPMI unreachable duration
	maxIPMIUnreachable, configuredMaxIPMIUnreachable := lookup("MAXIMUM_IPMI_UNREACHABLE_DURATION")
	maxIPMIUnreachable = strings.TrimSpace(maxIPMIUnreachable)
	if configuredMaxIPMIUnreachable && maxIPMIUnreachable == "" {
		cfg.MaximumIPMIUnreachableDuration = 0
	} else if configuredMaxIPMIUnreachable {
		cfg.MaximumIPMIUnreachableDuration, err = parseInterval(maxIPMIUnreachable)
		if err != nil || cfg.MaximumIPMIUnreachableDuration <= 0 {
			return cfg, errors.New("MAXIMUM_IPMI_UNREACHABLE_DURATION must be a positive duration, or empty to disable escalation")
		}
	}
	if value, ok := optionalEnv("MAXIMUM_CONSECUTIVE_IPMI_FAILURES"); ok {
		cfg.MaximumConsecutiveIPMIFailures, err = strconv.Atoi(value)
		if err != nil || cfg.MaximumConsecutiveIPMIFailures <= 0 {
			return cfg, errors.New("MAXIMUM_CONSECUTIVE_IPMI_FAILURES must be a positive integer")
		}
	}
	if value, ok := optionalEnv("IPMI_COMMAND_TIMEOUT"); ok {
		cfg.IPMICommandTimeout, err = parseInterval(value)
		if err != nil || cfg.IPMICommandTimeout <= 0 || cfg.IPMICommandTimeout > 5*time.Minute {
			return cfg, errors.New("IPMI_COMMAND_TIMEOUT must be a positive duration of at most 5 minutes")
		}
	}

	dashboardListenAddress := strings.TrimSpace(getEnv("DASHBOARD_LISTEN_ADDRESS"))
	if dashboardListenAddress != "" {
		cfg.DashboardListenAddress = dashboardListenAddress
	}

	dashboardSampleLimit := strings.TrimSpace(getEnv("DASHBOARD_SAMPLE_LIMIT"))
	if dashboardSampleLimit != "" {
		cfg.DashboardSampleLimit, err = parsePositiveIntInRange(dashboardSampleLimit, 30, 5000)
		if err != nil {
			return cfg, fmt.Errorf("DASHBOARD_SAMPLE_LIMIT: %w", err)
		}
	}

	return cfg, nil
}

// ─────────────────────── parsing helpers ──────────────────────────

func envOrDefault(name, fallback string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	return v
}

func optionalEnv(name string) (string, bool) {
	value, ok := os.LookupEnv(name)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

func parseBoolStrict(v string) (bool, error) {
	switch strings.TrimSpace(v) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("must be exactly true or false")
	}
}

func parseFanSpeed(v string) (int, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(strings.ToLower(v), "0x") {
		n, err := strconv.ParseInt(v[2:], 16, 64)
		if err != nil {
			return 0, errors.New("invalid hexadecimal value")
		}
		if n < 0 || n > 100 {
			return 0, errors.New("must be in range 0..100")
		}
		return int(n), nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, errors.New("invalid decimal value")
	}
	if n < 0 || n > 100 {
		return 0, errors.New("must be in range 0..100")
	}
	return n, nil
}

func parseTemperatureThreshold(v string) (int, error) {
	if strings.ToLower(strings.TrimSpace(v)) == "auto" {
		return defaultAutoTemperatureThreshold, nil
	}
	return parsePositiveIntInRange(v, 20, 125)
}

func parsePositiveIntInRange(v string, min, max int) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, errors.New("must be an integer")
	}
	if n < min || n > max {
		return 0, fmt.Errorf("must be in range %d..%d", min, max)
	}
	return n, nil
}

func parseFloat(v string) (float64, error) {
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0, errors.New("must be a finite non-negative number")
	}
	return n, nil
}

func parseInterval(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if regexp.MustCompile(`^[0-9]+$`).MatchString(v) {
		n, _ := strconv.Atoi(v)
		return time.Duration(n) * time.Second, nil
	}
	if regexp.MustCompile(`^[0-9]+[smhd]$`).MatchString(v) {
		n, _ := strconv.Atoi(v[:len(v)-1])
		switch v[len(v)-1] {
		case 's':
			return time.Duration(n) * time.Second, nil
		case 'm':
			return time.Duration(n) * time.Minute, nil
		case 'h':
			return time.Duration(n) * time.Hour, nil
		case 'd':
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	return 0, errors.New("invalid duration format, use e.g. 5, 30s, 2m, 1h")
}

func normalizeGPUSource(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "nvidia", "nvidia_smi", "nvidia-smi":
		return "nvidia-smi"
	default:
		return "disabled"
	}
}

// ────────────────────── controller setup ──────────────────────────

func newController(cfg Config) *Controller {
	initial := float64(cfg.FanSpeed)
	if cfg.AutoMode {
		initial = float64(cfg.AutoModeFanSpeedMin + (cfg.AutoModeFanSpeedMax-cfg.AutoModeFanSpeedMin)/2)
	}
	return &Controller{
		cfg:                 cfg,
		pid:                 pidState{current: initial},
		ipmiFailuresAllowed: cfg.ipmiFailureLimit(),
		dashboard: dashboardState{
			history: make([]dashboardSample, 0, cfg.DashboardSampleLimit),
		},
	}
}
