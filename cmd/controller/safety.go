// SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

const criticalTemperatureMargin = 10

// These defaults are policy limits, not detected CPU/GPU specifications. Users
// must choose limits below their hardware's critical temperature when needed.
func defaultCriticalTemperature(threshold int) int {
	return min(threshold+criticalTemperatureMargin, 125)
}

type thermalEmergency struct {
	source string
	raw    int
	limit  int
}

// temperatureEmergency examines all monitored raw sources independently. The
// normal PID source selection and EMA must never conceal a critical sensor.
func (cfg Config) temperatureEmergency(snap snapshot) (thermalEmergency, bool) {
	if raw, label, ok := maxTemp(snap.cpuTemps); ok && raw >= cfg.CPUCriticalTemperature {
		return thermalEmergency{label, raw, cfg.CPUCriticalTemperature}, true
	}
	if snap.gpu != nil && *snap.gpu >= cfg.GPUCriticalTemperature {
		return thermalEmergency{"GPU", *snap.gpu, cfg.GPUCriticalTemperature}, true
	}
	return thermalEmergency{}, false
}
