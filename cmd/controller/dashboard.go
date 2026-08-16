package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

func (c *Controller) appendDashboardSample(sample cycleSample) {
	point := dashboardSample{
		TimestampUnix: sample.timestamp.Unix(),
		Raw:           sample.raw,
		EMA:           sample.ema,
		Fan:           sample.fan,
		Inlet:         sample.inlet,
		Source:        sample.source,
		Profile:       sample.profile,
		Comment:       sample.comment,
		FanRPMMin:     sample.fanRPMMin,
		FanRPMMax:     sample.fanRPMMax,
		Trend30:       sample.trend30,
		Trend60:       sample.trend60,
		Trend90:       sample.trend90,
		TrendBoost:    sample.trendBoost,
	}

	c.dashboard.current = point
	c.dashboard.history = append(c.dashboard.history, point)
	if len(c.dashboard.history) > c.cfg.DashboardSampleLimit {
		c.dashboard.history = c.dashboard.history[1:]
	}
}

func (c *Controller) dashboardStatsHandler(w http.ResponseWriter, _ *http.Request) {
	c.statsMu.RLock()
	history := make([]dashboardSample, len(c.dashboard.history))
	copy(history, c.dashboard.history)
	current := c.dashboard.current
	c.statsMu.RUnlock()

	resp := struct {
		Now                  dashboardSample   `json:"now"`
		History              []dashboardSample `json:"history"`
		CheckIntervalSec     float64           `json:"check_interval_sec"`
		LogIntervalSec       float64           `json:"log_interval_sec"`
		TargetTemperature    int               `json:"target_temperature"`
		ThresholdTemperature int               `json:"threshold_temperature"`
		AutoMode             bool              `json:"auto_mode"`
		FanProfile           string            `json:"fan_profile"`
		PIDKp                float64           `json:"pid_kp"`
		PIDKi                float64           `json:"pid_ki"`
		PIDKd                float64           `json:"pid_kd"`
		Margin               int               `json:"margin"`
		RateBoost            float64           `json:"rate_boost"`
	}{
		Now:                  current,
		History:              history,
		CheckIntervalSec:     c.cfg.CheckInterval.Seconds(),
		LogIntervalSec:       c.cfg.LogInterval.Seconds(),
		TargetTemperature:    c.cfg.CPUTemperatureThreshold - c.cfg.AutoModeTemperatureMargin,
		ThresholdTemperature: c.cfg.CPUTemperatureThreshold,
		AutoMode:             c.cfg.AutoMode,
		FanProfile:           c.cfg.FanProfile,
		PIDKp:                c.cfg.PIDKp,
		PIDKi:                c.cfg.PIDKi,
		PIDKd:                c.cfg.PIDKd,
		Margin:               c.cfg.AutoModeTemperatureMargin,
		RateBoost:            c.cfg.RateOfChangeBoostGain,
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
    :root { color-scheme:light; --bg:#e8edf2; --panel:#ffffff; --ink:#162235; --muted:#647185; --border:#d8e0e8; --line1:#087e8b; --line2:#dc6045; --line3:#249e91; --target:#bc7c14; }
    [data-theme="dark"] { color-scheme:dark; --bg:#10161e; --panel:#19232f; --ink:#edf3f8; --muted:#9baaba; --border:#304154; --line1:#36c3c0; --line2:#ff8368; --line3:#5ed5b6; --target:#ffc15a; }
    body { margin:0; font-family:ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; background:var(--bg); color:var(--ink); }
    .wrap { max-width:1100px; margin:24px auto; padding:0 16px; }
    .header { display:flex; align-items:center; justify-content:space-between; gap:14px; margin-bottom:16px; }
    h2 { margin:0; font-family:Georgia, "Times New Roman", serif; font-size:28px; }
    .status { margin:5px 0 0; color:var(--muted); font-size:12px; }
    button { min-height:34px; padding:0 11px; color:var(--ink); background:var(--panel); border:1px solid var(--border); border-radius:6px; font:600 12px ui-monospace, monospace; cursor:pointer; }
    button:hover { border-color:var(--line1); }
    .grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(200px,1fr)); gap:12px; }
    .card { background:var(--panel); border:1px solid var(--border); border-radius:8px; padding:14px; box-shadow:0 8px 22px rgba(0,0,0,.08); }
    .label { color:var(--muted); font-size:12px; text-transform:uppercase; letter-spacing:.06em; }
    .value { font-size:28px; font-weight:700; margin-top:6px; }
    .chart { margin-top:14px; background:var(--panel); border:1px solid var(--border); border-radius:8px; padding:14px; box-shadow:0 8px 22px rgba(0,0,0,.08); }
    .chart-header { display:flex; align-items:flex-start; justify-content:space-between; gap:14px; margin-bottom:8px; }
    .title { font-size:14px; color:var(--muted); margin-bottom:6px; }
    .chart-summary { color:var(--muted); font-size:11px; }
    .legend { display:flex; flex-wrap:wrap; justify-content:flex-end; gap:10px; color:var(--muted); font-size:11px; }
    .legend span { display:inline-flex; align-items:center; gap:5px; }
    .swatch { width:9px; height:9px; border-radius:2px; background:var(--line1); }
    .swatch.ema { background:var(--line2); }
    .swatch.target { background:var(--target); }
    .swatch.rpm { background:var(--line3); }
    svg { width:100%; height:220px; display:block; }
    .tooltip { position:fixed; z-index:10; display:none; min-width:190px; padding:9px 10px; color:var(--ink); background:var(--panel); border:1px solid var(--border); border-radius:6px; box-shadow:0 10px 24px rgba(0,0,0,.2); font-size:11px; line-height:1.55; pointer-events:none; }
    .tooltip b { display:block; margin-bottom:3px; color:var(--muted); }
    .footer { margin-top:10px; color:var(--muted); font-size:13px; }
    .decision { margin-top:14px; padding:14px; display:grid; grid-template-columns:1fr auto; gap:12px; align-items:center; }
    .decision strong { display:block; margin-top:5px; font-family:Georgia, "Times New Roman", serif; font-size:19px; overflow-wrap:anywhere; }
    .decision-note { margin:9px 0 0; color:var(--muted); font-size:12px; line-height:1.5; }
    .facts { display:flex; gap:16px; color:var(--muted); font-size:12px; text-align:right; }
    .facts b { display:block; color:var(--ink); font-size:15px; margin-top:4px; }
    @media (max-width:600px) { .header { align-items:flex-start; } .decision { grid-template-columns:1fr; } .facts { text-align:left; } .chart-header { display:block; } .legend { justify-content:flex-start; margin-top:10px; } }
  </style>
</head>
<body>
<div class="wrap">
  <div class="header"><div><h2>Fan Controller</h2><p class="status" id="status">Waiting for telemetry</p></div><button id="themeToggle" type="button">Dark mode</button></div>
  <div class="grid">
    <div class="card"><div class="label">Current Raw Temp</div><div id="raw" class="value">-</div></div>
    <div class="card"><div class="label">Current EMA Temp</div><div id="ema" class="value">-</div></div>
    <div class="card"><div class="label">Current Fan Command</div><div id="fan" class="value">-</div></div>
    <div class="card"><div class="label">Control Source</div><div id="source" class="value">-</div></div>
    <div class="card"><div class="label">Measured Fan RPM</div><div id="rpm" class="value">-</div><div id="rpmDetail" class="status">Refreshed with the summary window</div></div>
  </div>
  <div class="card decision"><div><div class="label">Current decision</div><strong id="profile">Waiting for first sample</strong><p class="decision-note" id="note">The controller will publish its latest fan decision here.</p></div><div class="facts"><span>Profile<b id="fanProfile">--</b></span><span>Target<b id="target">-</b></span><span>Threshold<b id="threshold">-</b></span><span>Samples<b id="samples">0</b></span></div></div>
  <div class="chart">
    <div class="chart-header"><div><div class="title">Temperature history</div><div id="tempSummary" class="chart-summary">Waiting for samples</div></div><div class="legend"><span><i class="swatch"></i>Raw IPMI</span><span><i class="swatch ema"></i>EMA</span><span><i class="swatch target"></i>Target</span></div></div>
    <svg id="tempChart" viewBox="0 0 1000 220"></svg>
  </div>
  <div class="chart">
    <div class="chart-header"><div><div class="title">Fan command history</div><div id="fanSummary" class="chart-summary">Waiting for samples</div></div><div class="legend"><span><i class="swatch rpm"></i>Requested duty cycle</span></div></div>
    <svg id="fanChart" viewBox="0 0 1000 220"></svg>
  </div>
  <div class="chart">
    <div class="chart-header"><div><div class="title">Measured fan RPM history</div><div id="rpmSummary" class="chart-summary">RPM is refreshed at the log interval</div></div><div class="legend"><span><i class="swatch"></i>Lowest fan</span><span><i class="swatch rpm"></i>Highest fan</span></div></div>
    <svg id="rpmChart" viewBox="0 0 1000 220"></svg>
  </div>
  <div id="meta" class="footer"></div>
</div>
<div id="tooltip" class="tooltip"></div>
<script>
const root = document.documentElement;
const storedTheme = localStorage.getItem('idrac-theme');
root.dataset.theme = storedTheme || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
const themeButton = document.getElementById('themeToggle');
function labelTheme() { themeButton.textContent = root.dataset.theme === 'dark' ? 'Light mode' : 'Dark mode'; }
themeButton.onclick = function () { root.dataset.theme = root.dataset.theme === 'dark' ? 'light' : 'dark'; localStorage.setItem('idrac-theme', root.dataset.theme); labelTheme(); };
labelTheme();
function drawSeries(svg, series, color, yMin, yMax, plot) {
  if (!series.length) return;
  const span = Math.max(1, yMax - yMin);
  const points = series.map((v, i) => !Number.isFinite(v) ? null : ((plot.left + i * plot.width / Math.max(1, series.length - 1)).toFixed(1) + "," + (plot.top + (yMax - v) / span * plot.height).toFixed(1))).filter(Boolean).join(" ");
  if (!points) return;
  const line = document.createElementNS("http://www.w3.org/2000/svg", "polyline");
  line.setAttribute("fill", "none");
  line.setAttribute("stroke", color);
  line.setAttribute("stroke-width", "3");
  line.setAttribute("points", points);
  svg.appendChild(line);
}

function renderChart(svgId, samples, sets, colors, labels, minV, maxV, guides, formatValue) {
  const svg = document.getElementById(svgId);
  while (svg.firstChild) svg.removeChild(svg.firstChild);
  const width = 1000, height = 220, plot = { left:58, top:12, width:926, height:174 }, span = Math.max(1, maxV - minV);
  for (let tick = 0; tick <= 4; tick++) {
    const value = minV + span * tick / 4, y = plot.top + (maxV - value) / span * plot.height;
    const line = document.createElementNS("http://www.w3.org/2000/svg", "line");
    line.setAttribute("x1", plot.left); line.setAttribute("x2", plot.left + plot.width); line.setAttribute("y1", y); line.setAttribute("y2", y); line.setAttribute("stroke", getComputedStyle(root).getPropertyValue('--border').trim()); line.setAttribute("stroke-opacity", ".65"); svg.appendChild(line);
    const label = document.createElementNS("http://www.w3.org/2000/svg", "text"); label.setAttribute("x", plot.left - 8); label.setAttribute("y", y + 4); label.setAttribute("fill", getComputedStyle(root).getPropertyValue('--muted').trim()); label.setAttribute("font-size", "10"); label.setAttribute("text-anchor", "end"); label.textContent = formatValue(value); svg.appendChild(label);
  }
  if (samples.length) {
    [0, Math.floor((samples.length - 1) / 2), samples.length - 1].forEach((index) => {
      const x = plot.left + index * plot.width / Math.max(1, samples.length - 1), label = document.createElementNS("http://www.w3.org/2000/svg", "text");
      label.setAttribute("x", x); label.setAttribute("y", height - 9); label.setAttribute("fill", getComputedStyle(root).getPropertyValue('--muted').trim()); label.setAttribute("font-size", "10"); label.setAttribute("text-anchor", index === 0 ? "start" : (index === samples.length - 1 ? "end" : "middle")); label.textContent = new Date(samples[index].ts * 1000).toLocaleTimeString([], { hour:'2-digit', minute:'2-digit' }); svg.appendChild(label);
    });
  }
  (guides || []).forEach((guide) => {
    if (!Number.isFinite(guide.value)) return;
    const y = plot.top + (maxV - guide.value) / span * plot.height;
    const line = document.createElementNS("http://www.w3.org/2000/svg", "line");
    line.setAttribute("x1", plot.left); line.setAttribute("x2", plot.left + plot.width); line.setAttribute("y1", y); line.setAttribute("y2", y);
    line.setAttribute("stroke", guide.color); line.setAttribute("stroke-width", "1.5"); line.setAttribute("stroke-dasharray", "5 5");
    svg.appendChild(line);
    const label = document.createElementNS("http://www.w3.org/2000/svg", "text");
    label.setAttribute("x", plot.left + plot.width); label.setAttribute("y", y - 4); label.setAttribute("fill", guide.color); label.setAttribute("font-size", "11"); label.setAttribute("text-anchor", "end"); label.textContent = guide.label;
    svg.appendChild(label);
  });
  sets.forEach((s, i) => drawSeries(svg, s, colors[i], minV, maxV, plot));
  const cursor = document.createElementNS("http://www.w3.org/2000/svg", "line"); cursor.setAttribute("stroke", getComputedStyle(root).getPropertyValue('--muted').trim()); cursor.setAttribute("stroke-dasharray", "3 3"); cursor.style.display = "none"; svg.appendChild(cursor);
  const tooltip = document.getElementById('tooltip');
  svg.onmouseleave = () => { cursor.style.display = 'none'; tooltip.style.display = 'none'; };
  svg.onmousemove = (event) => {
    if (!samples.length) return;
    const rect = svg.getBoundingClientRect(), x = (event.clientX - rect.left) * width / rect.width;
    const index = Math.max(0, Math.min(samples.length - 1, Math.round((x - plot.left) / plot.width * Math.max(1, samples.length - 1))));
    const cursorX = plot.left + index * plot.width / Math.max(1, samples.length - 1); cursor.setAttribute('x1', cursorX); cursor.setAttribute('x2', cursorX); cursor.setAttribute('y1', plot.top); cursor.setAttribute('y2', plot.top + plot.height); cursor.style.display = 'block';
    const sample = samples[index]; tooltip.innerHTML = '<b>' + new Date(sample.ts * 1000).toLocaleString() + '</b>' + sets.map((values, i) => labels[i] + ': ' + (Number.isFinite(values[index]) ? formatValue(values[index]) : '—')).join('<br>'); tooltip.style.left = Math.min(event.clientX + 14, window.innerWidth - 210) + 'px'; tooltip.style.top = Math.min(event.clientY + 14, window.innerHeight - 110) + 'px'; tooltip.style.display = 'block';
  };
}

function rangeLabel(values, suffix) {
  if (!values.length) return '-';
  return Math.min(...values) + '–' + Math.max(...values) + suffix;
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
  document.getElementById('profile').textContent = now.profile || 'Waiting for first sample';
  document.getElementById('note').textContent = now.comment || 'The controller will publish its latest fan decision here.';
  document.getElementById('target').textContent = Number.isFinite(d.target_temperature) ? (d.target_temperature + 'C') : '-';
  document.getElementById('threshold').textContent = Number.isFinite(d.threshold_temperature) ? (d.threshold_temperature + 'C') : '-';
  document.getElementById('fanProfile').textContent = d.fan_profile || '--';

  const h = Array.isArray(d.history) ? d.history : [];
  document.getElementById('samples').textContent = h.length;
  const rawSeries = h.map(p => Number.isFinite(p.raw) ? p.raw : null);
  const emaSeries = h.map(p => Number.isFinite(p.ema) ? p.ema : null);
  const fanSeries = h.map(p => Number.isFinite(p.fan) && p.fan >= 0 ? p.fan : null);
  const rpmMinSeries = h.map(p => Number.isFinite(p.fan_rpm_min) ? p.fan_rpm_min : null);
  const rpmMaxSeries = h.map(p => Number.isFinite(p.fan_rpm_max) ? p.fan_rpm_max : null);
  const raw = rawSeries.filter(Number.isFinite);
  const ema = emaSeries.filter(Number.isFinite);
  const fan = fanSeries.filter(Number.isFinite);
  const rpmMin = rpmMinSeries.filter(Number.isFinite);
  const rpmMax = rpmMaxSeries.filter(Number.isFinite);
  const currentRPMMin = now.fan_rpm_min, currentRPMMax = now.fan_rpm_max;
  document.getElementById('rpm').textContent = Number.isFinite(currentRPMMin) && Number.isFinite(currentRPMMax) ? (currentRPMMin + '–' + currentRPMMax + ' RPM') : '-';
  document.getElementById('rpmDetail').textContent = Number.isFinite(currentRPMMin) ? 'Lowest–highest reported fan RPM' : 'No fan RPM sensor reported yet';

  const tempMin = Math.min(...raw, ...ema, 20);
  const tempMax = Math.max(...raw, ...ema, 100);
  const lower = tempMin - 2, upper = tempMax + 2;
  renderChart('tempChart', h, [rawSeries, emaSeries], ['#006d77', '#e76f51'], ['Raw IPMI', 'EMA'], lower, upper, [{ value:d.target_temperature, label:'target ' + d.target_temperature + 'C', color:'#bc7c14' }, { value:d.threshold_temperature, label:'threshold ' + d.threshold_temperature + 'C', color:'#dc6045' }], value => value.toFixed(1) + 'C');
  renderChart('fanChart', h, [fanSeries], ['#2a9d8f'], ['Requested duty cycle'], 0, 100, [], value => Math.round(value) + '%');
  const rpmFloor = Math.max(0, Math.min(...rpmMin, ...rpmMax, 0) - 200);
  const rpmCeiling = Math.max(...rpmMin, ...rpmMax, 1000) + 200;
  renderChart('rpmChart', h, [rpmMinSeries, rpmMaxSeries], ['#087e8b', '#249e91'], ['Lowest fan', 'Highest fan'], rpmFloor, rpmCeiling, [], value => Math.round(value) + ' RPM');
  document.getElementById('tempSummary').textContent = 'Raw ' + rangeLabel(raw, 'C') + ' | EMA ' + rangeLabel(ema, 'C');
  document.getElementById('fanSummary').textContent = 'Requested range ' + rangeLabel(fan, '%');
  document.getElementById('rpmSummary').textContent = rpmMin.length ? ('Measured range ' + Math.min(...rpmMin) + '–' + Math.max(...rpmMax) + ' RPM') : 'RPM is refreshed at the log interval';

  document.getElementById('meta').textContent =
    'samples=' + h.length +
    ' check_interval=' + d.check_interval_sec + 's' +
    ' log_interval=' + d.log_interval_sec + 's' +
    ' profile=' + (now.profile || '-') +
    ' mode=' + (d.auto_mode ? 'automatic' : 'static') +
    ' profile=' + (d.fan_profile || '-') +
    ' Kp/Ki/Kd=' + d.pid_kp + '/' + d.pid_ki + '/' + d.pid_kd +
    ' margin=' + d.margin + 'C' +
    ' rate_boost=' + d.rate_boost +
    ' trend_30/60/90=' + now.trend_30.toFixed(1) + '/' + now.trend_60.toFixed(1) + '/' + now.trend_90.toFixed(1) + 'C' +
    (now.trend_boost ? ' trend_boost=+' + now.trend_boost + '%' : '');
  document.getElementById('status').textContent = now.ts ? ('Live telemetry | latest sample ' + new Date(now.ts * 1000).toLocaleTimeString()) : 'Waiting for telemetry';
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
