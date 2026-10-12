// SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIPMIFailureLimitPrecedenceAndDisabledEscalation(t *testing.T) {
	cfg := Config{CheckInterval: 5 * time.Second, MaximumIPMIUnreachableDuration: time.Minute}
	if cfg.ipmiFailureLimit() != 12 {
		t.Fatal("default duration did not resolve to 12 checks")
	}
	cfg.MaximumConsecutiveIPMIFailures = 3
	if cfg.ipmiFailureLimit() != 3 {
		t.Fatal("explicit count did not override duration")
	}
	cfg.MaximumConsecutiveIPMIFailures, cfg.MaximumIPMIUnreachableDuration = 0, 0
	if cfg.ipmiFailureLimit() != 0 {
		t.Fatal("empty duration did not disable escalation")
	}
}

func TestTemperatureFailuresSurviveSuccessfulPowerChecks(t *testing.T) {
	installIPMIMock(t)
	t.Setenv("MOCK_IPMI_FAILURE", "temperature")
	c := newTestController()
	c.ipmiFailuresAllowed = 2
	if err := c.cycle(); err == nil || errors.Is(err, errIPMIUnreachable) {
		t.Fatalf("first failure must be recoverable: %v", err)
	}
	if err := c.cycle(); !errors.Is(err, errIPMIUnreachable) || c.ipmiFailures != 2 {
		t.Fatalf("partial success erased failed reads: failures=%d err=%v", c.ipmiFailures, err)
	}
	t.Setenv("MOCK_POWER_STATE", "off")
	if err := c.cycle(); err != nil || c.ipmiFailures != 0 {
		t.Fatalf("observed powered-off server did not reset failures: %v", err)
	}
}

func TestFatalIPMIEscalationLeavesControlLoop(t *testing.T) {
	installIPMIMock(t)
	t.Setenv("MOCK_IPMI_FAILURE", "power")
	c := newTestController()
	c.ipmiFailuresAllowed = 1
	if err := c.controlLoop(); !errors.Is(err, errIPMIUnreachable) {
		t.Fatalf("fatal escalation was swallowed: %v", err)
	}
}

func TestFanCommandFailureIsFatal(t *testing.T) {
	installIPMIMock(t)
	t.Setenv("MOCK_IPMI_FAILURE", "control")
	c := newTestController()
	c.cfg.CPUTemperatureThreshold, c.cfg.CPUCriticalTemperature = 70, 80
	if err := c.controlLoop(); !errors.Is(err, errFanControl) {
		t.Fatalf("failed actuator was silently retried: %v", err)
	}
}

func TestIPMICommandDeadlineIsBounded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ipmitool"), []byte("#!/bin/sh\nexec /bin/sleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	c := newTestController()
	c.cfg.IPMICommandTimeout = 20 * time.Millisecond
	start := time.Now()
	_, err := c.runIPMI("chassis", "power", "status")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("command deadline was not enforced: %v", err)
	}
}

func TestMonitoringShutdownNeverCommandsFans(t *testing.T) {
	log := installIPMIMock(t)
	c := newTestController()
	c.cfg.MonitoringOnlyMode = true
	if err := c.restoreSafeCooling(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only shutdown sent a hardware command")
	}
}

func TestIPMIEscalationConfigurationValidation(t *testing.T) {
	setupRampConfigTest(t)
	t.Setenv("MAXIMUM_IPMI_UNREACHABLE_DURATION", "")
	t.Setenv("MAXIMUM_CONSECUTIVE_IPMI_FAILURES", "3")
	t.Setenv("IPMI_COMMAND_TIMEOUT", "15s")
	cfg, err := loadConfig()
	if err != nil || cfg.ipmiFailureLimit() != 3 || cfg.IPMICommandTimeout != 15*time.Second {
		t.Fatalf("valid escalation overrides rejected: %+v %v", cfg, err)
	}
	for _, key := range []string{"MAXIMUM_IPMI_UNREACHABLE_DURATION", "MAXIMUM_CONSECUTIVE_IPMI_FAILURES", "IPMI_COMMAND_TIMEOUT"} {
		t.Setenv(key, "0")
		if _, err := loadConfig(); err == nil {
			t.Fatalf("accepted zero %s", key)
		}
		t.Setenv(key, "")
	}
}
