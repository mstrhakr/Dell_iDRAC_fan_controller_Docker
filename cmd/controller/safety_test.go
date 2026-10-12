// SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCriticalTemperatureDefaultsAndValidation(t *testing.T) {
	setupRampConfigTest(t)
	t.Setenv("CPU_CRITICAL_TEMPERATURE", "")
	t.Setenv("GPU_CRITICAL_TEMPERATURE", "")
	for _, profile := range []string{"quiet", "balanced", "performance", "manual"} {
		t.Setenv("FAN_PROFILE", profile)
		cfg, err := loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.CPUCriticalTemperature != cfg.CPUTemperatureThreshold+10 || cfg.GPUCriticalTemperature != 90 {
			t.Fatalf("incorrect critical defaults for %s: %+v", profile, cfg)
		}
	}
	t.Setenv("FAN_PROFILE", "balanced")
	t.Setenv("CPU_CRITICAL_TEMPERATURE", "78")
	if cfg, err := loadConfig(); err != nil || cfg.CPUCriticalTemperature != 78 {
		t.Fatalf("explicit critical limit: cfg=%+v err=%v", cfg, err)
	}
	for _, value := range []string{"69", "126", "NaN"} {
		t.Setenv("CPU_CRITICAL_TEMPERATURE", value)
		if _, err := loadConfig(); err == nil {
			t.Fatalf("accepted invalid critical temperature %s", value)
		}
	}
	if defaultCriticalTemperature(120) != 125 {
		t.Fatal("critical default exceeds accepted temperature range")
	}
}

func TestCriticalSafetyChecksAllRawSensors(t *testing.T) {
	cfg := Config{CPUCriticalTemperature: 80, GPUCriticalTemperature: 90}
	gpu := 89
	snap := snapshot{cpuTemps: map[string]int{"3.1": 80, "3.2": 60}, gpu: &gpu}
	if emergency, ok := cfg.temperatureEmergency(snap); !ok || emergency.source != "CPU 1" {
		t.Fatalf("critical CPU concealed by GPU source: %+v %t", emergency, ok)
	}
	snap.cpuTemps["3.1"] = 79
	gpu = 90
	if emergency, ok := cfg.temperatureEmergency(snap); !ok || emergency.source != "GPU" {
		t.Fatalf("critical GPU missed: %+v %t", emergency, ok)
	}
	gpu = 89
	if _, ok := cfg.temperatureEmergency(snap); ok {
		t.Fatal("noncritical sensors triggered emergency")
	}
}

// installIPMIMock exercises orchestration through the real process boundary,
// without adding testing-only branches to production hardware access.
func installIPMIMock(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "commands")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$MOCK_IPMI_LOG\"\ncase \"$*\" in\n*'sdr type temperature'*) printf '%s\\n' \"$MOCK_TEMPERATURE_OUTPUT\";;\n*'chassis power status'*) echo 'Chassis Power is on';;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "ipmitool"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOCK_IPMI_LOG", log)
	t.Setenv("MOCK_TEMPERATURE_OUTPUT", "CPU1 | ok | ok | 3.1 | 80 degrees C")
	return log
}

func TestCriticalCycleBypassesRampsButNotMonitoringOnly(t *testing.T) {
	for _, monitoring := range []bool{false, true} {
		t.Run(map[bool]string{false: "control", true: "monitoring"}[monitoring], func(t *testing.T) {
			log := installIPMIMock(t)
			c := newTestController()
			c.cfg.CPUTemperatureThreshold, c.cfg.CPUCriticalTemperature = 70, 80
			c.cfg.GPUCriticalTemperature = 90
			c.cfg.MonitoringOnlyMode = monitoring
			c.cfg.FanRampUp = 0.01
			c.cfg.LogInterval = 25 * time.Second
			c.cfg.DashboardSampleLimit = 30
			c.pid.current = 10
			c.ema.update(0.3, 30)
			if err := c.cycle(); err != nil {
				t.Fatal(err)
			}
			commands, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if monitoring {
				if strings.Contains(string(commands), "raw 0x30") {
					t.Fatal("monitoring-only mode sent fan control commands")
				}
			} else if !strings.Contains(string(commands), "raw 0x30 0x30 0x02 0xff 0x64") || c.pid.current != 100 {
				t.Fatalf("critical sample did not immediately command 100%%: %s", commands)
			}
		})
	}
}
