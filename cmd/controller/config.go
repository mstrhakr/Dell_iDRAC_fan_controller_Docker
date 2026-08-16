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

// loadConfig parses environment variables and returns a Config struct.
func loadConfig() (Config, error) {
	cfg := Config{
		IDRACHost:                        envOrDefault("IDRAC_HOST", "local"),
		IDRACUsername:                    os.Getenv("IDRAC_USERNAME"),
		IDRACPassword:                    os.Getenv("IDRAC_PASSWORD"),
		CheckInterval:                    0, // Will be auto-calculated
		ApplyInterval:                    0, // Will be auto-calculated
		LogInterval:                      0, // Will be auto-calculated
		MaximumIPMIUnreachableDuration:   60 * time.Second,
		DisableThirdPartyPCIeCooling:     false,
		KeepThirdPartyCoolingStateOnExit: false,
		MonitoringOnlyMode:               false,
		AutoMode:                         false,
		PIDKp:                            2.0,
		PIDKi:                            0.5,
		PIDKd:                            1.0,
		PIDIntegralLimit:                 50.0,
		AutoModeFanSpeedMin:              10,
		AutoModeFanSpeedMax:              100,
		AutoModeTemperatureMargin:        5,
		EMAAlpha:                         0.3,
		RateOfChangeTriggerPerCycle:      2.0,
		RateOfChangeBoostGain:            2.0,
		GPUTemperatureSource:             "disabled",
		GPUTemperatureThreshold:          80,
		VerboseCycleLogging:              false,
		DashboardEnabled:                 false,
		DashboardListenAddress:           ":8080",
		DashboardSampleLimit:             300,
	}

	var err error

	// Boolean flags
	cfg.MonitoringOnlyMode, err = parseBoolStrict(envOrDefault("MONITORING_ONLY_MODE", "false"))
	if err != nil {
		return cfg, fmt.Errorf("MONITORING_ONLY_MODE: %w", err)
	}
	cfg.AutoMode, err = parseBoolStrict(envOrDefault("AUTO_MODE", "false"))
	if err != nil {
		return cfg, fmt.Errorf("AUTO_MODE: %w", err)
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
	cfg.FanSpeed, err = parseFanSpeed(envOrDefault("FAN_SPEED", "5"))
	if err != nil {
		return cfg, fmt.Errorf("FAN_SPEED: %w", err)
	}

	// Temperature thresholds
	cfg.CPUTemperatureThreshold, err = parseTemperatureThreshold(envOrDefault("CPU_TEMPERATURE_THRESHOLD", "auto"))
	if err != nil {
		return cfg, fmt.Errorf("CPU_TEMPERATURE_THRESHOLD: %w", err)
	}

	cfg.GPUTemperatureSource = normalizeGPUSource(envOrDefault("GPU_TEMPERATURE_SOURCE", "disabled"))
	if cfg.GPUTemperatureSource != "disabled" && cfg.GPUTemperatureSource != "nvidia-smi" {
		return cfg, errors.New("GPU_TEMPERATURE_SOURCE must be disabled or nvidia-smi")
	}
	cfg.GPUTemperatureThreshold, err = parsePositiveIntInRange(envOrDefault("GPU_TEMPERATURE_THRESHOLD", "80"), 20, 125)
	if err != nil {
		return cfg, fmt.Errorf("GPU_TEMPERATURE_THRESHOLD: %w", err)
	}

	// PID gains
	cfg.PIDKp, err = parseFloat(envOrDefault("PID_GAIN_PROPORTIONAL", "2.0"))
	if err != nil {
		return cfg, fmt.Errorf("PID_GAIN_PROPORTIONAL: %w", err)
	}
	cfg.PIDKi, err = parseFloat(envOrDefault("PID_GAIN_INTEGRAL", "0.5"))
	if err != nil {
		return cfg, fmt.Errorf("PID_GAIN_INTEGRAL: %w", err)
	}
	cfg.PIDKd, err = parseFloat(envOrDefault("PID_GAIN_DERIVATIVE", "1.0"))
	if err != nil {
		return cfg, fmt.Errorf("PID_GAIN_DERIVATIVE: %w", err)
	}

	// PID margins and limits
	cfg.AutoModeTemperatureMargin, err = parsePositiveIntInRange(envOrDefault("AUTO_MODE_TEMPERATURE_MARGIN", "5"), 0, 20)
	if err != nil {
		return cfg, fmt.Errorf("AUTO_MODE_TEMPERATURE_MARGIN: %w", err)
	}

	// EMA and rate-of-change
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
	cfg.RateOfChangeBoostGain, err = parseFloat(envOrDefault("RATE_OF_CHANGE_BOOST", "2.0"))
	if err != nil {
		return cfg, fmt.Errorf("RATE_OF_CHANGE_BOOST: %w", err)
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
	checkIntervalEnv := strings.TrimSpace(os.Getenv("CHECK_INTERVAL"))
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
	logIntervalEnv := strings.TrimSpace(os.Getenv("LOG_INTERVAL"))
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
	maxIPMIUnreachable := strings.TrimSpace(os.Getenv("MAXIMUM_IPMI_UNREACHABLE_DURATION"))
	if maxIPMIUnreachable == "" {
		cfg.MaximumIPMIUnreachableDuration = 0
	} else {
		cfg.MaximumIPMIUnreachableDuration, err = parseInterval(maxIPMIUnreachable)
		if err != nil {
			return cfg, fmt.Errorf("MAXIMUM_IPMI_UNREACHABLE_DURATION: %w", err)
		}
	}

	dashboardListenAddress := strings.TrimSpace(os.Getenv("DASHBOARD_LISTEN_ADDRESS"))
	if dashboardListenAddress != "" {
		cfg.DashboardListenAddress = dashboardListenAddress
	}

	dashboardSampleLimit := strings.TrimSpace(os.Getenv("DASHBOARD_SAMPLE_LIMIT"))
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
		return 50, nil
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
	if err != nil {
		return 0, errors.New("must be a number")
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
	allowed := 0
	// CheckInterval must be known to calculate failures allowed
	// If still 0 at this point, it will be calculated later
	if cfg.CheckInterval > 0 && cfg.MaximumIPMIUnreachableDuration > 0 {
		allowed = int(math.Ceil(float64(cfg.MaximumIPMIUnreachableDuration) / float64(cfg.CheckInterval)))
		if allowed < 1 {
			allowed = 1
		}
	}
	return &Controller{
		cfg:                 cfg,
		pid:                 pidState{current: initial},
		ipmiFailuresAllowed: allowed,
		dashboard: dashboardState{
			history: make([]dashboardSample, 0, cfg.DashboardSampleLimit),
		},
	}
}
