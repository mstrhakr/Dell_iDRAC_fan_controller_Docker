// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardSettingsSaveRevisionAndPasswordSecrecy(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("CONTROLLER_SETTINGS_FILE", settingsPath)
	c := newTestController()
	if err := c.initSettings(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"revision":0,"changes":{"FAN_SPEED":"35","IDRAC_PASSWORD":"secret"}}`))
	request.Host = "controller.local"
	request.Header.Set("Origin", "http://controller.local")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Controller-Settings", "1")
	response := httptest.NewRecorder()
	c.dashboardSettingsHandler(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("settings response exposed password: %s", response.Body.String())
	}
	var responseData struct {
		Values map[string]string `json:"values"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &responseData); err != nil {
		t.Fatal(err)
	}
	if _, exposed := responseData.Values["IDRAC_PASSWORD"]; exposed {
		t.Fatal("settings response included the password in values")
	}

	var persisted map[string]string
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["FAN_SPEED"] != "35" || persisted["IDRAC_PASSWORD"] != "secret" {
		t.Fatalf("persisted settings = %#v", persisted)
	}
	if err := c.applyPendingSettings(); err != nil {
		t.Fatal(err)
	}
	if c.cfg.FanSpeed != 35 {
		t.Fatalf("applied fan speed = %d, want 35", c.cfg.FanSpeed)
	}

	request = httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"revision":0,"changes":{"FAN_SPEED":"40"}}`))
	request.Host = "controller.local"
	request.Header.Set("Origin", "http://controller.local")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Controller-Settings", "1")
	response = httptest.NewRecorder()
	c.dashboardSettingsHandler(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale revision status = %d, want %d", response.Code, http.StatusConflict)
	}
}
