// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

type settingDefinition struct {
	Name    string   `json:"name"`
	Field   string   `json:"-"`
	Label   string   `json:"label"`
	Group   string   `json:"group"`
	Kind    string   `json:"kind"`
	Help    string   `json:"help"`
	Restart bool     `json:"restart"`
	Options []string `json:"options,omitempty"`
}

// Explicit allowlist: derived state and internal policy constants are not settings.
var settingDefinitions = []settingDefinition{
	{"FAN_PROFILE", "FanProfile", "Fan profile", "Control", "select", "Preset label. Use Apply preset to replace its tuning values.", false, []string{"quiet", "balanced", "performance", "manual"}},
	{"AUTO_MODE", "AutoMode", "Automatic PID control", "Control", "bool", "Off uses static duty, still protected by thermal safety.", false, nil},
	{"MONITORING_ONLY_MODE", "MonitoringOnlyMode", "Monitoring only", "Control", "bool", "No fan commands. Entering this mode restores Dell automatic cooling first.", false, nil},
	{"FAN_SPEED", "FanSpeed", "Static fan duty (%)", "Control", "number", "0–100. Validate chassis, storage and PCIe cooling before reducing duty.", false, nil},
	{"AUTO_MODE_FAN_SPEED_MIN", "AutoModeFanSpeedMin", "Automatic minimum (%)", "Control", "number", "1–100; must not exceed maximum.", false, nil},
	{"AUTO_MODE_FAN_SPEED_MAX", "AutoModeFanSpeedMax", "Automatic maximum (%)", "Control", "number", "1–100. Critical cooling always bypasses this cap.", false, nil},
	{"CPU_TEMPERATURE_THRESHOLD", "CPUTemperatureThreshold", "CPU threshold (°C)", "Thermal safety", "number", "20–125. Normal cooling threshold; not a detected hardware limit.", false, nil},
	{"CPU_CRITICAL_TEMPERATURE", "CPUCriticalTemperature", "CPU critical limit (°C)", "Thermal safety", "number", "At least CPU threshold, up to 125. Raw temperature at this limit commands 100%.", false, nil},
	{"GPU_TEMPERATURE_SOURCE", "GPUTemperatureSource", "GPU sensor", "Thermal safety", "select", "nvidia-smi requires GPU access and executable inside the container.", false, []string{"disabled", "nvidia-smi"}},
	{"GPU_TEMPERATURE_THRESHOLD", "GPUTemperatureThreshold", "GPU threshold (°C)", "Thermal safety", "number", "20–125. GPU readings use their own threshold.", false, nil},
	{"GPU_CRITICAL_TEMPERATURE", "GPUCriticalTemperature", "GPU critical limit (°C)", "Thermal safety", "number", "At least GPU threshold, up to 125. Emergency protection remains enabled.", false, nil},
	{"AUTO_MODE_TEMPERATURE_MARGIN", "AutoModeTemperatureMargin", "Target margin (°C)", "Thermal safety", "number", "0–20. Target = selected CPU/GPU threshold minus margin.", false, nil},
	{"PID_GAIN_PROPORTIONAL", "PIDKp", "Proportional gain (Kp)", "PID and smoothing", "number", "Finite non-negative gain, normalized to a 5-second cadence.", false, nil},
	{"PID_GAIN_INTEGRAL", "PIDKi", "Integral gain (Ki)", "PID and smoothing", "number", "Finite non-negative gain. Small values reduce oscillation.", false, nil},
	{"PID_GAIN_DERIVATIVE", "PIDKd", "Derivative gain (Kd)", "PID and smoothing", "number", "Finite non-negative gain.", false, nil},
	{"PID_INTEGRAL_LIMIT", "PIDIntegralLimit", "Integral limit", "PID and smoothing", "number", "Finite non-negative integral clamp. Default 20.", false, nil},
	{"EMA_ALPHA", "EMAAlpha", "EMA smoothing alpha", "PID and smoothing", "number", "Greater than 0, up to 1. Higher values respond faster.", false, nil},
	{"FAN_RAMP_UP", "FanRampUp", "Ramp up (points/s)", "PID and smoothing", "number", "Greater than 0, up to 100. Critical and overtemperature cooling bypass normal ramps.", false, nil},
	{"FAN_RAMP_DOWN", "FanRampDown", "Ramp down (points/s)", "PID and smoothing", "number", "Greater than 0, up to 100. Slower reductions retain cooling.", false, nil},
	{"RATE_OF_CHANGE_TRIGGER", "RateOfChangeTriggerPerCycle", "Rise trigger (°C/5s)", "PID and smoothing", "number", "Finite non-negative EMA rise threshold.", false, nil},
	{"RATE_OF_CHANGE_BOOST", "RateOfChangeBoostGain", "Rise boost gain", "PID and smoothing", "number", "Finite non-negative boost gain.", false, nil},
	{"CHECK_INTERVAL", "CheckInterval", "Temperature polling", "Timing and reliability", "text", "Whole seconds or 5s / 2m. Blank = automatic. Keep short for timely critical protection.", false, nil},
	{"LOG_INTERVAL", "LogInterval", "Summary and RPM interval", "Timing and reliability", "text", "Whole seconds or duration. Blank = five checks, capped at 30s.", false, nil},
	{"IPMI_COMMAND_TIMEOUT", "IPMICommandTimeout", "IPMI command timeout", "Timing and reliability", "text", "Positive whole seconds or duration, at most 5m.", false, nil},
	{"MAXIMUM_IPMI_UNREACHABLE_DURATION", "MaximumIPMIUnreachableDuration", "Failed-read escalation duration", "Timing and reliability", "text", "Positive duration. Blank disables failed-read escalation; actuator failures remain fatal.", false, nil},
	{"MAXIMUM_CONSECUTIVE_IPMI_FAILURES", "MaximumConsecutiveIPMIFailures", "Failed-read count override", "Timing and reliability", "text", "Positive integer. Blank uses duration-derived count.", false, nil},
	{"VERBOSE_CYCLE_LOGGING", "VerboseCycleLogging", "Log every cycle", "Timing and reliability", "bool", "Off emits interval summaries; critical events always log immediately.", false, nil},
	{"DASHBOARD_SAMPLE_LIMIT", "DashboardSampleLimit", "History sample limit", "Dashboard", "number", "30–5000 retained samples. Reducing this trims oldest history.", false, nil},
	{"ENABLE_DASHBOARD", "DashboardEnabled", "Enable dashboard", "Dashboard", "bool", "Restart required. Disabling removes UI access after restart.", true, nil},
	{"DASHBOARD_LISTEN_ADDRESS", "DashboardListenAddress", "Dashboard listen address", "Dashboard", "text", "host:port, e.g. :8080. Publish only on a trusted network.", true, nil},
	{"IDRAC_HOST", "IDRACHost", "iDRAC host", "Connection", "text", "local or remote IP/hostname. Restart required; old connection stays active until then.", true, nil},
	{"IDRAC_USERNAME", "IDRACUsername", "iDRAC username", "Connection", "text", "Required for network mode. Restart required.", true, nil},
	{"IDRAC_PASSWORD", "IDRACPassword", "iDRAC password", "Connection", "password", "Never returned by API. Leave blank to keep existing password. Stored in private settings file.", true, nil},
}

type controllerSettings struct {
	mu              sync.Mutex
	baseline        map[string]string
	overrides       map[string]string
	active          Config
	pending         *Config
	revision        uint64
	appliedRevision uint64
	path            string
}

func settingsFilePath() string {
	if path, ok := os.LookupEnv("CONTROLLER_SETTINGS_FILE"); ok {
		return path
	}
	return "/app/controller-settings.json"
}

func settingsLookup(overrides map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if value, ok := overrides[name]; ok {
			return value, true
		}
		return os.LookupEnv(name)
	}
}

func readSettingsFile(path string) (map[string]string, error) {
	values := map[string]string{}
	if path == "" {
		return values, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, fmt.Errorf("settings file: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("settings file: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("settings file must contain one JSON object")
	}
	if values == nil {
		return nil, errors.New("settings file must contain an object")
	}
	for name := range values {
		if !knownSetting(name) {
			return nil, fmt.Errorf("unknown setting: %s", name)
		}
	}
	return values, nil
}

func knownSetting(name string) bool {
	for _, def := range settingDefinitions {
		if def.Name == name {
			return true
		}
	}
	return false
}

func (c *Controller) initSettings() error {
	s := &c.settings
	s.path = settingsFilePath()
	values, err := readSettingsFile(s.path)
	if err != nil {
		return err
	}
	s.overrides = values
	s.baseline = map[string]string{}
	for _, def := range settingDefinitions {
		if value, ok := os.LookupEnv(def.Name); ok {
			s.baseline[def.Name] = value
		}
	}
	s.active = c.cfg
	return nil
}

func settingValue(cfg Config, def settingDefinition) string {
	value := reflect.ValueOf(cfg).FieldByName(def.Field)
	if value.Type() == reflect.TypeOf(time.Duration(0)) {
		return strconv.FormatInt(value.Int()/int64(time.Second), 10) + "s"
	}
	return fmt.Sprint(value.Interface())
}

func persistSettings(path string, values map[string]string) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	// Atomic rename in the same directory; never truncate working configuration.
	file, err := os.CreateTemp(filepath.Dir(path), ".controller-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (c *Controller) settingsResponseLocked() any {
	s := &c.settings
	values, active := map[string]string{}, map[string]string{}
	restart := []string{}
	for _, def := range settingDefinitions {
		if def.Kind == "password" {
			continue
		}
		active[def.Name] = settingValue(s.active, def)
		values[def.Name] = active[def.Name]
		if value, ok := s.baseline[def.Name]; ok {
			values[def.Name] = value
		}
		if value, ok := s.overrides[def.Name]; ok {
			values[def.Name] = value
		}
		if def.Name == "CHECK_INTERVAL" || def.Name == "LOG_INTERVAL" || def.Name == "MAXIMUM_CONSECUTIVE_IPMI_FAILURES" {
			_, baseline := s.baseline[def.Name]
			_, override := s.overrides[def.Name]
			if !baseline && !override {
				values[def.Name] = ""
			}
		}
	}
	for _, def := range settingDefinitions {
		if !def.Restart {
			continue
		}
		value, ok := s.overrides[def.Name]
		if ok && value != settingValue(s.active, def) {
			restart = append(restart, def.Name)
		}
	}
	return struct {
		Definitions     []settingDefinition `json:"definitions"`
		Values          map[string]string   `json:"values"`
		Active          map[string]string   `json:"active"`
		Revision        uint64              `json:"revision"`
		AppliedRevision uint64              `json:"applied_revision"`
		Restart         []string            `json:"restart_required"`
		Persistent      bool                `json:"persistent"`
		PasswordSet     bool                `json:"password_set"`
	}{settingDefinitions, values, active, s.revision, s.appliedRevision, restart, s.path != "", s.active.IDRACPassword != "" || s.overrides["IDRAC_PASSWORD"] != ""}
}

func writeSettingsJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

func (c *Controller) dashboardSettingsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s := &c.settings
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method == http.MethodGet {
		writeSettingsJSON(w, c.settingsResponseLocked())
		return
	}
	// Custom header + JSON + strict origin reject browser cross-site form/fetch attacks.
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || (r.Header.Get("Origin") != "" && (origin.Host != r.Host || (origin.Scheme != "http" && origin.Scheme != "https"))) || r.Header.Get("Sec-Fetch-Site") == "cross-site" || r.Header.Get("X-Controller-Settings") != "1" {
		http.Error(w, "same-origin settings request required", http.StatusForbidden)
		return
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
		return
	}
	var request struct {
		Revision *uint64           `json:"revision"`
		Changes  map[string]string `json:"changes"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid settings JSON", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "expected one JSON object", http.StatusBadRequest)
		return
	}
	if request.Revision == nil || *request.Revision != s.revision {
		http.Error(w, "Settings changed in another session. Reload settings before saving.", http.StatusConflict)
		return
	}
	if len(request.Changes) == 0 {
		http.Error(w, "no settings changes", http.StatusBadRequest)
		return
	}
	values := map[string]string{}
	for name, value := range s.overrides {
		values[name] = value
	}
	for name, value := range request.Changes {
		if !knownSetting(name) {
			http.Error(w, "unknown setting: "+name, http.StatusBadRequest)
			return
		}
		if name == "IDRAC_PASSWORD" && value == "" {
			continue
		}
		values[name] = value
	}
	lookup := func(name string) (string, bool) {
		if value, ok := values[name]; ok {
			return value, true
		}
		value, ok := s.baseline[name]
		return value, ok
	}
	candidate, err := loadConfigFrom(lookup)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, port, err := net.SplitHostPort(candidate.DashboardListenAddress); err != nil || port == "" {
		http.Error(w, "DASHBOARD_LISTEN_ADDRESS must be host:port", http.StatusBadRequest)
		return
	}
	// Never retarget a controller holding manual duty on the old server.
	candidate.IDRACHost, candidate.IDRACUsername, candidate.IDRACPassword = s.active.IDRACHost, s.active.IDRACUsername, s.active.IDRACPassword
	candidate.NetworkMode, candidate.LocalIPMIDevice = s.active.NetworkMode, s.active.LocalIPMIDevice
	candidate.DashboardEnabled, candidate.DashboardListenAddress = s.active.DashboardEnabled, s.active.DashboardListenAddress
	candidate.IPMILatency = s.active.IPMILatency
	if candidate.CheckInterval == 0 {
		candidate.CheckInterval = automaticCheckInterval(candidate.IPMILatency)
	}
	if candidate.LogInterval == 0 {
		candidate.LogInterval = min(5*candidate.CheckInterval, 30*time.Second)
	}
	candidate.ApplyInterval = candidate.CheckInterval
	// Validate live connection too, not only a staged local/network switch.
	if candidate.NetworkMode && candidate.CheckInterval > 15*time.Minute {
		http.Error(w, "CHECK_INTERVAL must be at most 15 minutes for the active network connection", http.StatusBadRequest)
		return
	}
	if err := persistSettings(s.path, values); err != nil {
		fmt.Fprintf(os.Stderr, "Could not save dashboard settings: %v\n", err)
		http.Error(w, "Could not persist settings. Check settings directory permissions and volume mount.", http.StatusInternalServerError)
		return
	}
	s.overrides, s.pending = values, &candidate
	s.revision++
	writeSettingsJSON(w, c.settingsResponseLocked())
}

// Only the control goroutine applies updates, between complete cycles.
func (c *Controller) applyPendingSettings() error {
	s := &c.settings
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return nil
	}
	if !c.cfg.MonitoringOnlyMode && s.pending.MonitoringOnlyMode {
		if err := c.applyDellDefault(); err != nil {
			return fmt.Errorf("%w: entering monitoring mode: %v", errFanControl, err)
		}
	}
	c.cfgMu.Lock()
	c.cfg = *s.pending
	c.cfgMu.Unlock()
	c.ipmiFailuresAllowed = c.cfg.ipmiFailureLimit()
	c.resetThermalEvidence()
	c.lastControlSample = time.Time{}
	c.lastFanRPMRead = time.Time{}
	c.statsMu.Lock()
	if len(c.dashboard.history) > c.cfg.DashboardSampleLimit {
		c.dashboard.history = c.dashboard.history[len(c.dashboard.history)-c.cfg.DashboardSampleLimit:]
	}
	c.statsMu.Unlock()
	s.active, s.pending, s.appliedRevision = c.cfg, nil, s.revision
	fmt.Printf("Dashboard settings revision %d applied; thermal learning reset.\n", s.revision)
	return nil
}
