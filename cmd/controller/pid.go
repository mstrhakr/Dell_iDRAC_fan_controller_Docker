package main

import "math"

const (
	maxFanSpeedChangePerCycle = 10.0
	defaultFanRampUp          = 0.4
	defaultFanRampDown        = 0.2
	pidTemperatureDeadband    = 1.0
)

// pidStep computes the next integer fan speed and returns the rate of change
// of the EMA temperature (degrees per cycle) for logging.
func (c *Controller) pidStep(smoothedTemp, threshold float64) (int, float64) {
	return c.automaticFanStep(smoothedTemp, smoothedTemp, threshold, trendState{})
}

// automaticFanStep limits the combined PID and trend correction, retaining
// fractional duty changes so slow ramps still progress at short check intervals.
func (c *Controller) automaticFanStep(smoothedTemp, rawTemp, threshold float64, trend trendState) (int, float64) {
	// Aim for (threshold - margin) to stay comfortable below the limit.
	target := threshold - float64(c.cfg.AutoModeTemperatureMargin)
	errNow := smoothedTemp - target // positive = too hot

	// An accumulated positive correction must not keep raising the fan speed
	// after the temperature reaches its target or crosses below it.
	if math.Abs(errNow) < pidTemperatureDeadband || errNow*c.pid.prevError < 0 {
		c.pid.integral = 0
	} else {
		c.pid.integral += errNow
	}

	// Integral with symmetric anti-windup.
	lim := c.cfg.PIDIntegralLimit
	if c.pid.integral > lim {
		c.pid.integral = lim
	} else if c.pid.integral < -lim {
		c.pid.integral = -lim
	}

	// Derivative on smoothed error
	errRate := errNow - c.pid.prevError

	// Rate-of-change guard: if EMA is rising fast, pre-empt with extra boost.
	roc := 0.0
	if c.hasPrevSmoothed {
		roc = smoothedTemp - c.prevSmoothed
	}
	rocBoost := 0.0
	if roc >= c.cfg.RateOfChangeTriggerPerCycle {
		rocBoost = c.cfg.RateOfChangeBoostGain * roc
	}

	delta := c.cfg.PIDKp*errNow +
		c.cfg.PIDKi*c.pid.integral +
		c.cfg.PIDKd*errRate +
		rocBoost
	if math.Abs(errNow) < pidTemperatureDeadband {
		// Do not chase sensor noise around the target; rapid warming still acts.
		delta = rocBoost
	}

	next := c.pid.current + delta
	if float64(trend.baselineFloor) > next && next < trendBoostFanCeiling {
		next = float64(trend.baselineFloor)
	}
	if trend.boost > 0 && next >= c.pid.current && next < trendBoostFanCeiling {
		next = math.Min(next+float64(trend.boost), trendBoostFanCeiling)
	}
	next = math.Max(next, float64(c.adaptive.floor))

	interval := c.cfg.CheckInterval.Seconds()
	if interval <= 0 {
		interval = defaultCheckInterval.Seconds()
	}
	up, down := c.cfg.FanRampUp, c.cfg.FanRampDown
	if up <= 0 {
		up = defaultFanRampUp
	}
	if down <= 0 {
		down = defaultFanRampDown
	}
	rise := math.Min(up*interval, maxFanSpeedChangePerCycle)
	fall := math.Min(down*interval, maxFanSpeedChangePerCycle)
	if rawTemp > threshold || smoothedTemp > threshold {
		// Raw overtemperature must not wait for EMA or a quiet ramp setting.
		rise = maxFanSpeedChangePerCycle
		next = math.Max(next, c.pid.current+rise)
	} else if roc >= c.cfg.RateOfChangeTriggerPerCycle {
		// Preserve pre-emptive cooling for a genuine rapid temperature rise.
		rise = maxFanSpeedChangePerCycle
	}
	next = math.Max(c.pid.current-fall, math.Min(next, c.pid.current+rise))

	// Clamp to allowed fan speed range
	if next < float64(c.cfg.AutoModeFanSpeedMin) {
		next = float64(c.cfg.AutoModeFanSpeedMin)
	}
	if next > float64(c.cfg.AutoModeFanSpeedMax) {
		next = float64(c.cfg.AutoModeFanSpeedMax)
	}

	c.pid.prevError = errNow
	c.pid.current = next
	c.prevSmoothed = smoothedTemp
	c.hasPrevSmoothed = true

	return int(math.Round(next)), roc
}
