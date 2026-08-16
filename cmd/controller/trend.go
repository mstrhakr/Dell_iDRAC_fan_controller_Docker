package main

import "time"

const (
	trendWarmupDuration      = 90 * time.Second
	trendBoostCooldown       = 30 * time.Second
	trendBoostFanCeiling     = 70
	trendBoostAmount         = 5
	baselineLearningDuration = 10 * time.Minute
	baselineMaximumFan       = 40
	baselineMaximumFloor     = 35
)

func averageTrend(samples []trendSample, since time.Time) float64 {
	total := 0.0
	count := 0
	for _, sample := range samples {
		if sample.timestamp.Before(since) {
			continue
		}
		total += sample.temp
		count++
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

// recordTrend maintains rolling thermal averages. A small anticipatory boost is
// only offered after a stable 90-second observation period and never near the
// controller's target or high fan speeds.
func (c *Controller) recordTrend(timestamp time.Time, temperature, target float64, fan int) trendState {
	if c.trend.startupBaseline == 0 {
		c.trend.startupBaseline = temperature
	}
	c.trend.samples = append(c.trend.samples, trendSample{timestamp: timestamp, temp: temperature})
	oldest := timestamp.Add(-trendWarmupDuration)
	for len(c.trend.samples) > 0 && c.trend.samples[0].timestamp.Before(oldest) {
		c.trend.samples = c.trend.samples[1:]
	}

	c.trend.average30 = averageTrend(c.trend.samples, timestamp.Add(-30*time.Second))
	c.trend.average60 = averageTrend(c.trend.samples, timestamp.Add(-60*time.Second))
	c.trend.average90 = averageTrend(c.trend.samples, oldest)
	c.trend.boost = 0
	c.trend.baselineFloor = 0

	stable := len(c.trend.samples) > 0 && !c.trend.samples[0].timestamp.After(oldest) &&
		temperature <= target-3 && fan <= baselineMaximumFan &&
		c.trend.average30 <= c.trend.average90+1 && c.trend.average30 >= c.trend.average90-1
	if !stable {
		c.trend.baselineStableSince = time.Time{}
	} else if c.trend.baselineStableSince.IsZero() {
		c.trend.baselineStableSince = timestamp
	} else if timestamp.Sub(c.trend.baselineStableSince) >= baselineLearningDuration {
		if !c.trend.baselineReady {
			c.trend.learnedBaseline = c.trend.average90
			c.trend.baselineReady = true
		} else {
			c.trend.learnedBaseline = 0.02*c.trend.average90 + 0.98*c.trend.learnedBaseline
		}
	}

	if c.trend.baselineReady && temperature < target && fan < trendBoostFanCeiling {
		aboveBaseline := temperature - c.trend.learnedBaseline
		if aboveBaseline > 5 {
			floor := c.cfg.AutoModeFanSpeedMin + int((aboveBaseline-5)*3)
			if floor > baselineMaximumFloor {
				floor = baselineMaximumFloor
			}
			c.trend.baselineFloor = floor
		}
	}

	if len(c.trend.samples) == 0 || c.trend.samples[0].timestamp.After(oldest) {
		return c.trend
	}
	if fan >= trendBoostFanCeiling || temperature >= target || c.trend.average30 <= c.trend.average90+0.75 {
		return c.trend
	}
	if !c.trend.lastBoost.IsZero() && timestamp.Sub(c.trend.lastBoost) < trendBoostCooldown {
		return c.trend
	}

	c.trend.boost = trendBoostAmount
	c.trend.lastBoost = timestamp
	return c.trend
}
