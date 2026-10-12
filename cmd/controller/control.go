// SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import "strings"

// thermalControlGroup separates physical control domains, not individual CPU
// labels: sockets sharing a threshold jointly determine the hottest CPU value.
func thermalControlGroup(source string) string {
	if strings.HasPrefix(source, "CPU") {
		return "CPU"
	}
	return source
}

// prepareThermalControl keeps the actuator duty but resets thermal evidence
// when its meaning changes. Mixing CPU and GPU absolute temperatures in one
// EMA can otherwise reduce cooling just as the hotter control domain takes over.
func (c *Controller) prepareThermalControl(source string, threshold int) bool {
	group := thermalControlGroup(source)
	if group == c.controlGroup && threshold == c.controlThreshold {
		return false
	}
	changed := c.controlGroup != ""
	c.controlGroup, c.controlThreshold = group, threshold
	c.resetThermalEvidence()
	return changed
}

func (c *Controller) resetThermalEvidence() {
	c.ema = emaState{}
	c.pid.prevError, c.pid.integral = 0, 0
	c.prevSmoothed, c.hasPrevSmoothed = 0, false
	c.trend = trendState{}
	c.adaptive = adaptiveState{}
}
