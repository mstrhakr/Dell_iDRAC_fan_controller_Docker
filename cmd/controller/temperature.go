package main

import (
	"bufio"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// readTemperatures reads all available temperatures from IPMI.
func (c *Controller) readTemperatures() (snapshot, error) {
	out, err := c.runIPMI("sdr", "type", "temperature")
	if err != nil {
		return snapshot{}, err
	}
	snap := snapshot{cpuTemps: map[string]int{}}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "degrees") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 5 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		entity := strings.TrimSpace(fields[3])
		tempVal, ok := parseTempField(fields[4])
		if !ok {
			continue
		}
		if entityRegex.MatchString(entity) {
			snap.cpuTemps[entity] = tempVal
		}
		if strings.HasPrefix(name, "Inlet") || strings.HasPrefix(name, "Ambient") {
			v := tempVal
			snap.inlet = &v
		}
		if strings.HasPrefix(name, "Exhaust") {
			v := tempVal
			snap.exhaust = &v
		}
	}
	if c.cfg.GPUTemperatureSource == "nvidia-smi" {
		if v, ok := c.readNvidiaGPU(); ok {
			snap.gpu = &v
		}
	}
	snap.fanRPMMin, snap.fanRPMMax = c.cachedFanRPMRange()
	return snap, nil
}

// cachedFanRPMRange refreshes fan RPM telemetry at the summary interval so it
// can confirm actuator behavior without doubling the control-loop IPMI traffic.
func (c *Controller) cachedFanRPMRange() (*int, *int) {
	if c.lastFanRPMRead.IsZero() || time.Since(c.lastFanRPMRead) >= c.cfg.LogInterval {
		c.lastFanRPMRead = time.Now()
		if out, err := c.runIPMI("sdr", "type", "fan"); err == nil {
			if min, max, ok := parseFanRPMRange(out); ok {
				c.fanRPMMin = &min
				c.fanRPMMax = &max
			}
		}
	}
	return intPointerCopy(c.fanRPMMin), intPointerCopy(c.fanRPMMax)
}

func parseFanRPMRange(output string) (int, int, bool) {
	min, max := 0, 0
	found := false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, "|")
		for _, field := range fields {
			if !strings.Contains(strings.ToUpper(field), "RPM") {
				continue
			}
			value, ok := parseTempField(field)
			if !ok || value < 0 {
				continue
			}
			if !found || value < min {
				min = value
			}
			if !found || value > max {
				max = value
			}
			found = true
		}
	}
	return min, max, found
}

func intPointerCopy(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// parseTempField extracts a temperature value from an IPMI field.
func parseTempField(raw string) (int, bool) {
	for _, t := range strings.Fields(raw) {
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// readNvidiaGPU reads the maximum GPU temperature from nvidia-smi.
func (c *Controller) readNvidiaGPU() (int, bool) {
	cmd := exec.Command("nvidia-smi", "--query-gpu=temperature.gpu", "--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	max := -1
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	if max < 0 {
		return 0, false
	}
	return max, true
}

// maxTemp finds the highest CPU temperature and its label.
// Returns (maxTemp, label, hasReading).
func maxTemp(m map[string]int) (int, string, bool) {
	if len(m) == 0 {
		return 0, "", false
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return entityInstance(keys[i]) < entityInstance(keys[j])
	})
	maxVal := math.MinInt
	maxLabel := ""
	for idx, k := range keys {
		if m[k] > maxVal {
			maxVal = m[k]
			maxLabel = fmt.Sprintf("CPU %d", idx+1)
		}
	}
	return maxVal, maxLabel, true
}

// entityInstance extracts the instance number from an IPMI entity ID.
func entityInstance(entity string) int {
	parts := strings.Split(entity, ".")
	if len(parts) != 2 {
		return math.MaxInt
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return math.MaxInt
	}
	return n
}
