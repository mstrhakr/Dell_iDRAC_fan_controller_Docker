package main

import "math"

const maxFanSpeedChangePerCycle = 10.0

// pidStep computes the next integer fan speed and returns the rate of change
// of the EMA temperature (degrees per cycle) for logging.
func (c *Controller) pidStep(smoothedTemp, threshold float64) (int, float64) {
	// Aim for (threshold - margin) to stay comfortable below the limit.
	target := threshold - float64(c.cfg.AutoModeTemperatureMargin)
	errNow := smoothedTemp - target // positive = too hot

	// An accumulated positive correction must not keep raising the fan speed
	// after the temperature reaches its target or crosses below it.
	if math.Abs(errNow) < 1 || errNow*c.pid.prevError < 0 {
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

	next := c.pid.current + delta
	if next > c.pid.current+maxFanSpeedChangePerCycle {
		next = c.pid.current + maxFanSpeedChangePerCycle
	} else if next < c.pid.current-maxFanSpeedChangePerCycle {
		next = c.pid.current - maxFanSpeedChangePerCycle
	}

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
