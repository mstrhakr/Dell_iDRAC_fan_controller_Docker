package main

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ────────────────────────── configuration ──────────────────────────

type Config struct {
	IDRACHost        string
	IDRACUsername    string
	IDRACPassword    string
	NetworkMode      bool
	LocalIPMIDevice  string // resolved at startup
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
	// Temperature margin: PID targets (threshold - margin) to stay comfortable.
	// Proactive rather than reactive: fans ramp before threshold is crossed.
	AutoModeTemperatureMargin int

	// EMA smoothing: alpha in (0,1]. Lower = smoother, slower to react.
	// 0.3 is a good default (new reading contributes 30%, history 70%).
	EMAAlpha float64

	// Rate-of-change pre-emption: if EMA rises faster than TriggerPerCycle
	// degrees per cycle, add (Rate * RateBoostGain) to the PID output.
	// Lets the controller start speeding up before the threshold is crossed.
	RateOfChangeTriggerPerCycle float64
	RateOfChangeBoostGain       float64

	// Safety / misc
	MaximumIPMIUnreachableDuration   time.Duration
	DisableThirdPartyPCIeCooling     bool
	KeepThirdPartyCoolingStateOnExit bool
	MonitoringOnlyMode               bool
}

// ────────────────────────── state structs ──────────────────────────

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

// pidState keeps fully float64 controller state to preserve precision across
// many cycles with small fractional corrections.
type pidState struct {
	prevError float64
	integral  float64
	current   float64 // float64 fan speed; rounded to int on application
}

type Controller struct {
	cfg                 Config
	pid                 pidState
	ema                 emaState
	prevSmoothed        float64
	ipmiFailures        int
	ipmiFailuresAllowed int
}

// ─────────────────────────── IPMI paths ────────────────────────────

var ipmiDevicePaths = []string{"/dev/ipmi0", "/dev/ipmi/0", "/dev/ipmidev/0"}

func detectLocalIPMIDevice() (string, error) {
	for _, path := range ipmiDevicePaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf(
		"no IPMI device found at %s.\n"+
			"  To fix this on Unraid:\n"+
			"    1. Open a Terminal on the Unraid host and run:\n"+
			"         modprobe ipmi_devintf ipmi_si\n"+
			"    2. Add --device=/dev/ipmi0:/dev/ipmi0 to your docker run command.\n"+
			"  Alternatively, set IDRAC_HOST to your iDRAC IP address to use network mode.",
		strings.Join(ipmiDevicePaths, " or "),
	)
}

// ───────────────────────── sensor regex ────────────────────────────

var entityRegex = regexp.MustCompile(`^3\.[0-9]+$`)

// ────────────────────────── entry point ────────────────────────────

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}
	c := newController(cfg)
	if err := c.run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// ──────────────────────── config parsing ───────────────────────────

func loadConfig() (Config, error) {
	cfg := Config{
		IDRACHost:                        envOrDefault("IDRAC_HOST", "local"),
		IDRACUsername:                    os.Getenv("IDRAC_USERNAME"),
		IDRACPassword:                    os.Getenv("IDRAC_PASSWORD"),
		CheckInterval:                    5 * time.Second,
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
	}

	var err error
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

	cfg.FanSpeed, err = parseFanSpeed(envOrDefault("FAN_SPEED", "5"))
	if err != nil {
		return cfg, fmt.Errorf("FAN_SPEED: %w", err)
	}

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
	cfg.AutoModeTemperatureMargin, err = parsePositiveIntInRange(envOrDefault("AUTO_MODE_TEMPERATURE_MARGIN", "5"), 0, 20)
	if err != nil {
		return cfg, fmt.Errorf("AUTO_MODE_TEMPERATURE_MARGIN: %w", err)
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
	cfg.RateOfChangeBoostGain, err = parseFloat(envOrDefault("RATE_OF_CHANGE_BOOST", "2.0"))
	if err != nil {
		return cfg, fmt.Errorf("RATE_OF_CHANGE_BOOST: %w", err)
	}

	// Local mode: verify IPMI device is available before attempting anything
	cfg.NetworkMode = cfg.IDRACHost != "local"
	if !cfg.NetworkMode {
		device, err := detectLocalIPMIDevice()
		if err != nil {
			return cfg, err
		}
		cfg.LocalIPMIDevice = device
	}

	if cfg.NetworkMode {
		if cfg.IDRACUsername == "" || cfg.IDRACPassword == "" {
			return cfg, errors.New("IDRAC_USERNAME and IDRAC_PASSWORD are required in network mode")
		}
	}

	cfg.CheckInterval, err = parseInterval(envOrDefault("CHECK_INTERVAL", "5"))
	if err != nil {
		return cfg, fmt.Errorf("CHECK_INTERVAL: %w", err)
	}
	if cfg.CheckInterval <= 0 {
		return cfg, errors.New("CHECK_INTERVAL must be greater than zero")
	}
	// In network mode, cap at 15 minutes. Local IPMI is fast enough for any interval.
	if cfg.NetworkMode && cfg.CheckInterval > 15*time.Minute {
		return cfg, errors.New("CHECK_INTERVAL must be at most 15 minutes in network mode")
	}

	maxIPMIUnreachable := strings.TrimSpace(os.Getenv("MAXIMUM_IPMI_UNREACHABLE_DURATION"))
	if maxIPMIUnreachable == "" {
		cfg.MaximumIPMIUnreachableDuration = 0
	} else {
		cfg.MaximumIPMIUnreachableDuration, err = parseInterval(maxIPMIUnreachable)
		if err != nil {
			return cfg, fmt.Errorf("MAXIMUM_IPMI_UNREACHABLE_DURATION: %w", err)
		}
	}

	return cfg, nil
}

// ─────────────────────── controller setup ──────────────────────────

func newController(cfg Config) *Controller {
	initial := float64(cfg.FanSpeed)
	if cfg.AutoMode {
		initial = float64(cfg.AutoModeFanSpeedMin + (cfg.AutoModeFanSpeedMax-cfg.AutoModeFanSpeedMin)/2)
	}
	allowed := 0
	if cfg.MaximumIPMIUnreachableDuration > 0 {
		allowed = int(math.Ceil(float64(cfg.MaximumIPMIUnreachableDuration) / float64(cfg.CheckInterval)))
		if allowed < 1 {
			allowed = 1
		}
	}
	return &Controller{
		cfg:                 cfg,
		pid:                 pidState{current: initial},
		ipmiFailuresAllowed: allowed,
	}
}

// ──────────────────────── main loop ────────────────────────────────

func (c *Controller) run() error {
	model, fw, err := c.getServerInfo()
	if err != nil {
		return fmt.Errorf("could not identify server: %w", err)
	}

	fmt.Printf("Server model         : %s\n", model)
	if c.cfg.NetworkMode {
		fmt.Printf("iDRAC host           : %s\n", c.cfg.IDRACHost)
	} else {
		fmt.Printf("IPMI device          : %s (local)\n", c.cfg.LocalIPMIDevice)
	}
	fmt.Printf("iDRAC firmware       : %s\n", fw)

	if c.cfg.AutoMode {
		target := c.cfg.CPUTemperatureThreshold - c.cfg.AutoModeTemperatureMargin
		fmt.Println("Fan control mode     : AUTO (Go PID controller)")
		fmt.Printf("PID gains            : Kp=%.2f  Ki=%.2f  Kd=%.2f\n", c.cfg.PIDKp, c.cfg.PIDKi, c.cfg.PIDKd)
		fmt.Printf("Fan speed range      : %d%% – %d%%\n", c.cfg.AutoModeFanSpeedMin, c.cfg.AutoModeFanSpeedMax)
		fmt.Printf("Target temperature   : %d°C  (threshold %d°C – margin %d°C)\n",
			target, c.cfg.CPUTemperatureThreshold, c.cfg.AutoModeTemperatureMargin)
		fmt.Printf("EMA smoothing alpha  : %.2f\n", c.cfg.EMAAlpha)
		fmt.Printf("Rate-of-change guard : trigger %.1f°C/cycle, boost gain %.1f×\n",
			c.cfg.RateOfChangeTriggerPerCycle, c.cfg.RateOfChangeBoostGain)
	} else {
		fmt.Printf("Fan speed objective  : %d%%\n", c.cfg.FanSpeed)
		fmt.Printf("CPU threshold        : %d°C\n", c.cfg.CPUTemperatureThreshold)
	}
	fmt.Printf("GPU temp source      : %s  (threshold %d°C)\n", c.cfg.GPUTemperatureSource, c.cfg.GPUTemperatureThreshold)
	fmt.Printf("Check interval       : %s\n\n", c.cfg.CheckInterval)

	fmt.Println("Date & time            Inlet   Raw   EMA   Fan%   Profile                  Comment")

	for {
		start := time.Now()
		if err := c.cycle(); err != nil {
			fmt.Fprintf(os.Stderr, "%s  error: %v\n",
				time.Now().Format("02-01-2006 15:04:05"), err)
		}
		// Sleep the remainder of the interval, not a fixed sleep.
		// This keeps cycle timing accurate even when work takes variable time.
		elapsed := time.Since(start)
		if remaining := c.cfg.CheckInterval - elapsed; remaining > 0 {
			time.Sleep(remaining)
		}
	}
}

// ─────────────────────────── cycle ─────────────────────────────────

type snapshot struct {
	cpuTemps map[string]int
	inlet    *int
	exhaust  *int
	gpu      *int
}

func (c *Controller) cycle() error {
	if c.cfg.NetworkMode {
		power, err := c.chassisPowerState()
		if err != nil {
			if c.recordIPMIFailure() {
				return fmt.Errorf("iDRAC unreachable for too long")
			}
			fmt.Fprintf(os.Stderr, "%s  IPMI unreachable (%d/%d): %v\n",
				time.Now().Format("02-01-2006 15:04:05"), c.ipmiFailures, c.ipmiFailuresAllowed, err)
			return nil
		}
		c.ipmiFailures = 0
		if strings.Contains(power, "is off") {
			fmt.Printf("%s  Target server is powered off\n",
				time.Now().Format("02-01-2006 15:04:05"))
			return nil
		}
	}

	snap, err := c.readTemperatures()
	if err != nil {
		if c.recordIPMIFailure() {
			return fmt.Errorf("iDRAC unreachable for too long")
		}
		return err
	}
	c.ipmiFailures = 0

	// Choose the hottest control source relative to its own threshold.
	rawMax, cpuLabel, hasCPU := maxTemp(snap.cpuTemps)
	controlRaw := rawMax
	controlThreshold := c.cfg.CPUTemperatureThreshold
	controlLabel := cpuLabel

	if snap.gpu != nil {
		gpuDelta := *snap.gpu - c.cfg.GPUTemperatureThreshold
		cpuDelta := -9999
		if hasCPU {
			cpuDelta = rawMax - c.cfg.CPUTemperatureThreshold
		}
		if !hasCPU || gpuDelta > cpuDelta {
			controlRaw = *snap.gpu
			controlThreshold = c.cfg.GPUTemperatureThreshold
			controlLabel = "GPU"
		}
	}

	// Update EMA on the chosen control input.
	smoothed := c.ema.update(c.cfg.EMAAlpha, float64(controlRaw))

	profile := "-"
	comment := "-"
	appliedSpeed := c.pid.current

	switch {
	case c.cfg.MonitoringOnlyMode:
		profile = "monitoring-only"
		comment = "No fan control (monitoring only mode)"

	case c.cfg.AutoMode && controlLabel != "":
		prevSmoothed := c.prevSmoothed
		speed, roc := c.pidStep(smoothed, float64(controlThreshold))
		appliedSpeed = float64(speed)
		if !c.cfg.MonitoringOnlyMode {
			if err := c.applyManualSpeed(speed); err != nil {
				return err
			}
		}
		profile = fmt.Sprintf("PID Auto Mode (%d%%)", speed)
		target := float64(controlThreshold - c.cfg.AutoModeTemperatureMargin)
		switch {
		case roc >= c.cfg.RateOfChangeTriggerPerCycle:
			comment = fmt.Sprintf("Rising fast (+%.1f°C/cycle): %s raw=%d°C, boosted to %d%%",
				roc, controlLabel, controlRaw, speed)
		case smoothed > float64(controlThreshold):
			comment = fmt.Sprintf("Overtemp: %s EMA=%.1f°C > %d°C, fan→%d%%",
				controlLabel, smoothed, controlThreshold, speed)
		case smoothed > target && float64(speed) > prevSmoothed:
			comment = fmt.Sprintf("Stabilizing: %s EMA=%.1f°C, fan↑%d%%",
				controlLabel, smoothed, speed)
		case float64(speed) < appliedSpeed:
			comment = fmt.Sprintf("Cooling: %s EMA=%.1f°C, fan↓%d%%",
				controlLabel, smoothed, speed)
		default:
			comment = fmt.Sprintf("Optimal: %s EMA=%.1f°C target %.0f°C, %d%%",
				controlLabel, smoothed, target, speed)
		}
		c.prevSmoothed = smoothed

	case hasCPU && rawMax <= c.cfg.CPUTemperatureThreshold && !(snap.gpu != nil && *snap.gpu > c.cfg.GPUTemperatureThreshold):
		if !c.cfg.MonitoringOnlyMode {
			if err := c.applyManualSpeed(c.cfg.FanSpeed); err != nil {
				return err
			}
		}
		appliedSpeed = float64(c.cfg.FanSpeed)
		profile = fmt.Sprintf("User static (%d%%)", c.cfg.FanSpeed)
		comment = "Temperatures within thresholds"

	default:
		if !c.cfg.MonitoringOnlyMode {
			if err := c.applyDellDefault(); err != nil {
				return err
			}
		}
		profile = "Dell default (safety)"
		if snap.gpu != nil && *snap.gpu > c.cfg.GPUTemperatureThreshold {
			comment = fmt.Sprintf("GPU %d°C > %d°C threshold", *snap.gpu, c.cfg.GPUTemperatureThreshold)
		} else if hasCPU {
			comment = fmt.Sprintf("CPU %d°C > %d°C threshold", rawMax, c.cfg.CPUTemperatureThreshold)
		} else {
			comment = "No CPU temperature readable"
		}
	}

	inlet := "-"
	if snap.inlet != nil {
		inlet = fmt.Sprintf("%d°C", *snap.inlet)
	}
	raw := "-"
	if hasCPU || snap.gpu != nil {
		raw = fmt.Sprintf("%d°C", controlRaw)
	}
	ema := "-"
	if c.ema.seeded {
		ema = fmt.Sprintf("%.1f°C", smoothed)
	}
	fan := fmt.Sprintf("%.0f%%", appliedSpeed)

	ts := time.Now().Format("02-01-2006 15:04:05")
	fmt.Printf("%s  %-6s  %-5s  %-6s  %-5s  %-24s  %s\n",
		ts, inlet, raw, ema, fan, profile, comment)
	return nil
}

// ──────────────────────── PID step ─────────────────────────────────

// pidStep computes the next integer fan speed and returns the rate of change of
// the EMA temperature (degrees per cycle) so the caller can log it.
func (c *Controller) pidStep(smoothedTemp, threshold float64) (int, float64) {
	// Aim for (threshold - margin) to stay comfortable below the limit.
	target := threshold - float64(c.cfg.AutoModeTemperatureMargin)
	errNow := smoothedTemp - target // positive = too hot

	// Integral with symmetric anti-windup
	c.pid.integral += errNow
	lim := c.cfg.PIDIntegralLimit
	if c.pid.integral > lim {
		c.pid.integral = lim
	} else if c.pid.integral < -lim {
		c.pid.integral = -lim
	}

	// Derivative on smoothed error
	errRate := errNow - c.pid.prevError

	// Rate-of-change guard: if EMA is rising fast, pre-empt with extra boost.
	roc := smoothedTemp - c.prevSmoothed
	rocBoost := 0.0
	if roc >= c.cfg.RateOfChangeTriggerPerCycle {
		rocBoost = c.cfg.RateOfChangeBoostGain * roc
	}

	delta := c.cfg.PIDKp*errNow +
		c.cfg.PIDKi*c.pid.integral +
		c.cfg.PIDKd*errRate +
		rocBoost

	next := c.pid.current + delta

	// Clamp to allowed fan speed range
	if next < float64(c.cfg.AutoModeFanSpeedMin) {
		next = float64(c.cfg.AutoModeFanSpeedMin)
	}
	if next > float64(c.cfg.AutoModeFanSpeedMax) {
		next = float64(c.cfg.AutoModeFanSpeedMax)
	}

	c.pid.prevError = errNow
	c.pid.current = next

	return int(math.Round(next)), roc
}

// ────────────────────── temperature reading ─────────────────────────

func (c *Controller) readTemperatures() (snapshot, error) {
	out, err := c.runIPMI("sdr", "type", "temperature")
	if err != nil {
		return snapshot{}, err
	}
	snap := snapshot{cpuTemps: map[string]int{}}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "degrees") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 5 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		entity := strings.TrimSpace(fields[3])
		tempVal, ok := parseTempField(fields[4])
		if !ok {
			continue
		}
		if entityRegex.MatchString(entity) {
			snap.cpuTemps[entity] = tempVal
		}
		if strings.HasPrefix(name, "Inlet") || strings.HasPrefix(name, "Ambient") {
			v := tempVal
			snap.inlet = &v
		}
		if strings.HasPrefix(name, "Exhaust") {
			v := tempVal
			snap.exhaust = &v
		}
	}
	if c.cfg.GPUTemperatureSource == "nvidia-smi" {
		if v, ok := c.readNvidiaGPU(); ok {
			snap.gpu = &v
		}
	}
	return snap, nil
}

func parseTempField(raw string) (int, bool) {
	for _, t := range strings.Fields(raw) {
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n, true
		}
	}
	return 0, false
}

func (c *Controller) readNvidiaGPU() (int, bool) {
	cmd := exec.Command("nvidia-smi", "--query-gpu=temperature.gpu", "--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	max := -1
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	if max < 0 {
		return 0, false
	}
	return max, true
}

func maxTemp(m map[string]int) (int, string, bool) {
	if len(m) == 0 {
		return 0, "", false
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return entityInstance(keys[i]) < entityInstance(keys[j])
	})
	maxVal := math.MinInt
	maxLabel := ""
	for idx, k := range keys {
		if m[k] > maxVal {
			maxVal = m[k]
			maxLabel = fmt.Sprintf("CPU %d", idx+1)
		}
	}
	return maxVal, maxLabel, true
}

func entityInstance(entity string) int {
	parts := strings.Split(entity, ".")
	if len(parts) != 2 {
		return math.MaxInt
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return math.MaxInt
	}
	return n
}

// ─────────────────────── IPMI commands ─────────────────────────────

func (c *Controller) getServerInfo() (string, string, error) {
	fru, err := c.runIPMI("fru")
	if err != nil {
		return "", "", err
	}
	manufacturer, model := "", ""
	for _, line := range strings.Split(fru, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Product Manufacturer") {
			manufacturer = valueAfterColon(line)
		}
		if strings.HasPrefix(line, "Product Name") {
			model = valueAfterColon(line)
		}
	}
	if manufacturer == "" {
		manufacturer = "DELL"
	}
	if model == "" {
		model = "Unknown model"
	}

	mc, err := c.runIPMI("mc", "info")
	firmware := "unknown"
	if err == nil {
		for _, line := range strings.Split(mc, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Firmware Revision") {
				firmware = valueAfterColon(line)
			}
		}
	}

	return fmt.Sprintf("%s %s", manufacturer, model), firmware, nil
}

func (c *Controller) chassisPowerState() (string, error) {
	return c.runIPMI("chassis", "power", "status")
}

func (c *Controller) applyDellDefault() error {
	_, err := c.runIPMI("raw", "0x30", "0x30", "0x01", "0x01")
	return err
}

func (c *Controller) applyManualSpeed(speed int) error {
	if _, err := c.runIPMI("raw", "0x30", "0x30", "0x01", "0x00"); err != nil {
		return err
	}
	_, err := c.runIPMI("raw", "0x30", "0x30", "0x02", "0xff", fmt.Sprintf("0x%02x", speed))
	return err
}

func (c *Controller) runIPMI(args ...string) (string, error) {
	var base []string
	if c.cfg.NetworkMode {
		base = []string{"-I", "lanplus", "-H", c.cfg.IDRACHost, "-U", c.cfg.IDRACUsername, "-E"}
	} else {
		base = []string{"-I", "open"}
	}
	cmd := exec.Command("ipmitool", append(base, args...)...)
	cmd.Env = os.Environ()
	if c.cfg.NetworkMode {
		cmd.Env = append(cmd.Env, "IPMI_PASSWORD="+c.cfg.IDRACPassword)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ipmitool %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Controller) recordIPMIFailure() bool {
	c.ipmiFailures++
	return c.ipmiFailuresAllowed > 0 && c.ipmiFailures >= c.ipmiFailuresAllowed
}

// ──────────────────────── helpers ──────────────────────────────────

func valueAfterColon(line string) string {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func envOrDefault(name, fallback string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	return v
}

func normalizeGPUSource(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "nvidia", "nvidia_smi", "nvidia-smi":
		return "nvidia-smi"
	default:
		return "disabled"
	}
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

