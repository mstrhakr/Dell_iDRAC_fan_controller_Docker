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

type Config struct {
	IDRACHost                        string
	IDRACUsername                    string
	IDRACPassword                    string
	FanSpeed                         int
	CPUTemperatureThreshold          int
	CheckInterval                    time.Duration
	MaximumIPMIUnreachableDuration   time.Duration
	DisableThirdPartyPCIeCooling     bool
	KeepThirdPartyCoolingStateOnExit bool
	MonitoringOnlyMode               bool
	AutoMode                         bool
	PIDKp                            float64
	PIDKi                            float64
	PIDKd                            float64
	AutoModeFanSpeedMin              int
	AutoModeFanSpeedMax              int
	AutoModeTemperatureMargin        int
	GPUTemperatureSource             string
	GPUTemperatureThreshold          int
}

type TemperatureSnapshot struct {
	CPUTemps map[string]int
	Inlet    *int
	Exhaust  *int
	GPU      *int
}

type PIDState struct {
	PrevError int
	Integral  int
	Current   int
}

type Controller struct {
	cfg                 Config
	pid                 PIDState
	ipmiFailures        int
	ipmiFailuresAllowed int
}

var entityRegex = regexp.MustCompile(`^3\.[0-9]+$`)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	controller := newController(cfg)
	if err := controller.run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

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
		AutoModeFanSpeedMin:              10,
		AutoModeFanSpeedMax:              100,
		AutoModeTemperatureMargin:        3,
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
	cfg.AutoModeTemperatureMargin, err = parsePositiveIntInRange(envOrDefault("AUTO_MODE_TEMPERATURE_MARGIN", "3"), 0, 20)
	if err != nil {
		return cfg, fmt.Errorf("AUTO_MODE_TEMPERATURE_MARGIN: %w", err)
	}

	cfg.CheckInterval, err = parseInterval(envOrDefault("CHECK_INTERVAL", "5"))
	if err != nil {
		return cfg, fmt.Errorf("CHECK_INTERVAL: %w", err)
	}
	if cfg.CheckInterval <= 0 {
		return cfg, errors.New("CHECK_INTERVAL must be greater than zero")
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

	if cfg.IDRACHost != "local" {
		if cfg.IDRACUsername == "" || cfg.IDRACPassword == "" {
			return cfg, errors.New("IDRAC_USERNAME and IDRAC_PASSWORD are required in network mode")
		}
	}

	return cfg, nil
}

func newController(cfg Config) *Controller {
	initial := cfg.FanSpeed
	if cfg.AutoMode {
		initial = cfg.AutoModeFanSpeedMin + (cfg.AutoModeFanSpeedMax-cfg.AutoModeFanSpeedMin)/2
	}
	allowed := 0
	if cfg.MaximumIPMIUnreachableDuration > 0 {
		allowed = int(math.Ceil(float64(cfg.MaximumIPMIUnreachableDuration) / float64(cfg.CheckInterval)))
		if allowed < 1 {
			allowed = 1
		}
	}
	return &Controller{
		cfg: cfg,
		pid: PIDState{Current: initial},
		ipmiFailuresAllowed: allowed,
	}
}

func (c *Controller) run() error {
	model, fw, err := c.getServerInfo()
	if err != nil {
		return err
	}

	fmt.Printf("Server model: %s\n", model)
	fmt.Printf("iDRAC/IPMI host: %s\n", c.cfg.IDRACHost)
	fmt.Printf("iDRAC firmware version: %s\n", fw)
	if c.cfg.AutoMode {
		fmt.Println("Fan control mode: AUTO (Go PID controller)")
		fmt.Printf("PID Gains - Proportional: %.2f, Integral: %.2f, Derivative: %.2f\n", c.cfg.PIDKp, c.cfg.PIDKi, c.cfg.PIDKd)
		fmt.Printf("Fan speed range: %d%% - %d%%\n", c.cfg.AutoModeFanSpeedMin, c.cfg.AutoModeFanSpeedMax)
	} else {
		fmt.Printf("Fan speed objective: %d%%\n", c.cfg.FanSpeed)
	}
	fmt.Printf("CPU temperature threshold: %dC\n", c.cfg.CPUTemperatureThreshold)
	fmt.Printf("GPU temperature source: %s\n", c.cfg.GPUTemperatureSource)
	fmt.Printf("GPU temperature threshold: %dC\n", c.cfg.GPUTemperatureThreshold)
	fmt.Printf("Check interval: %s\n\n", c.cfg.CheckInterval)
	fmt.Println("Date & time            Inlet  MaxCPU  GPU    Profile                    Comment")

	for {
		if err := c.cycle(); err != nil {
			fmt.Fprintf(os.Stderr, "%s  %v\n", time.Now().Format("02-01-2006 15:04:05"), err)
		}
		time.Sleep(c.cfg.CheckInterval)
	}
}

func (c *Controller) cycle() error {
	if c.cfg.IDRACHost != "local" {
		power, err := c.chassisPowerState()
		if err != nil {
			if c.recordIPMIFailure(err) {
				return fmt.Errorf("iDRAC unreachable for too long: %w", err)
			}
			return err
		}
		c.ipmiFailures = 0
		if strings.Contains(power, "is off") {
			fmt.Printf("%s  Target server is powered off\n", time.Now().Format("02-01-2006 15:04:05"))
			return nil
		}
	}

	snap, err := c.readTemperatures()
	if err != nil {
		if c.recordIPMIFailure(err) {
			return fmt.Errorf("iDRAC unreachable for too long: %w", err)
		}
		return err
	}
	c.ipmiFailures = 0

	maxCPU, cpuLabel, hasCPU := maxTemp(snap.CPUTemps)
	controlTemp := maxCPU
	controlThreshold := c.cfg.CPUTemperatureThreshold
	controlSource := cpuLabel
	if snap.GPU != nil {
		gpuDelta := *snap.GPU - c.cfg.GPUTemperatureThreshold
		cpuDelta := -9999
		if hasCPU {
			cpuDelta = maxCPU - c.cfg.CPUTemperatureThreshold
		}
		if !hasCPU || gpuDelta > cpuDelta {
			controlTemp = *snap.GPU
			controlThreshold = c.cfg.GPUTemperatureThreshold
			controlSource = "GPU"
		}
	}

	profile := "Dell default"
	comment := "No valid control temperature"
	appliedSpeed := c.cfg.FanSpeed

	if c.cfg.MonitoringOnlyMode {
		profile = "monitoring-only"
		comment = "No fan control command sent"
	} else if c.cfg.AutoMode && controlSource != "" {
		prev := c.pid.Current
		next := c.pidStep(controlTemp, controlThreshold)
		if err := c.applyManualSpeed(next); err != nil {
			return err
		}
		appliedSpeed = next
		profile = fmt.Sprintf("PID Auto Mode (%d%%)", next)
		if controlTemp > controlThreshold {
			comment = fmt.Sprintf("Overtemp: %s at %dC, increased fan", controlSource, controlTemp)
		} else if next > prev {
			comment = fmt.Sprintf("Stabilizing: %s at %dC, raised fan", controlSource, controlTemp)
		} else if next < prev {
			comment = fmt.Sprintf("Cool: %s at %dC, lowered fan", controlSource, controlTemp)
		} else {
			comment = fmt.Sprintf("Optimal: %s at %dC, maintaining", controlSource, controlTemp)
		}
	} else if hasCPU && maxCPU <= c.cfg.CPUTemperatureThreshold && !(snap.GPU != nil && *snap.GPU > c.cfg.GPUTemperatureThreshold) {
		if err := c.applyManualSpeed(c.cfg.FanSpeed); err != nil {
			return err
		}
		appliedSpeed = c.cfg.FanSpeed
		profile = fmt.Sprintf("User static profile (%d%%)", appliedSpeed)
		comment = "Temperatures are within thresholds"
	} else {
		if err := c.applyDellDefault(); err != nil {
			return err
		}
		profile = "Dell default dynamic profile"
		if hasCPU {
			comment = fmt.Sprintf("CPU hot (%dC), fallback for safety", maxCPU)
		} else {
			comment = "No CPU temperature, fallback for safety"
		}
	}

	inlet := "-"
	if snap.Inlet != nil {
		inlet = fmt.Sprintf("%dC", *snap.Inlet)
	}
	cpu := "-"
	if hasCPU {
		cpu = fmt.Sprintf("%dC", maxCPU)
	}
	gpu := "-"
	if snap.GPU != nil {
		gpu = fmt.Sprintf("%dC", *snap.GPU)
	}

	ts := time.Now().Format("02-01-2006 15:04:05")
	if strings.HasPrefix(profile, "PID Auto Mode") {
		fmt.Printf("%s  %-5s  %-6s  %-5s  %-24s  %s\n", ts, inlet, cpu, gpu, profile, comment)
	} else {
		fmt.Printf("%s  %-5s  %-6s  %-5s  %-24s  %s\n", ts, inlet, cpu, gpu, fmt.Sprintf("%s (%d%%)", profile, appliedSpeed), comment)
	}

	return nil
}

func (c *Controller) pidStep(temp, threshold int) int {
	errNow := temp - threshold
	c.pid.Integral += errNow
	if c.pid.Integral > 50 {
		c.pid.Integral = 50
	}
	if c.pid.Integral < -50 {
		c.pid.Integral = -50
	}
	errRate := errNow - c.pid.PrevError
	delta := int(math.Round(c.cfg.PIDKp*float64(errNow) + c.cfg.PIDKi*float64(c.pid.Integral) + c.cfg.PIDKd*float64(errRate)))
	next := c.pid.Current + delta
	if errNow > -c.cfg.AutoModeTemperatureMargin && next < c.pid.Current {
		next = c.pid.Current
	}
	if next < c.cfg.AutoModeFanSpeedMin {
		next = c.cfg.AutoModeFanSpeedMin
	}
	if next > c.cfg.AutoModeFanSpeedMax {
		next = c.cfg.AutoModeFanSpeedMax
	}
	c.pid.PrevError = errNow
	c.pid.Current = next
	return next
}

func (c *Controller) readTemperatures() (TemperatureSnapshot, error) {
	out, err := c.runIPMI("sdr", "type", "temperature")
	if err != nil {
		return TemperatureSnapshot{}, err
	}
	snap := TemperatureSnapshot{CPUTemps: map[string]int{}}
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
			snap.CPUTemps[entity] = tempVal
		}
		if strings.HasPrefix(name, "Inlet") || strings.HasPrefix(name, "Ambient") {
			v := tempVal
			snap.Inlet = &v
		}
		if strings.HasPrefix(name, "Exhaust") {
			v := tempVal
			snap.Exhaust = &v
		}
	}
	if c.cfg.GPUTemperatureSource == "nvidia-smi" {
		if v, ok := c.readNvidiaGPU(); ok {
			snap.GPU = &v
		}
	}
	return snap, nil
}

func parseTempField(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	tokens := strings.Fields(raw)
	if len(tokens) == 0 {
		return 0, false
	}
	for _, t := range tokens {
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

func (c *Controller) getServerInfo() (string, string, error) {
	fru, err := c.runIPMI("fru")
	if err != nil {
		return "", "", err
	}
	manufacturer := ""
	model := ""
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
	hex := fmt.Sprintf("0x%02x", speed)
	_, err := c.runIPMI("raw", "0x30", "0x30", "0x02", "0xff", hex)
	return err
}

func (c *Controller) runIPMI(args ...string) (string, error) {
	base := []string{"-I"}
	if c.cfg.IDRACHost == "local" {
		base = append(base, "open")
	} else {
		base = append(base, "lanplus", "-H", c.cfg.IDRACHost, "-U", c.cfg.IDRACUsername, "-E")
	}
	fullArgs := append(base, args...)
	cmd := exec.Command("ipmitool", fullArgs...)
	cmd.Env = os.Environ()
	if c.cfg.IDRACHost != "local" {
		cmd.Env = append(cmd.Env, "IPMI_PASSWORD="+c.cfg.IDRACPassword)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ipmitool %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Controller) recordIPMIFailure(err error) bool {
	c.ipmiFailures++
	if c.ipmiFailuresAllowed == 0 {
		return false
	}
	return c.ipmiFailures >= c.ipmiFailuresAllowed
}

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
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "nvidia", "nvidia_smi", "nvidia-smi":
		return "nvidia-smi"
	default:
		return v
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
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "auto" {
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
	if v == "" {
		return 0, errors.New("empty duration")
	}
	if regexp.MustCompile(`^[0-9]+$`).MatchString(v) {
		n, _ := strconv.Atoi(v)
		return time.Duration(n) * time.Second, nil
	}
	if regexp.MustCompile(`^[0-9]+[smhd]$`).MatchString(v) {
		n, _ := strconv.Atoi(v[:len(v)-1])
		suffix := v[len(v)-1]
		switch suffix {
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
	return 0, errors.New("invalid duration format")
}
