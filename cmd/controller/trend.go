package main

import "time"

const (
	trendWarmupDuration  = 90 * time.Second
	trendBoostCooldown   = 30 * time.Second
	trendBoostFanCeiling = 70
	trendBoostAmount     = 5
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
	c.trend.samples = append(c.trend.samples, trendSample{timestamp: timestamp, temp: temperature})
	oldest := timestamp.Add(-trendWarmupDuration)
	for len(c.trend.samples) > 0 && c.trend.samples[0].timestamp.Before(oldest) {
		c.trend.samples = c.trend.samples[1:]
	}

	c.trend.average30 = averageTrend(c.trend.samples, timestamp.Add(-30*time.Second))
	c.trend.average60 = averageTrend(c.trend.samples, timestamp.Add(-60*time.Second))
	c.trend.average90 = averageTrend(c.trend.samples, oldest)
	c.trend.boost = 0

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
