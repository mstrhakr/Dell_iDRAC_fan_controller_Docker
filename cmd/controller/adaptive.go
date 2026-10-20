package main

import "time"

const (
	adaptiveCycleWindow     = 5 * time.Minute
	adaptiveCyclePeakDuty   = 50.0
	adaptiveReturnTolerance = 2.0
	adaptiveFloorIncrement  = 5
	adaptiveMaximumFloor    = 40
)

// adaptiveState raises the minimum after a full high-duty/cooldown cycle.
type adaptiveState struct {
	floor      int
	source     string
	target     float64
	cycleStart time.Time
}

func (c *Controller) recordAdaptiveFloor(timestamp time.Time, target float64, source string, fan float64) int {
	source = thermalControlGroup(source)
	a := &c.adaptive
	if source != a.source || target != a.target {
		*a = adaptiveState{source: source, target: target}
	}
	activeFloor := max(c.cfg.AutoModeFanSpeedMin, a.floor)
	if a.cycleStart.IsZero() {
		if fan > adaptiveCyclePeakDuty {
			a.cycleStart = timestamp
		}
		return a.floor
	}
	if timestamp.Before(a.cycleStart) || timestamp.Sub(a.cycleStart) > adaptiveCycleWindow {
		a.cycleStart = time.Time{}
		if fan > adaptiveCyclePeakDuty {
			a.cycleStart = timestamp
		}
		return a.floor
	}
	if fan > float64(activeFloor)+adaptiveReturnTolerance {
		return a.floor
	}

	floor := activeFloor + adaptiveFloorIncrement
	ceiling := min(adaptiveMaximumFloor, c.cfg.AutoModeFanSpeedMax)
	if floor > ceiling {
		floor = ceiling
	}
	if floor > c.cfg.AutoModeFanSpeedMin {
		a.floor = floor
	}
	a.cycleStart = time.Time{}
	return a.floor
}
