package main

import (
	"fmt"
	"math"
)

func (c *Controller) initWindow(sample cycleSample) {
	c.window = logWindow{
		rawMin:      sample.raw,
		rawMax:      sample.raw,
		emaMin:      sample.ema,
		emaMax:      sample.ema,
		fanMin:      sample.fan,
		fanMax:      sample.fan,
		lastProfile: sample.profile,
		lastComment: sample.comment,
		lastSource:  sample.source,
	}
}

func (c *Controller) appendWindow(sample cycleSample) {
	if c.window.count == 0 {
		c.initWindow(sample)
	}
	c.window.count++
	if sample.raw < c.window.rawMin {
		c.window.rawMin = sample.raw
	}
	if sample.raw > c.window.rawMax {
		c.window.rawMax = sample.raw
	}
	c.window.rawSum += sample.raw
	if sample.ema < c.window.emaMin {
		c.window.emaMin = sample.ema
	}
	if sample.ema > c.window.emaMax {
		c.window.emaMax = sample.ema
	}
	c.window.emaSum += sample.ema
	if sample.fan >= 0 {
		if c.window.fanMin < 0 || sample.fan < c.window.fanMin {
			c.window.fanMin = sample.fan
		}
		if sample.fan > c.window.fanMax {
			c.window.fanMax = sample.fan
		}
		c.window.fanSum += sample.fan
	}
	c.window.lastProfile = sample.profile
	c.window.lastComment = sample.comment
	c.window.lastSource = sample.source
}

// recordSample records dashboard telemetry and writes either cycle logs or interval summaries.
func (c *Controller) recordSample(sample cycleSample) {
	c.statsMu.Lock()
	defer c.statsMu.Unlock()

	c.appendDashboardSample(sample)
	if c.cfg.VerboseCycleLogging {
		inlet := "-"
		if sample.inlet != nil {
			inlet = fmt.Sprintf("%dC", *sample.inlet)
		}
		fan := "-"
		if sample.fan >= 0 {
			fan = fmt.Sprintf("%d%%", sample.fan)
		}
		fmt.Printf("%s  inlet=%-4s raw=%3dC ema=%5.1fC fan=%-4s src=%-6s profile=%-24s %s\n",
			sample.timestamp.Format("02-01-2006 15:04:05"), inlet, sample.raw, sample.ema, fan, sample.source, sample.profile, sample.comment)
		return
	}

	c.appendWindow(sample)
	if c.lastSummaryLog.IsZero() {
		c.lastSummaryLog = sample.timestamp
		return
	}
	if sample.timestamp.Sub(c.lastSummaryLog) < c.cfg.LogInterval {
		return
	}

	count := float64(c.window.count)
	rawAvg := float64(c.window.rawSum) / count
	emaAvg := c.window.emaSum / count
	fanSummary := "fan=--"
	if c.window.fanMin >= 0 {
		fanSummary = fmt.Sprintf("fan=min/avg/max %d/%.0f/%d%%", c.window.fanMin, float64(c.window.fanSum)/count, c.window.fanMax)
	}
	fmt.Printf("%s  window=%s samples=%d raw=min/avg/max %d/%.1f/%dC ema=min/avg/max %.1f/%.1f/%.1fC %s src=%s profile=%s note=%s\n",
		sample.timestamp.Format("02-01-2006 15:04:05"), c.cfg.LogInterval, c.window.count,
		c.window.rawMin, rawAvg, c.window.rawMax, c.window.emaMin, emaAvg, c.window.emaMax,
		fanSummary, c.window.lastSource, c.window.lastProfile, c.window.lastComment)
	c.window = logWindow{}
	c.lastSummaryLog = sample.timestamp
}

func (c *Controller) logStartup() {
	if c.cfg.AutoMode {
		target := c.cfg.CPUTemperatureThreshold - c.cfg.AutoModeTemperatureMargin
		fmt.Println("Fan control mode     : AUTO (Go PID controller)")
		fmt.Printf("PID gains            : Kp=%.2f  Ki=%.2f  Kd=%.2f\n", c.cfg.PIDKp, c.cfg.PIDKi, c.cfg.PIDKd)
		fmt.Printf("Fan speed range      : %d%% - %d%%\n", c.cfg.AutoModeFanSpeedMin, c.cfg.AutoModeFanSpeedMax)
		fmt.Printf("Target temperature   : %dC  (threshold %dC - margin %dC)\n", target, c.cfg.CPUTemperatureThreshold, c.cfg.AutoModeTemperatureMargin)
		fmt.Printf("EMA smoothing alpha  : %.2f\n", c.cfg.EMAAlpha)
		fmt.Printf("Rate-of-change guard : trigger %.1fC/cycle, boost gain %.1fx\n", c.cfg.RateOfChangeTriggerPerCycle, c.cfg.RateOfChangeBoostGain)
	} else {
		fmt.Printf("Fan speed objective  : %d%%\n", c.cfg.FanSpeed)
		fmt.Printf("CPU threshold        : %d°C\n", c.cfg.CPUTemperatureThreshold)
	}
	fmt.Printf("GPU temp source      : %s  (threshold %d°C)\n", c.cfg.GPUTemperatureSource, c.cfg.GPUTemperatureThreshold)
	fmt.Printf("Check interval       : %s\n", c.cfg.CheckInterval)
	fmt.Printf("Apply interval       : %s\n", c.cfg.ApplyInterval)
	fmt.Printf("Log interval         : %s\n", c.cfg.LogInterval)
	if c.cfg.VerboseCycleLogging {
		fmt.Println("Logging mode         : cycle (verbose)")
	} else {
		fmt.Println("Logging mode         : summary window")
	}
	if c.cfg.DashboardEnabled {
		fmt.Printf("Dashboard            : enabled (%s)\n", c.cfg.DashboardListenAddress)
	} else {
		fmt.Println("Dashboard            : disabled")
	}
	fmt.Println()
	if c.cfg.VerboseCycleLogging {
		fmt.Println("Date & time            inlet  raw   ema    fan   src     profile                  comment")
	} else {
		fmt.Println("Date & time            summary logs emitted once per LOG_INTERVAL window")
	}
}

func (c *Controller) safeRound(value float64) int {
	if math.IsNaN(value) {
		return -1
	}
	return int(math.Round(value))
}
