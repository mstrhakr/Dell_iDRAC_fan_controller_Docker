package main

import (
	"math"
	"time"
)

const (
	adaptiveObservationWindow = 10 * time.Minute
	adaptiveFloorTimeout      = 15 * time.Minute
	adaptiveFanExcursion      = 20.0
	adaptiveTemperatureSpan   = 3.0
	adaptiveRequiredReversals = 4
	adaptiveFloorIncrement    = 5
	adaptiveMaximumFloor      = 40
)

// adaptiveState tracks significant duty reversals, not per-sample noise. Only
// newly observed thermal oscillations can raise or renew the temporary floor.
type adaptiveState struct {
	floor           int
	expires         time.Time
	source          string
	target          float64
	lastObservation time.Time
	direction       int
	extremeFan      float64
	minimumTemp     float64
	maximumTemp     float64
	reversals       []time.Time
}

func (c *Controller) recordAdaptiveFloor(timestamp time.Time, temperature, target float64, source string, fan float64) int {
	a := &c.adaptive
	gapLimit := 3 * c.cfg.CheckInterval
	if gapLimit < 90*time.Second {
		gapLimit = 90 * time.Second
	}
	if source != a.source || target != a.target ||
		(!a.expires.IsZero() && !timestamp.Before(a.expires)) ||
		(!a.lastObservation.IsZero() && (timestamp.Before(a.lastObservation) || timestamp.Sub(a.lastObservation) > gapLimit)) {
		*a = adaptiveState{source: source, target: target}
	}
	if a.lastObservation.IsZero() {
		a.lastObservation = timestamp
		a.extremeFan = fan
		a.minimumTemp, a.maximumTemp = temperature, temperature
		return a.floor
	}
	a.lastObservation = timestamp
	a.minimumTemp = math.Min(a.minimumTemp, temperature)
	a.maximumTemp = math.Max(a.maximumTemp, temperature)
	oldest := timestamp.Add(-adaptiveObservationWindow)
	for len(a.reversals) > 0 && a.reversals[0].Before(oldest) {
		a.reversals = a.reversals[1:]
	}

	if a.direction == 0 {
		if math.Abs(fan-a.extremeFan) >= adaptiveFanExcursion {
			a.direction = 1
			if fan < a.extremeFan {
				a.direction = -1
			}
			a.extremeFan = fan
		}
		return a.floor
	}
	change := float64(a.direction) * (fan - a.extremeFan)
	if change > 0 {
		a.extremeFan = fan
		return a.floor
	}
	if change > -adaptiveFanExcursion {
		return a.floor
	}

	// A reversal requires both a 20-point duty excursion and a meaningful
	// thermal swing. Four reversals represent repeated heating/cooling cycles.
	if a.maximumTemp-a.minimumTemp >= adaptiveTemperatureSpan {
		a.reversals = append(a.reversals, timestamp)
	} else {
		a.reversals = nil
	}
	a.direction = -a.direction
	a.extremeFan = fan
	a.minimumTemp, a.maximumTemp = temperature, temperature
	if len(a.reversals) >= adaptiveRequiredReversals {
		floor := a.floor
		if floor < c.cfg.AutoModeFanSpeedMin {
			floor = c.cfg.AutoModeFanSpeedMin
		}
		floor += adaptiveFloorIncrement
		ceiling := adaptiveMaximumFloor
		if ceiling > c.cfg.AutoModeFanSpeedMax {
			ceiling = c.cfg.AutoModeFanSpeedMax
		}
		if floor > ceiling {
			floor = ceiling
		}
		if floor > c.cfg.AutoModeFanSpeedMin {
			a.floor = floor
			a.expires = timestamp.Add(adaptiveFloorTimeout)
		}
		a.reversals = nil // Never renew from the same set of observations.
	}
	return a.floor
}
