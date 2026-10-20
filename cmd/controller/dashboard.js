// SPDX-License-Identifier: AGPL-3.0-only
'use strict';
(() => {
  const $ = id => document.getElementById(id);
  let settings = null, dirty = false, busy = false;
  const controls = new Map();
  function message(text, state = '') { $('settingsMessage').textContent = text; $('settingsMessage').dataset.state = state; }
  function valueOf(input) { return input.type === 'checkbox' ? String(input.checked) : input.value; }
  function changes() {
    const result = {};
    if (!settings) return result;
    controls.forEach((input, name) => {
      const value = valueOf(input);
      if (input.type === 'password') { if (value) result[name] = value; }
      else if (value !== settings.values[name]) result[name] = value;
    });
    return result;
  }
  function updateDirty() {
    const count = Object.keys(changes()).length;
    dirty = count > 0;
    $('dirtyStatus').textContent = count ? count + ' unsaved change' + (count === 1 ? '' : 's') : 'No unsaved changes';
    $('saveSettings').disabled = busy || !dirty;
    $('discardSettings').disabled = busy || !dirty;
  }
  function setBusy(value) {
    busy = value;
    ['reloadSettings', 'applyPreset', 'presetChoice'].forEach(id => $(id).disabled = busy);
    controls.forEach(input => input.disabled = busy);
    updateDirty();
  }
  function savedStatus(data) {
    let text = data.persistent ? 'Settings persist in the configured file. Mount its directory to retain changes across container recreation.' : 'Session-only settings: persistence disabled. Changes are lost on restart.';
    if (data.applied_revision < data.revision) text += ' Live updates queued for the next control cycle.';
    if (data.restart_required.length) text += ' Restart required: ' + data.restart_required.join(', ') + '.';
    message(text, data.restart_required.length ? '' : 'success');
  }
  function renderSettings(data) {
    settings = data;
    controls.clear();
    const target = $('settingsFields'); target.replaceChildren();
    const groups = new Map();
    data.definitions.forEach(def => {
      let group = groups.get(def.group);
      if (!group) {
        const fieldset = document.createElement('fieldset'), legend = document.createElement('legend');
        legend.textContent = def.group; fieldset.appendChild(legend);
        group = document.createElement('div'); group.className = 'setting-grid'; fieldset.appendChild(group); target.appendChild(fieldset); groups.set(def.group, group);
      }
      const wrapper = document.createElement('div'); wrapper.className = 'setting'; wrapper.dataset.search = (def.label + ' ' + def.name + ' ' + def.help).toLowerCase();
      const label = document.createElement('label'); label.htmlFor = 'setting-' + def.name; label.textContent = def.label;
      if (def.restart) { const tag = document.createElement('span'); tag.className = 'restart-tag'; tag.textContent = 'Restart'; label.appendChild(tag); }
      let input;
      if (def.kind === 'select') {
        input = document.createElement('select');
        def.options.forEach(value => { const option = document.createElement('option'); option.value = value; option.textContent = value; input.appendChild(option); });
      } else {
        input = document.createElement('input'); input.type = def.kind === 'bool' ? 'checkbox' : def.kind;
        if (def.kind === 'number') { input.step = 'any'; input.min = '0'; input.required = true; }
        if (def.kind === 'password') { input.autocomplete = 'new-password'; input.placeholder = data.password_set ? 'Password stored — leave blank to keep' : 'Enter password'; }
      }
      input.id = label.htmlFor; input.name = def.name;
      if (def.kind === 'bool') input.checked = data.values[def.name] === 'true'; else input.value = data.values[def.name] || '';
      const help = document.createElement('small'); help.id = input.id + '-help'; help.textContent = def.help;
      const env = document.createElement('small'); env.className = 'env-name'; env.textContent = def.name;
      input.setAttribute('aria-describedby', help.id); input.addEventListener('input', updateDirty); input.addEventListener('change', updateDirty);
      wrapper.append(label, input, help, env); group.appendChild(wrapper); controls.set(def.name, input);
    });
    savedStatus(data); updateDirty(); filterSettings();
  }
  function filterSettings() {
    const term = $('settingsSearch').value.toLowerCase().trim();
    $('settingsFields').querySelectorAll('.setting').forEach(node => node.hidden = !node.dataset.search.includes(term));
    $('settingsFields').querySelectorAll('fieldset').forEach(node => node.hidden = !Array.from(node.querySelectorAll('.setting')).some(setting => !setting.hidden));
  }
  async function loadSettings() {
    if (dirty && !confirm('Discard unsaved settings and reload?')) return;
    setBusy(true);
    try {
      const response = await fetch('/api/settings', { cache:'no-store', signal:AbortSignal.timeout(10000) });
      if (!response.ok) throw new Error(await response.text());
      renderSettings(await response.json());
    } catch (error) { message('Could not load settings: ' + error.message, 'error'); }
    finally { setBusy(false); }
  }
  function showPanel(settingsView) {
    $('overviewPanel').hidden = settingsView; $('settingsPanel').hidden = !settingsView;
    $('overviewTab').setAttribute('aria-pressed', String(!settingsView)); $('settingsTab').setAttribute('aria-pressed', String(settingsView));
    if (settingsView && !settings && !busy) loadSettings();
  }
  $('overviewTab').onclick = () => showPanel(false);
  $('settingsTab').onclick = () => showPanel(true);
  $('reloadSettings').onclick = loadSettings;
  $('settingsSearch').oninput = filterSettings;
  $('discardSettings').onclick = () => { if (settings && confirm('Discard unsaved settings?')) renderSettings(settings); };
  $('applyPreset').onclick = () => {
    if (!settings || busy) return;
    const name = $('presetChoice').value;
    const presets = { quiet:[75,5,.25,.01,.25,.5,true], balanced:[70,5,.35,.015,.4,.75,true], performance:[65,5,.5,.02,.5,1,true], manual:[60,3,1.5,.1,.5,1,false] };
    const keys = ['CPU_TEMPERATURE_THRESHOLD','AUTO_MODE_TEMPERATURE_MARGIN','PID_GAIN_PROPORTIONAL','PID_GAIN_INTEGRAL','PID_GAIN_DERIVATIVE','RATE_OF_CHANGE_BOOST','AUTO_MODE'];
    keys.forEach((key, i) => { const input = controls.get(key), value = presets[name][i]; if (input.type === 'checkbox') input.checked = value; else input.value = String(value); });
    controls.get('FAN_PROFILE').value = name;
    controls.get('CPU_CRITICAL_TEMPERATURE').value = String(Math.min(presets[name][0] + 10, 125));
    updateDirty(); message('Preset staged. Review CPU critical limit and tuning before saving. Other settings preserved.');
  };
  $('settingsForm').onsubmit = async event => {
    event.preventDefault();
    if (!settings || busy || !dirty) return;
    const changed = changes();
    if (!confirm('Apply ' + Object.keys(changed).length + ' settings changes? Cooling policy may change immediately. Verify hardware-safe thresholds and fan duty.')) return;
    const revision = settings.revision;
    setBusy(true); message('Validating and saving…');
    try {
      const response = await fetch('/api/settings', { method:'PUT', headers:{ 'Content-Type':'application/json', 'X-Controller-Settings':'1' }, body:JSON.stringify({ revision, changes:changed }), signal:AbortSignal.timeout(15000) });
      if (!response.ok) throw new Error(await response.text());
      renderSettings(await response.json());
    } catch (error) { message('Save failed: ' + error.message + ' Your edits are preserved. If the request timed out, reload to check whether it saved.', 'error'); }
    finally { setBusy(false); }
  };
  window.addEventListener('beforeunload', event => { if (dirty) { event.preventDefault(); event.returnValue = ''; } });
  window.addEventListener('telemetry', event => {
    const data = event.detail, now = data.now || {};
    const stale = !now.ts || Date.now()/1000 - now.ts > Math.max(15, data.check_interval_sec*3);
    $('connection').textContent = !now.ts ? 'Waiting' : stale ? 'Stale telemetry' : 'Live';
    $('connection').dataset.state = stale ? 'stale' : 'live';
    const number = value => Number.isFinite(value) ? value.toFixed(1) + '°C' : '—';
    $('trendInsight').textContent = now.ts ? [now.trend_30,now.trend_60,now.trend_90].map(number).join(' / ') + (now.trend_boost ? ' · +' + now.trend_boost + '% boost' : '') : 'Waiting for first sample';
    $('inletInsight').textContent = number(now.inlet);
    $('floorInsight').textContent = now.adaptive_floor ? 'Learned minimum ' + now.adaptive_floor + '%' : now.baseline_ready ? 'Baseline ' + number(now.learned_baseline) + ' · floor ' + now.baseline_floor + '%' : 'Learning idle baseline';
  });
  pollTelemetry();
})();