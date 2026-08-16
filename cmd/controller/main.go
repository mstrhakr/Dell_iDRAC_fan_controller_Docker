package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// main is the entry point.
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

// run orchestrates the main control loop.
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
	fmt.Println()

	// Auto-calculate intervals if CHECK_INTERVAL wasn't explicitly set
	if err := c.setupAutoIntervals(); err != nil {
		return fmt.Errorf("failed to auto-calculate intervals: %w", err)
	}

	c.logStartup()


	for {
		start := time.Now()
		if err := c.cycle(); err != nil {
			fmt.Fprintf(os.Stderr, "%s  error: %v\n",
				time.Now().Format("02-01-2006 15:04:05"), err)
		}
		// Sleep the remainder of the interval.
		elapsed := time.Since(start)
		if remaining := c.cfg.CheckInterval - elapsed; remaining > 0 {
			time.Sleep(remaining)
		}
	}
}

// cycle performs a single control loop: read temps, apply PID, command fans, log.
func (c *Controller) cycle() error {
	// Check if server is powered on (network mode only).
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

	// Read temperatures.
	snap, err := c.readTemperatures()
	if err != nil {
		if c.recordIPMIFailure() {
			return fmt.Errorf("iDRAC unreachable for too long")
		}
		return err
	}
	c.ipmiFailures = 0

	// Choose the hottest control source.
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

	// Update EMA.
	smoothed := c.ema.update(c.cfg.EMAAlpha, float64(controlRaw))

	// Decide fan speed and action.
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

	// Format and log.
	ts := time.Now().Format("02-01-2006 15:04:05")
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

	c.logCycle(ts, inlet, raw, ema, fan, profile, comment)
	return nil
}
