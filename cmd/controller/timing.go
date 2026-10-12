// SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"math"
	"time"
)

// Gains and legacy EMA/rise settings retain their meaning at a five-second
// reference cadence. Actual elapsed time, not configured polling, drives control.
const controlReferenceInterval = 5 * time.Second

func effectiveControlInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return controlReferenceInterval
	}
	return interval
}

func scaledEMAAlpha(alpha float64, elapsed time.Duration) float64 {
	if elapsed == controlReferenceInterval {
		return alpha
	}
	return 1 - math.Pow(1-alpha, float64(effectiveControlInterval(elapsed))/float64(controlReferenceInterval))
}

// sampleElapsed resets evidence across blind periods rather than integrating
// an unobserved temperature error for the entire outage.
func (c *Controller) sampleElapsed(timestamp time.Time) time.Duration {
	elapsed := effectiveControlInterval(c.cfg.CheckInterval)
	if !c.lastControlSample.IsZero() {
		actual := timestamp.Sub(c.lastControlSample)
		if actual > 0 && actual <= observationGapLimit(c.cfg.CheckInterval) {
			elapsed = actual
		} else {
			c.resetThermalEvidence()
		}
	}
	c.lastControlSample = timestamp
	return elapsed
}

// observationGapLimit tolerates polling jitter but never interprets a prolonged
// blind period as evidence of stable thermal operation.
func observationGapLimit(interval time.Duration) time.Duration {
	return max(90*time.Second, 3*interval)
}
