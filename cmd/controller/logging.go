package main

import "fmt"

// logCycle logs a single control cycle. Later will expand to window-based aggregation.
func (c *Controller) logCycle(ts string, inlet, raw, ema, fan string, profile, comment string) {
	fmt.Printf("%s  %-6s  %-5s  %-6s  %-5s  %-24s  %s\n",
		ts, inlet, raw, ema, fan, profile, comment)
}

// logStartup prints startup configuration information.
func (c *Controller) logStartup() {
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
	fmt.Printf("Check interval       : %s\n", c.cfg.CheckInterval)
	fmt.Printf("Apply interval       : %s\n", c.cfg.ApplyInterval)
	fmt.Printf("Log interval         : %s\n\n", c.cfg.LogInterval)

	fmt.Println("Date & time            Inlet   Raw   EMA   Fan%   Profile                  Comment")
}
