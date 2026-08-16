package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// detectLocalIPMIDevice finds and returns the path to the local IPMI device.
func detectLocalIPMIDevice() (string, error) {
	for _, path := range ipmiDevicePaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf(
		"no IPMI device found at %s.\n"+
			"  To fix this on Unraid:\n"+
			"    1. Open a Terminal on the Unraid host and run:\n"+
			"         modprobe ipmi_devintf ipmi_si\n"+
			"    2. Add --device=/dev/ipmi0:/dev/ipmi0 to your docker run command.\n"+
			"  Alternatively, set IDRAC_HOST to your iDRAC IP address to use network mode.",
		strings.Join(ipmiDevicePaths, " or "),
	)
}

// runIPMI executes an ipmitool command and returns its output.
func (c *Controller) runIPMI(args ...string) (string, error) {
	var base []string
	if c.cfg.NetworkMode {
		base = []string{"-I", "lanplus", "-H", c.cfg.IDRACHost, "-U", c.cfg.IDRACUsername, "-E"}
	} else {
		base = []string{"-I", "open"}
	}
	cmd := exec.Command("ipmitool", append(base, args...)...)
	cmd.Env = os.Environ()
	if c.cfg.NetworkMode {
		cmd.Env = append(cmd.Env, "IPMI_PASSWORD="+c.cfg.IDRACPassword)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ipmitool %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// getServerInfo retrieves server model, manufacturer, and firmware from iDRAC.
func (c *Controller) getServerInfo() (string, string, error) {
	fru, err := c.runIPMI("fru")
	if err != nil {
		return "", "", err
	}
	manufacturer, model := "", ""
	for _, line := range strings.Split(fru, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Product Manufacturer") {
			manufacturer = valueAfterColon(line)
		}
		if strings.HasPrefix(line, "Product Name") {
			model = valueAfterColon(line)
		}
	}
	if manufacturer == "" {
		manufacturer = "DELL"
	}
	if model == "" {
		model = "Unknown model"
	}

	mc, err := c.runIPMI("mc", "info")
	firmware := "unknown"
	if err == nil {
		for _, line := range strings.Split(mc, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Firmware Revision") {
				firmware = valueAfterColon(line)
			}
		}
	}

	return fmt.Sprintf("%s %s", manufacturer, model), firmware, nil
}

// chassisPowerState returns the current power state of the chassis.
func (c *Controller) chassisPowerState() (string, error) {
	return c.runIPMI("chassis", "power", "status")
}

// applyDellDefault applies Dell's default fan control (automatic iDRAC management).
func (c *Controller) applyDellDefault() error {
	_, err := c.runIPMI("raw", "0x30", "0x30", "0x01", "0x01")
	return err
}

// applyManualSpeed sets a manual fan speed (0-100%).
func (c *Controller) applyManualSpeed(speed int) error {
	if _, err := c.runIPMI("raw", "0x30", "0x30", "0x01", "0x00"); err != nil {
		return err
	}
	_, err := c.runIPMI("raw", "0x30", "0x30", "0x02", "0xff", fmt.Sprintf("0x%02x", speed))
	return err
}

// recordIPMIFailure increments the failure counter and returns true if limit reached.
func (c *Controller) recordIPMIFailure() bool {
	c.ipmiFailures++
	return c.ipmiFailuresAllowed > 0 && c.ipmiFailures >= c.ipmiFailuresAllowed
}

// ────────────────────── helpers ───────────────────────────────────

func valueAfterColon(line string) string {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
