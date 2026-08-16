package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"time"
)

func (c *Controller) initWindow(sample cycleSample) {
	c.window = logWindow{
		count:       0,
		rawMin:      sample.raw,
		rawMax:      sample.raw,
		emaMin:      sample.ema,
		emaMax:      sample.ema,
		fanMin:      sample.fan,
		fanMax:      sample.fan,
		lastProfile: sample.profile,
		lastComment: sample.comment,
		lastSource:  sample.source,
	}
}

func (c *Controller) appendWindow(sample cycleSample) {
	if c.window.count == 0 {
		c.initWindow(sample)
	}
	c.window.count++

	if sample.raw < c.window.rawMin {
		c.window.rawMin = sample.raw
	}
	if sample.raw > c.window.rawMax {
		c.window.rawMax = sample.raw
	}
	c.window.rawSum += sample.raw

	if sample.ema < c.window.emaMin {
		c.window.emaMin = sample.ema
	}
	if sample.ema > c.window.emaMax {
		c.window.emaMax = sample.ema
	}
	c.window.emaSum += sample.ema

	if sample.fan >= 0 {
		if c.window.fanMin < 0 || sample.fan < c.window.fanMin {
			c.window.fanMin = sample.fan
		}
		if sample.fan > c.window.fanMax {
			c.window.fanMax = sample.fan
		}
		c.window.fanSum += sample.fan
	}

	c.window.lastProfile = sample.profile
	c.window.lastComment = sample.comment
	c.window.lastSource = sample.source
}

func (c *Controller) appendDashboardSample(sample cycleSample) {
	point := dashboardSample{
		TimestampUnix: sample.timestamp.Unix(),
		Raw:           sample.raw,
		EMA:           sample.ema,
		Fan:           sample.fan,
		Inlet:         sample.inlet,
		Source:        sample.source,
		Profile:       sample.profile,
	}

	c.dashboard.current = point
	c.dashboard.history = append(c.dashboard.history, point)
	if len(c.dashboard.history) > c.cfg.DashboardSampleLimit {
		c.dashboard.history = c.dashboard.history[1:]
	}
}

// recordSample updates dashboard history and writes either cycle logs or interval summaries.
func (c *Controller) recordSample(sample cycleSample) {
	c.statsMu.Lock()
	defer c.statsMu.Unlock()

	c.appendDashboardSample(sample)

	if c.cfg.VerboseCycleLogging {
		inlet := "-"
		if sample.inlet != nil {
			inlet = fmt.Sprintf("%dC", *sample.inlet)
		}
		fan := "-"
		if sample.fan >= 0 {
			fan = fmt.Sprintf("%d%%", sample.fan)
		}
		fmt.Printf("%s  inlet=%-4s raw=%3dC ema=%5.1fC fan=%-4s src=%-6s profile=%-24s %s\n",
			sample.timestamp.Format("02-01-2006 15:04:05"), inlet, sample.raw, sample.ema, fan, sample.source, sample.profile, sample.comment)
		return
	}

	c.appendWindow(sample)
	if c.lastSummaryLog.IsZero() {
		c.lastSummaryLog = sample.timestamp
		return
	}
	if sample.timestamp.Sub(c.lastSummaryLog) < c.cfg.LogInterval {
		return
	}

	count := float64(c.window.count)
	rawAvg := float64(c.window.rawSum) / count
	emaAvg := c.window.emaSum / count
	fanAvg := float64(c.window.fanSum) / count

	fanSummary := "fan=--"
	if c.window.fanMin >= 0 {
		fanSummary = fmt.Sprintf("fan=min/avg/max %d/%.0f/%d%%", c.window.fanMin, fanAvg, c.window.fanMax)
	}

	fmt.Printf("%s  window=%s samples=%d raw=min/avg/max %d/%.1f/%dC ema=min/avg/max %.1f/%.1f/%.1fC %s src=%s profile=%s note=%s\n",
		sample.timestamp.Format("02-01-2006 15:04:05"),
		c.cfg.LogInterval,
		c.window.count,
		c.window.rawMin,
		rawAvg,
		c.window.rawMax,
		c.window.emaMin,
		emaAvg,
		c.window.emaMax,
		fanSummary,
		c.window.lastSource,
		c.window.lastProfile,
		c.window.lastComment,
	)

	c.window = logWindow{}
	c.lastSummaryLog = sample.timestamp
}

func (c *Controller) dashboardStatsHandler(w http.ResponseWriter, _ *http.Request) {
	c.statsMu.RLock()
	history := make([]dashboardSample, len(c.dashboard.history))
	copy(history, c.dashboard.history)
	current := c.dashboard.current
	c.statsMu.RUnlock()

	resp := struct {
		Now              dashboardSample   `json:"now"`
		History          []dashboardSample `json:"history"`
		CheckIntervalSec float64           `json:"check_interval_sec"`
		LogIntervalSec   float64           `json:"log_interval_sec"`
	}{
		Now:              current,
		History:          history,
		CheckIntervalSec: c.cfg.CheckInterval.Seconds(),
		LogIntervalSec:   c.cfg.LogInterval.Seconds(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (c *Controller) dashboardIndexHandler(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>Dell iDRAC Fan Controller Dashboard</title>
  <style>
    :root { --bg:#f4f2ec; --panel:#ffffff; --ink:#1f2733; --muted:#5e6673; --line1:#006d77; --line2:#e76f51; --line3:#2a9d8f; }
    body { margin:0; font-family:"Segoe UI", Tahoma, Geneva, Verdana, sans-serif; background:radial-gradient(circle at top right,#fff,#e9ecef); color:var(--ink); }
    .wrap { max-width:1100px; margin:24px auto; padding:0 16px; }
    .grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(200px,1fr)); gap:12px; }
    .card { background:var(--panel); border-radius:10px; padding:14px; box-shadow:0 8px 22px rgba(0,0,0,.08); }
    .label { color:var(--muted); font-size:12px; text-transform:uppercase; letter-spacing:.06em; }
    .value { font-size:28px; font-weight:700; margin-top:6px; }
    .chart { margin-top:14px; background:#fff; border-radius:10px; padding:12px; box-shadow:0 8px 22px rgba(0,0,0,.08); }
    .title { font-size:14px; color:var(--muted); margin-bottom:6px; }
    svg { width:100%; height:220px; display:block; }
    .footer { margin-top:10px; color:var(--muted); font-size:13px; }
  </style>
</head>
<body>
<div class="wrap">
  <h2>Dell iDRAC Fan Controller</h2>
  <div class="grid">
    <div class="card"><div class="label">Current Raw Temp</div><div id="raw" class="value">-</div></div>
    <div class="card"><div class="label">Current EMA Temp</div><div id="ema" class="value">-</div></div>
    <div class="card"><div class="label">Current Fan Command</div><div id="fan" class="value">-</div></div>
    <div class="card"><div class="label">Control Source</div><div id="source" class="value">-</div></div>
  </div>
  <div class="chart">
    <div class="title">Temperature (Raw and EMA)</div>
    <svg id="tempChart" viewBox="0 0 1000 220"></svg>
  </div>
  <div class="chart">
    <div class="title">Fan Command (%)</div>
    <svg id="fanChart" viewBox="0 0 1000 220"></svg>
  </div>
  <div id="meta" class="footer"></div>
</div>
<script>
function drawSeries(svg, series, color, yMin, yMax) {
  if (!series.length) return;
  const width = 1000, height = 220, pad = 16;
  const span = Math.max(1, yMax - yMin);
  const points = series.map((v, i) => {
    const x = pad + (i * (width - 2*pad) / Math.max(1, series.length - 1));
    const y = height - pad - ((v - yMin) / span) * (height - 2*pad);
    return x.toFixed(1) + "," + y.toFixed(1);
  }).join(" ");
  const line = document.createElementNS("http://www.w3.org/2000/svg", "polyline");
  line.setAttribute("fill", "none");
  line.setAttribute("stroke", color);
  line.setAttribute("stroke-width", "3");
  line.setAttribute("points", points);
  svg.appendChild(line);
}

function renderChart(svgId, sets, colors, minV, maxV) {
  const svg = document.getElementById(svgId);
  while (svg.firstChild) svg.removeChild(svg.firstChild);
  sets.forEach((s, i) => drawSeries(svg, s, colors[i], minV, maxV));
}

async function refresh() {
  const r = await fetch('/api/stats');
  if (!r.ok) return;
  const d = await r.json();
  const now = d.now || {};
	document.getElementById('raw').textContent = Number.isFinite(now.raw) ? (now.raw + 'C') : '-';
	document.getElementById('ema').textContent = Number.isFinite(now.ema) ? (now.ema.toFixed(1) + 'C') : '-';
	document.getElementById('fan').textContent = Number.isFinite(now.fan) && now.fan >= 0 ? (now.fan + '%') : '-';
  document.getElementById('source').textContent = now.source || '-';

  const h = Array.isArray(d.history) ? d.history : [];
  const raw = h.map(p => p.raw).filter(v => Number.isFinite(v));
  const ema = h.map(p => p.ema).filter(v => Number.isFinite(v));
  const fan = h.map(p => p.fan).filter(v => Number.isFinite(v) && v >= 0);

  const tempMin = Math.min(...raw, ...ema, 20);
  const tempMax = Math.max(...raw, ...ema, 100);
  renderChart('tempChart', [raw, ema], ['#006d77', '#e76f51'], tempMin - 2, tempMax + 2);
  renderChart('fanChart', [fan], ['#2a9d8f'], 0, 100);

	document.getElementById('meta').textContent =
		'samples=' + h.length +
		' check_interval=' + d.check_interval_sec + 's' +
		' log_interval=' + d.log_interval_sec + 's' +
		' profile=' + (now.profile || '-');
}

refresh();
setInterval(refresh, 2000);
</script>
</body>
</html>`))
}

func (c *Controller) startDashboard() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", c.dashboardIndexHandler)
	mux.HandleFunc("/api/stats", c.dashboardStatsHandler)

	server := &http.Server{
		Addr:              c.cfg.DashboardListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmt.Printf("Dashboard            : enabled at http://0.0.0.0%s\n", c.cfg.DashboardListenAddress)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "%s  dashboard server error: %v\n", time.Now().Format("02-01-2006 15:04:05"), err)
		}
	}()
}

// logStartup prints startup configuration information.
func (c *Controller) logStartup() {
	if c.cfg.AutoMode {
		target := c.cfg.CPUTemperatureThreshold - c.cfg.AutoModeTemperatureMargin
		fmt.Println("Fan control mode     : AUTO (Go PID controller)")
		fmt.Printf("PID gains            : Kp=%.2f  Ki=%.2f  Kd=%.2f\n", c.cfg.PIDKp, c.cfg.PIDKi, c.cfg.PIDKd)
		fmt.Printf("Fan speed range      : %d%% - %d%%\n", c.cfg.AutoModeFanSpeedMin, c.cfg.AutoModeFanSpeedMax)
		fmt.Printf("Target temperature   : %dC  (threshold %dC - margin %dC)\n",
			target, c.cfg.CPUTemperatureThreshold, c.cfg.AutoModeTemperatureMargin)
		fmt.Printf("EMA smoothing alpha  : %.2f\n", c.cfg.EMAAlpha)
		fmt.Printf("Rate-of-change guard : trigger %.1fC/cycle, boost gain %.1fx\n",
			c.cfg.RateOfChangeTriggerPerCycle, c.cfg.RateOfChangeBoostGain)
	} else {
		fmt.Printf("Fan speed objective  : %d%%\n", c.cfg.FanSpeed)
		fmt.Printf("CPU threshold        : %d°C\n", c.cfg.CPUTemperatureThreshold)
	}
	fmt.Printf("GPU temp source      : %s  (threshold %d°C)\n", c.cfg.GPUTemperatureSource, c.cfg.GPUTemperatureThreshold)
	fmt.Printf("Check interval       : %s\n", c.cfg.CheckInterval)
	fmt.Printf("Apply interval       : %s\n", c.cfg.ApplyInterval)
	fmt.Printf("Log interval         : %s\n", c.cfg.LogInterval)
	if c.cfg.VerboseCycleLogging {
		fmt.Printf("Logging mode         : cycle (verbose)\n")
	} else {
		fmt.Printf("Logging mode         : summary window\n")
	}
	if c.cfg.DashboardEnabled {
		fmt.Printf("Dashboard            : enabled (%s)\n", c.cfg.DashboardListenAddress)
	} else {
		fmt.Printf("Dashboard            : disabled\n")
	}
	fmt.Println()

	if c.cfg.VerboseCycleLogging {
		fmt.Println("Date & time            inlet  raw   ema    fan   src     profile                  comment")
	} else {
		fmt.Println("Date & time            summary logs emitted once per LOG_INTERVAL window")
	}
}

func (c *Controller) safeRound(v float64) int {
	if math.IsNaN(v) {
		return -1
	}
	return int(math.Round(v))
}
