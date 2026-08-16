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

// detectBMCRefreshRate rapid-polls the BMC for sensor changes to detect refresh rate.
// Returns the interval at which the BMC updates sensor readings.
func (c *Controller) detectBMCRefreshRate(timeout time.Duration) (time.Duration, error) {
	var firstReading *int
	defaultRefreshRate := 5 * time.Second
	pollStart := time.Now()

	for time.Since(pollStart) < timeout {
		snap, err := c.readTemperatures()
		if err != nil {
			return defaultRefreshRate, fmt.Errorf("failed to read temperatures during BMC detection: %w", err)
		}

		rawMax, _, hasReading := maxTemp(snap.cpuTemps)
		if !hasReading {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if firstReading == nil {
			firstReading = &rawMax
		} else if rawMax != *firstReading {
			// Value changed, so we detected a refresh
			refreshRate := time.Since(pollStart)
			return refreshRate, nil
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Timeout reached; return default
	return defaultRefreshRate, nil
}

// setupAutoIntervals measures IPMI latency and BMC refresh rate, then auto-calculates all intervals.
func (c *Controller) setupAutoIntervals() error {
	fmt.Println("Auto-calculating intervals...")

	// Only measure if CHECK_INTERVAL wasn't explicitly set
	if c.cfg.CheckInterval == 0 {
		// Measure IPMI latency
		latency, err := c.measureIPMILatency(3)
		if err != nil {
			return err
		}
		c.cfg.IPMILatency = latency
		fmt.Printf("  IPMI latency (avg)              : %v\n", latency)

		// Detect BMC sensor refresh rate
		fmt.Println("  Detecting BMC sensor refresh rate (up to 15 seconds)...")
		bmcRefreshRate, err := c.detectBMCRefreshRate(15 * time.Second)
		if err != nil {
			fmt.Printf("  Warning: BMC detection failed: %v (using default 5s)\n", err)
		}
		fmt.Printf("  BMC sensor refresh rate         : %v\n", bmcRefreshRate)

		// Calculate CHECK_INTERVAL = max(IPMI_LATENCY × 2.5 + safety margin, BMC_REFRESH_RATE)
		safetyMargin := 500 * time.Millisecond
		if !c.cfg.NetworkMode {
			safetyMargin = 100 * time.Millisecond
		}

		calculatedInterval := latency*5/2 + safetyMargin // 2.5x latency + safety
		if calculatedInterval < bmcRefreshRate {
			calculatedInterval = bmcRefreshRate
		}

		// Enforce min/max bounds
		if calculatedInterval < 1*time.Second {
			calculatedInterval = 1 * time.Second
		}
		if calculatedInterval > 60*time.Second {
			calculatedInterval = 60 * time.Second
		}

		c.cfg.CheckInterval = calculatedInterval
		fmt.Printf("  Calculated CHECK_INTERVAL       : %v\n", c.cfg.CheckInterval)
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

// ────────────────────── helpers ───────────────────────────────────

func valueAfterColon(line string) string {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
