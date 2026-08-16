package main

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"
)

// detectLocalIPMIDevice finds and returns the path to the local IPMI device.
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

// measureIPMILatency samples IPMI round-trip time over N attempts.
func (c *Controller) measureIPMILatency(samples int) (time.Duration, error) {
	var latencies []time.Duration
	for i := 0; i < samples; i++ {
		start := time.Now()
		_, err := c.runIPMI("sdr", "type", "temperature")
		elapsed := time.Since(start)
		if err != nil {
			return 0, fmt.Errorf("failed to measure IPMI latency (sample %d): %w", i+1, err)
		}
		latencies = append(latencies, elapsed)
	}

	// Calculate average latency
	var totalLatency time.Duration
	for _, l := range latencies {
		totalLatency += l
	}
	avgLatency := totalLatency / time.Duration(len(latencies))
	return avgLatency, nil
}

const defaultCheckInterval = 5 * time.Second

// automaticCheckInterval uses a predictable baseline while ensuring one IPMI
// request has time to finish before the next control cycle begins.
func automaticCheckInterval(latency time.Duration) time.Duration {
	interval := defaultCheckInterval
	if latencyFloor := latency * 3; latencyFloor > interval {
		interval = latencyFloor
	}
	if interval > 60*time.Second {
		return 60 * time.Second
	}
	return interval
}

// setupAutoIntervals measures IPMI latency, then configures a predictable cadence.
func (c *Controller) setupAutoIntervals() error {
	fmt.Println("Configuring control intervals...")

	// Only measure if CHECK_INTERVAL wasn't explicitly set
	if c.cfg.CheckInterval == 0 {
		// Measure IPMI latency
		latency, err := c.measureIPMILatency(3)
		if err != nil {
			return err
		}
		c.cfg.IPMILatency = latency
		fmt.Printf("  IPMI latency (avg)              : %v\n", latency)

		c.cfg.CheckInterval = automaticCheckInterval(latency)
		fmt.Printf("  CHECK_INTERVAL (auto)           : %v (5s baseline, latency-clamped)\n", c.cfg.CheckInterval)
	} else {
		fmt.Printf("  CHECK_INTERVAL (user-set)       : %v\n", c.cfg.CheckInterval)
	}

	// Set APPLY_INTERVAL = CHECK_INTERVAL (apply on every cycle)
	c.cfg.ApplyInterval = c.cfg.CheckInterval
	fmt.Printf("  APPLY_INTERVAL (auto)            : %v (same as CHECK_INTERVAL)\n", c.cfg.ApplyInterval)

	// Calculate or use user-provided LOG_INTERVAL
	if c.cfg.LogInterval == 0 {
		// Default: 5× CHECK_INTERVAL, capped at 30 seconds
		c.cfg.LogInterval = c.cfg.CheckInterval * 5
		if c.cfg.LogInterval > 30*time.Second {
			c.cfg.LogInterval = 30 * time.Second
		}
		fmt.Printf("  LOG_INTERVAL (auto)              : %v (5× CHECK_INTERVAL, max 30s)\n", c.cfg.LogInterval)
	} else {
		fmt.Printf("  LOG_INTERVAL (user-set)          : %v\n", c.cfg.LogInterval)
	}

	// Recalculate IPMI failures allowed now that we know CHECK_INTERVAL
	if c.cfg.MaximumIPMIUnreachableDuration > 0 {
		c.ipmiFailuresAllowed = int(math.Ceil(float64(c.cfg.MaximumIPMIUnreachableDuration) / float64(c.cfg.CheckInterval)))
		if c.ipmiFailuresAllowed < 1 {
			c.ipmiFailuresAllowed = 1
		}
	}

	fmt.Println()
	return nil
}

// runIPMI executes an ipmitool command and returns its output.
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

// getServerInfo retrieves server model, manufacturer, and firmware from iDRAC.
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

// chassisPowerState returns the current power state of the chassis.
func (c *Controller) chassisPowerState() (string, error) {
	return c.runIPMI("chassis", "power", "status")
}

// applyDellDefault applies Dell's default fan control (automatic iDRAC management).
func (c *Controller) applyDellDefault() error {
	_, err := c.runIPMI("raw", "0x30", "0x30", "0x01", "0x01")
	return err
}

// applyManualSpeed sets a manual fan speed (0-100%).
func (c *Controller) applyManualSpeed(speed int) error {
	if _, err := c.runIPMI("raw", "0x30", "0x30", "0x01", "0x00"); err != nil {
		return err
	}
	_, err := c.runIPMI("raw", "0x30", "0x30", "0x02", "0xff", fmt.Sprintf("0x%02x", speed))
	return err
}

// recordIPMIFailure increments the failure counter and returns true if limit reached.
func (c *Controller) recordIPMIFailure() bool {
	c.ipmiFailures++
	return c.ipmiFailuresAllowed > 0 && c.ipmiFailures >= c.ipmiFailuresAllowed
}

// setFansToMaxSpeed sets both fans to 100% speed for emergency cooling.
func (c *Controller) setFansToMaxSpeed() error {
	// Disable IPMI automatic control first
	if _, err := c.runIPMI("raw", "0x30", "0x30", "0x01", "0x00"); err != nil {
		return fmt.Errorf("failed to disable IPMI auto mode: %w", err)
	}
	// Set fans to 100%
	_, err := c.runIPMI("raw", "0x30", "0x30", "0x02", "0xff", "0x64")
	return err
}

// safetyExit attempts to set fans to 100% before exiting.
// reason is logged to indicate why the exit occurred (signal, panic, error).
func (c *Controller) safetyExit(reason string) {
	fmt.Fprintf(os.Stderr, "Safety exit triggered by %s.\n", reason)

	// Try to set fans to max, but don't fail if IPMI is unreachable
	if err := c.setFansToMaxSpeed(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not set fans to 100%%: %v\n", err)
		fmt.Fprintf(os.Stderr, "Ensure manual fan control or reboot the server to restore iDRAC automatic cooling.\n")
	} else {
		fmt.Fprintf(os.Stderr, "Fans set to 100%% for emergency cooling.\n")
	}

	// If configured to keep third-party cooling state, restore it
	if c.cfg.KeepThirdPartyCoolingStateOnExit {
		if err := c.applyDellDefault(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not restore Dell default cooling: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Dell default cooling restored.\n")
		}
	}

	os.Exit(1)
}

// ────────────────────── helpers ───────────────────────────────

func valueAfterColon(line string) string {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
