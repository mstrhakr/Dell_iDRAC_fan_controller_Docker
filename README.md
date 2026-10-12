<div id="top"></div>

# Dell iDRAC fan controller Docker image

**❤️ This project now has a Sponsor button.** It stays free and open source, and always will.
If it has made your server(s) quieter, you can [thank me by sponsoring me](https://github.com/sponsors/tigerblue77). Thank you 🙏

## Table of contents
<ol>
  <li><a href="#container-console-log-example">Container console log example</a></li>
  <li><a href="#requirements">Requirements</a></li>
  <li><a href="#supported-architectures">Supported architectures</a></li>
  <li><a href="#download-docker-image">Download Docker image</a></li>
  <li><a href="#usage">Usage</a></li>
  <li><a href="#parameters">Parameters</a></li>
  <li><a href="#stopping-the-container">Stopping the container</a></li>
  <li><a href="#troubleshooting">Troubleshooting</a></li>
  <li><a href="#contributing">Contributing</a></li>
  <li><a href="#license">License</a></li>
</ol>

## Container console log example

![image](https://user-images.githubusercontent.com/37409593/216442212-d2ad7ff7-0d6f-443f-b8ac-c67b5f613b83.png)

<p align="right">(<a href="#top">back to top</a>)</p>

<!-- REQUIREMENTS -->
## Requirements
### iDRAC version

This container drives the fans with two IPMI raw commands Dell removed from its recent hardware. What decides whether yours takes them is the iDRAC firmware, not the model :

| iDRAC | Dell's IPMI raw fan control commands |
| --- | --- |
| iDRAC 6, 7 and 8 (generations 11 to 13) | Available |
| iDRAC 9 up to firmware `3.30.30.30` | Available, that being the newest version they are confirmed to work on |
| iDRAC 9 from firmware `3.34.34.34` on | **Removed by Dell**, deliberately and for good |
| iDRAC 10 (17th generation) | Never had them |

`3.31.31.31` and `3.32.32.32` sit between those two versions and what either answers has never been reported. Blades and modular servers refuse them whatever their generation, their fans belonging to the enclosure.

**Downgrading is no longer a way out** : an iDRAC 9 that has installed Dell's June 2024 release or any later one [cannot go back to `4.40.10.00` or older](https://www.dell.com/support/kbdoc/en-us/000225924/rac0181-idrac9-firmware-downgrade-failures-on-14-15g-poweredge-servers).

None of it has to be worked out in advance : the container asks the server, logs its iDRAC firmware version at startup and [says what it was answered](#your-fan-speed-never-changes-and-the-log-says-the-server-refused-fan-control).

### iDRAC user privileges

The account this container uses must be an **Administrator** on the iDRAC : fan control is not read-only, and a lesser account is refused it with the very completion code (`0xd4`) a firmware that removed the commands returns.

### To access iDRAC over LAN (not needed in "local" mode) :

1. Log into your iDRAC web console

![001](https://user-images.githubusercontent.com/37409593/210168273-7d760e47-143e-4a6e-aca7-45b483024139.png)

2. In the left side menu, expand "iDRAC settings", click "Network" then click "IPMI Settings" link at the top of the web page.

![002](https://user-images.githubusercontent.com/37409593/210168249-994f29cc-ac9e-4667-84f7-07f6d9a87522.png)

3. Check the "Enable IPMI over LAN" checkbox then click "Apply" button.

![003](https://user-images.githubusercontent.com/37409593/210168248-a68982c4-9fe7-40e7-8b2c-b3f06fbfee62.png)

4. Test access to IPMI over LAN running the following commands :
```bash
apt -y install ipmitool
ipmitool -I lanplus \
  -H <iDRAC IP address> \
  -U <iDRAC username> \
  -P <iDRAC password> \
  sdr elist all
```

<p align="right">(<a href="#top">back to top</a>)</p>

<!-- SUPPORTED ARCHITECTURES -->
## Supported architectures

This Docker container is currently built and available for the following CPU architectures :
- AMD64
- ARM64

<p align="right">(<a href="#top">back to top</a>)</p>

<!-- DOWNLOAD DOCKER IMAGE -->
## Download Docker image

- [Docker Hub](https://hub.docker.com/r/tigerblue77/dell_idrac_fan_controller)
- [GitHub Containers Repository](https://github.com/tigerblue77/Dell_iDRAC_fan_controller_Docker/pkgs/container/dell_idrac_fan_controller)

<p align="right">(<a href="#top">back to top</a>)</p>

<!-- USAGE -->
## Usage

1. with local iDRAC:

```bash
docker run -d \
  --name Dell_iDRAC_fan_controller \
  --restart=unless-stopped \
  -e IDRAC_HOST=local \
  -e FAN_SPEED=<fan speed in %, from 0 to 100, or hexadecimal from 0x00 to 0x64> \
  -e CPU_TEMPERATURE_THRESHOLD=<decimal temperature threshold in °C, from 20 to 125, or auto> \
  -e CHECK_INTERVAL=<seconds between each check, or a suffixed duration like 5m, up to 15 minutes> \
  -e MAXIMUM_IPMI_UNREACHABLE_DURATION=<how long the iDRAC may stay unreachable before exiting, in seconds or suffixed like 5m, or empty> \
  -e MAXIMUM_CONSECUTIVE_IPMI_FAILURES=<the same threshold in cycles instead, 1 or more, or empty> \
  -e MONITORING_ONLY_MODE=<true or false> \
  --device=/dev/ipmi0:/dev/ipmi0:rw \
  tigerblue77/dell_idrac_fan_controller:latest
```

2. with LAN iDRAC:

```bash
docker run -d \
  --name Dell_iDRAC_fan_controller \
  --restart=unless-stopped \
  -e IDRAC_HOST=<iDRAC IP address> \
  -e IDRAC_USERNAME=<iDRAC username> \
  -e IDRAC_PASSWORD=<iDRAC password> \
  -e FAN_SPEED=<fan speed in %, from 0 to 100, or hexadecimal from 0x00 to 0x64> \
  -e CPU_TEMPERATURE_THRESHOLD=<decimal temperature threshold in °C, from 20 to 125, or auto> \
  -e CHECK_INTERVAL=<seconds between each check, or a suffixed duration like 5m, up to 15 minutes> \
  -e MAXIMUM_IPMI_UNREACHABLE_DURATION=<how long the iDRAC may stay unreachable before exiting, in seconds or suffixed like 5m, or empty> \
  -e MAXIMUM_CONSECUTIVE_IPMI_FAILURES=<the same threshold in cycles instead, 1 or more, or empty> \
  -e MONITORING_ONLY_MODE=<true or false> \
  tigerblue77/dell_idrac_fan_controller:latest
```

`docker-compose.yml` examples:

1. to use with local iDRAC:

```yml
version: '3.8'

services:
  Dell_iDRAC_fan_controller:
    image: tigerblue77/dell_idrac_fan_controller:latest
    container_name: Dell_iDRAC_fan_controller
    restart: unless-stopped
    environment:
      - IDRAC_HOST=local
      - FAN_SPEED=<fan speed in %, from 0 to 100, or hexadecimal from 0x00 to 0x64>
      - CPU_TEMPERATURE_THRESHOLD=<decimal temperature threshold in °C, from 20 to 125, or auto>
      - CHECK_INTERVAL=<seconds between each check, or a suffixed duration like 5m, up to 15 minutes>
      - MAXIMUM_IPMI_UNREACHABLE_DURATION=<how long the iDRAC may stay unreachable before exiting, in seconds or suffixed like 5m, or empty>
      - MAXIMUM_CONSECUTIVE_IPMI_FAILURES=<the same threshold in cycles instead, 1 or more, or empty>
      - MONITORING_ONLY_MODE=<true or false>
    devices:
      - /dev/ipmi0:/dev/ipmi0:rw
```

2. to use with LAN iDRAC:

```yml
version: '3.8'

services:
  Dell_iDRAC_fan_controller:
    image: mstrhakr/dell_idrac_fan_controller:latest
    container_name: Dell_iDRAC_fan_controller
    restart: unless-stopped
    environment:
      - IDRAC_HOST=<iDRAC IP address>
      - IDRAC_USERNAME=<iDRAC username>
      - IDRAC_PASSWORD=<iDRAC password>
      - FAN_SPEED=<fan speed in %, from 0 to 100, or hexadecimal from 0x00 to 0x64>
      - CPU_TEMPERATURE_THRESHOLD=<decimal temperature threshold in °C, from 20 to 125, or auto>
      - CHECK_INTERVAL=<seconds between each check, or a suffixed duration like 5m, up to 15 minutes>
      - MAXIMUM_IPMI_UNREACHABLE_DURATION=<how long the iDRAC may stay unreachable before exiting, in seconds or suffixed like 5m, or empty>
      - MAXIMUM_CONSECUTIVE_IPMI_FAILURES=<the same threshold in cycles instead, 1 or more, or empty>
      - MONITORING_ONLY_MODE=<true or false>
```

For security reasons, it is recommended to store your credentials in a `.env` file instead of hardcoding them in a `docker run` command or a `docker-compose.yml` file. Copy [`.env.example`](./.env.example) to `.env`, fill in your values, then reference it:

- with `docker run`:

```bash
docker run -d \
  --name Dell_iDRAC_fan_controller \
  --restart=unless-stopped \
  --env-file .env \
  tigerblue77/dell_idrac_fan_controller:latest
```

- with `docker-compose.yml`:

```yml
version: '3.8'

services:
  Dell_iDRAC_fan_controller:
    image: tigerblue77/dell_idrac_fan_controller:latest
    container_name: Dell_iDRAC_fan_controller
    restart: unless-stopped
    env_file:
      - .env
```

(if using local iDRAC, add the `devices:` section shown in the examples above)

<p align="right">(<a href="#top">back to top</a>)</p>

<!-- PARAMETERS -->
## Parameters

These are the current **Go controller** settings. Restart the container after changing configuration. Network mode requires explicit credentials; local mode requires an exposed IPMI device.

- `IDRAC_HOST` parameter can be set to "local" or to your distant iDRAC's IP address. **Default** value is "local".
- `IDRAC_USERNAME` and `IDRAC_PASSWORD` are required in network mode. **Neither has a default**. They are unnecessary in local mode.
- `FAN_PROFILE` selects a startup-only fan-control preset. **Default** value is `balanced`.
  - `quiet` targets 70°C with a gradual PID response.
  - `balanced` targets 65°C and is the recommended general-purpose setting.
  - `performance` targets 60°C with an earlier, firmer response.
  - `manual` disables PID control and uses `FAN_SPEED` instead.
  - Explicit low-level settings such as `CPU_TEMPERATURE_THRESHOLD`, `AUTO_MODE_TEMPERATURE_MARGIN`, `PID_GAIN_*`, `RATE_OF_CHANGE_BOOST`, or `AUTO_MODE` override the matching profile setting. Restart the container after changing a profile.
- `AUTO_MODE` enables the automatic PID fan controller. **Default** value is true. It adjusts the fan command from the hottest CPU or GPU temperature. Automatic profiles use gentler proportional/integral gains and hold duty within ±1°C of the target to reduce repeated ramping. Set it to `false` to use the fixed `FAN_SPEED` behavior instead.
  - Default PID gains (`Kp / Ki / Kd`): `quiet` = `0.25 / 0.01 / 0.25`, `balanced` = `0.35 / 0.015 / 0.4`, `performance` = `0.5 / 0.02 / 0.5`. Explicit `PID_GAIN_PROPORTIONAL`, `PID_GAIN_INTEGRAL`, and `PID_GAIN_DERIVATIVE` settings still override these defaults.
  - Gains use a **5-second reference cadence**. PID corrections, derivative/rise detection, EMA smoothing and learned-baseline adaptation scale with measured elapsed time. Long observation gaps reset evidence rather than integrating unseen temperature errors. The first sample after a CPU/GPU domain change reseeds thermal state without an artificial derivative kick.
- `FAN_RAMP_UP` and `FAN_RAMP_DOWN` limit normal automatic fan changes in **percentage points per second**. Defaults are `0.4` up and `0.2` down: at a 5-second check interval, at most **+2 / −1 percentage points per check**, including trend boosts and learned-baseline corrections. Slower reductions retain cooling while the server settles. Both accept finite numbers greater than zero, up to 100. Fractional changes accumulate internally; a longer check interval allows a proportionally larger change, capped at 10 points per check.
  - Rapid EMA warming can bypass the normal upward ramp limit. A raw or smoothed temperature **above the selected CPU/GPU threshold** raises duty by 10 points per check (up to the configured fan maximum), regardless of the slow ramp setting or EMA lag. Temperature polling is not slowed. These settings do not affect manual or monitoring-only mode.
  - Automatic mode also detects repeated large heating/cooling swings: **four duty reversals of at least 20 points**, each with a temperature span of at least **3°C**, within **10 minutes** raise a temporary minimum by **5 points**. New qualifying cycles can raise it again, capped at **40%** (or the configured maximum if lower). One load spike, monotonic changes, and small sensor noise do not qualify.
  - This adaptive minimum **expires 15 minutes after the last qualifying set of reversals**; steady samples do not renew it. Duty then ramps down normally rather than dropping abruptly. CPUs sharing a threshold are one control group: the hottest socket changing does **not** discard learning. CPU/GPU domain changes, threshold changes, observation gaps longer than the greater of 90 seconds or three check intervals, and restarting clear thermal learning. Manual and monitoring-only modes do not apply the adaptive floor. Logs and the dashboard show the active minimum and expiry time.
  - Sustained-warming detection uses a 90-second warmup, a 30-second cooldown and bounded 5-point boost requests below 70% duty. Stable low-fan operation learns an idle baseline after 10 minutes; its baseline-dependent floor is separate from the expiring oscillation floor. Neither policy overrides critical cooling.
- `EMA_ALPHA` defaults to `0.3`, accepted range `(0, 1]`, representing smoothing at the 5-second reference cadence. `RATE_OF_CHANGE_TRIGGER` defaults to `2.0` °C **per five seconds** of EMA rise; `RATE_OF_CHANGE_BOOST` defaults to quiet `0.5`, balanced `0.75`, performance `1.0`. `AUTO_MODE_TEMPERATURE_MARGIN` defaults to 5°C for automatic profiles (3°C for manual), accepted range `0..20`. Avoid additional smoothing without checking thermal lag under load.
- `AUTO_MODE_FAN_SPEED_MIN` sets the automatic duty floor. **Default** is `10` (%); accepted integer range is `1..100`. `FAN_SPEED` does not set this floor. Learned policies build on this minimum, and automatic control starts halfway between this floor and 100% (55% with defaults).
- `FAN_SPEED` sets static duty when `AUTO_MODE=false` or `FAN_PROFILE=manual`, subject to thermal safety. Decimal percentages (`0..100`) and prefixed hexadecimal (`0x00..0x64`) are accepted. **Default** is now `10` (%), previously 5%. Explicit settings, including lower ones, are preserved. Neither the 10% automatic floor nor static default is a universal safe chassis minimum: validate CPU, storage and PCIe cooling on your hardware.
  - Anything outside that range stops the container at startup rather than being clamped or passed through to the fans, `200` having once reached `ipmitool` as `0xc8`.
  - :warning: **The `0x` prefix is the only thing that tells the two notations apart**, and both are accepted, so a value that lost its prefix is not refused — it just applies a different duty cycle than the one you meant. `0x64` is 100% while `64` is 64%; `0x30` is 48% while `30` is 30%. The startup log always states the one that was resolved:

    ```text
    Fan speed objective: 64%
    ```

    That is the line to check first if the fans do not behave the way you expected.
- `CPU_TEMPERATURE_THRESHOLD` is the normal CPU control threshold, accepted range `20..125` °C. The default comes from `FAN_PROFILE`: quiet 75°C, balanced 70°C, performance 65°C, manual 60°C. In automatic mode the target is threshold minus margin; exceeding the normal threshold bypasses slow upward ramping. In static mode exceeding a CPU or monitored GPU threshold restores Dell automatic cooling.
  - Explicit `CPU_TEMPERATURE_THRESHOLD=auto` is a compatibility spelling for **60°C**, not hardware detection. The Go controller does not read `lm-sensors` high/critical limits. CPU readings come from IPMI; the legacy `CPU_TEMPERATURE_SOURCE` setting is not implemented.
- `GPU_TEMPERATURE_SOURCE` defaults to `disabled`; use `nvidia-smi` to monitor NVIDIA GPU temperatures (`nvidia` and `nvidia_smi` are accepted aliases). The executable and GPU access must be available inside the container. `GPU_TEMPERATURE_THRESHOLD` defaults to **80°C**, accepted range `20..125` °C. Normal control selects the source with the greatest temperature excess relative to its own threshold, not necessarily the highest absolute reading. If the GPU query fails, the current implementation omits that GPU reading; verify GPU telemetry before relying on it.
- `CPU_CRITICAL_TEMPERATURE` and `GPU_CRITICAL_TEMPERATURE` define a separate emergency policy. Defaults are **normal threshold +10°C**, capped at 125°C (balanced CPU 80°C; GPU 90°C). Explicit integers must be at least the corresponding normal threshold and at most 125°C. **Any monitored raw reading at or above its critical limit immediately commands 100%**, bypassing EMA, PID, minimums and normal ramps; static mode is also protected. Critical entry and recovery are logged immediately, independently of summary logging. Monitoring-only mode sends no commands, even on critical readings.
  - **These are policy defaults, not detected hardware limits.** Set thresholds below your hardware's specified critical limits, and validate cooling under load. Raising a threshold does not make hardware tolerate higher temperatures; the accepted 125°C configuration bound is not a recommended operating temperature.
- `CHECK_INTERVAL` accepts positive whole seconds or `s`, `m`, `h`, `d` suffixes. When unset, polling uses the greater of **5s** and three times measured IPMI latency, capped at 60s. An explicit value above 15 minutes is rejected in network mode, including monitoring-only mode; local mode currently has no upper bound. **Keep active-control polling short:** elapsed-time normalization does not detect overheating between reads, and slow polling defeats timely critical protection.
- `LOG_INTERVAL` defaults to five check intervals, capped at 30s (normally 25s). `VERBOSE_CYCLE_LOGGING=false` emits interval summaries; `true` emits every sample. Critical events and IPMI recovery are logged immediately. Fan RPM telemetry refreshes at the log interval.
- `IPMI_COMMAND_TIMEOUT` defaults to **10s**, accepted positive durations up to 5 minutes. Each IPMI subprocess, including shutdown commands, has a deadline; inherited output-pipe cleanup is also bounded. This is separate from the failed-check escalation below.
- `MAXIMUM_IPMI_UNREACHABLE_DURATION` defaults to **60s** and is converted to a whole failed-check count, rounding up using the configured check interval (normally 12 checks). A deliberately empty value disables escalation; zero is invalid. It counts failed power-status or temperature reads, not just one specific transport error. Only a complete successful temperature read or an observed powered-off state resets the counter; a successful power query followed by a failed temperature query does not. Command duration and IPMI delays can make actual elapsed escalation longer than the nominal setting.
- `MAXIMUM_CONSECUTIVE_IPMI_FAILURES` is an optional positive integer overriding the duration-derived count. A one-check limit is warned about at startup. Once reached, the failure exits the control loop and attempts safety cooling before process exit. Fan-control command failures are fatal immediately rather than leaving an actuator silently unresponsive.
  - **Use a restart policy** such as `unless-stopped`. If IPMI is unreachable, shutdown cooling can fail too: exiting without a restart policy leaves the previous duty with no controller supervising it. Clearing the duration keeps retrying failed reads; it does not disable fatal actuator errors or critical-temperature protection.
- Legacy third-party PCIe cooling settings are not implemented by the Go controller: `DISABLE_THIRD_PARTY_PCIE_CARD_DELL_DEFAULT_COOLING_RESPONSE` and `KEEP_THIRD_PARTY_PCIE_CARD_COOLING_RESPONSE_STATE_ON_EXIT` are parsed for compatibility but do not change PCIe cooling policy. Configure that policy through supported iDRAC controls instead.
- `MONITORING_ONLY_MODE` parameter is a boolean that allows to run the container in a read-only, monitoring-only mode: temperatures are still read and logged at each `CHECK_INTERVAL`, but no fan control profile (neither the user-defined one nor Dell's default) and no third-party PCIe card cooling response change is ever sent to the server. Useful to observe temperatures and validate your `FAN_SPEED`/`CPU_TEMPERATURE_THRESHOLD` values before letting the container actually take control of the fans. **Default** value is false.

- `ENABLE_DASHBOARD` defaults to `false`. When enabled, `DASHBOARD_LISTEN_ADDRESS` defaults to `:8080` and `DASHBOARD_SAMPLE_LIMIT` to `300` (accepted `30..5000`, normally about 25 minutes of history). The dashboard shows requested duty, available measured RPM, thermal trends, and the adaptive floor with expiry. Publish the port only on trusted networks: the dashboard has no authentication.

Boolean settings accept **only lowercase `true` or `false`**. Invalid configuration produces a `Configuration error:` message and exits before the control loop starts.

<p align="right">(<a href="#top">back to top</a>)</p>

<!-- STOPPING THE CONTAINER -->
## Stopping the container

While the container runs, the fans are held at the speed selected by the controller and the server's own thermal regulation is switched off. Shutdown therefore has to leave the server at a safe fan speed.

The Go controller runs directly as the container's PID 1. On `docker stop`, `docker restart`, a host shutdown or `docker compose down`, it catches `SIGTERM` and attempts 100% cooling before exiting. The same policy handles fatal control errors and panics. The log confirms a successful action:

```text
Received signal: terminated. Starting safe shutdown...
Safety exit triggered by signal.
Fans set to 100% for emergency cooling.
```

Do not force the container to stop with `SIGKILL`: no process can catch it, so the controller cannot apply the 100% safety speed.

**Monitoring-only shutdown is read-only too:** it logs `Monitoring-only shutdown: no fan commands sent` and leaves the existing cooling policy untouched. In control mode, unreachable IPMI or rejected commands can prevent safety cooling; check the shutdown log rather than assuming it succeeded.

<p align="right">(<a href="#top">back to top</a>)</p>

<!-- TROUBLESHOOTING -->
## Troubleshooting

### Your container restarts over and over without ever monitoring anything

Check the log for a configuration error, failed IPMI reads, or refused actuator commands. Configuration failures name the setting and the accepted range, for example:

```text
Configuration error: CPU_TEMPERATURE_THRESHOLD: must be in range 20..125
```

Fix configuration errors before restarting. Repeated IPMI read failures exit after the resolved failed-check limit; investigate connectivity, credentials and command timeouts. A restart policy can recover transient outages but cannot fix unsupported firmware or invalid configuration.

### Your fan speed never changes and the log says the server refused fan control

Check iDRAC firmware support and Administrator privileges. The current Go controller treats a failed fan-control command as fatal, attempts safety cooling, then exits. It does **not** automatically switch into a permanent monitoring-only mode. A typical error includes:

```text
Fatal error: fan control command failed: ipmitool raw 0x30 0x30 ...
```

Fan control uses two separate commands: disable Dell automatic control, then set duty. The first may succeed while the second fails, so do not assume that a refused command left cooling untouched. Verify fan behavior and the safety-exit log. Set `MONITORING_ONLY_MODE=true` explicitly if you only want observation. The [iDRAC version](#idrac-version) and [iDRAC user privileges](#idrac-user-privileges) sections explain firmware and account limitations.

**If it is the firmware, downgrading will not get you out of it.** Dell removed the commands on purpose — *"going forward access is not going to be allowed as it affects the thermal algorithms and cooling the system"*, [on its own forum](https://www.dell.com/community/en/conversations/poweredge-hardware-general/dell-eng-is-taking-away-fan-speed-control-away-from-users-idrac-3343434/647f8593f4ccf8a8de47aa9b) — and since the June 2024 release (`7.00.00.172` on the 14th generation, `7.10.50.00` on the 15th) an iDRAC 9 [cannot be downgraded to `4.40.10.00` or older](https://www.dell.com/support/kbdoc/en-us/000225924/rac0181-idrac9-firmware-downgrade-failures-on-14-15g-poweredge-servers), the bootloader having changed in a way older firmware cannot boot. The restriction carries forward to every version since, and Dell documents that *"while the firmware downgrade job may reflect a successful status, the Lifecycle Log records a RAC0181 informational event on iDRAC9 reboot"* : check the version the iDRAC reports afterwards rather than the job's outcome.

**What the server still lets you do, without this container.** The iDRAC keeps a coarser set of cooling controls of its own, in *Configuration > System Settings > Hardware Settings > Cooling Configuration*. None of them is a substitute for `FAN_SPEED` — Dell's algorithm keeps deciding, and [its own documentation](https://downloads.dell.com/manuals/common/customcooling_poweredge_idrac9.pdf) is explicit that *"the server does not allow fan speeds to drop below the threshold that is required to cool the server"* — but two of the three are worth knowing about, and the third is worth knowing about precisely so you do not reach for it :

- **Thermal profile** — *Minimum Power* or *Sound Cap* instead of *Default*. This is the only one of the three that can make a server quieter, and it selects a profile rather than a speed.
- **Fan speed offset** — *Low*, *Medium*, *High* and *Max* add +25%, +50%, +75% and +100% **over** the baseline the algorithm computed. It only ever raises, so it answers a server that runs too hot and never one that runs too loud, whatever its name suggests.
- **Minimum fan speed** — a floor, not a setpoint : *"System fans can run higher than the fan speed that the MFS option sets [...] but not lower"*. It cannot take the fans below what the algorithm asks for, which is the one thing this container does and the one thing these servers no longer allow. It is also bounded from below by a read-only limit that varies with the machine, so a low value may not even be accepted.

Dell's own documents disagree on which licence this needs — [KB 000257346](https://www.dell.com/support/kbdoc/en-us/000257346/poweredge-how-to-change-the-fan-speed-offset) says *"Enterprise or Datacenter"* for the web interface, the iDRAC 9 RACADM guide says *"iDRAC Express or iDRAC Enterprise"* for the same attributes, and the iDRAC 10 attribute registry marks them as needing none — so look rather than assume. Whether this container could ever drive any of it is [#360](https://github.com/tigerblue77/Dell_iDRAC_fan_controller_Docker/issues/360), which is waiting on hardware and is not a promise.

### Your server frequently switches back to the default Dell fan mode:

1. In static mode, Dell cooling resumes when a monitored temperature exceeds its normal threshold; increase a validated `FAN_SPEED` if static duty is insufficient.
2. In automatic mode, normal threshold crossings increase requested duty rather than handing control back to Dell. Critical raw temperatures command 100% immediately. Check the dashboard and logged action to distinguish these paths.
3. Verify airflow, dust, inlet temperature, and thermal contact before raising temperature limits. Use limits appropriate to the actual sensor and hardware; `auto` does not detect them.

### Not all of your CPUs appear in the temperatures table

The Go controller reads all currently readable IPMI CPU entities, then displays the hottest controlling CPU in summary logs and the dashboard. It does not display a persistent per-socket temperature table or retain an inventory of previously seen CPUs.

If fewer sensors are listed than the number of CPUs installed, your iDRAC isn't reporting the missing sockets as readable IPMI processor entities. Check what it does report with :
```bash
ipmitool -I lanplus \
  -H <iDRAC IP address> \
  -U <iDRAC username> \
  -P <iDRAC password> \
  sdr type temperature
```
CPUs are the lines whose 4th column is an entity `3.<something>` and whose reading ends in `degrees C`. They need not be contiguous nor in order: a socket that is empty or unreadable is usually still listed, but as `Disabled` or `No Reading` instead of a temperature, and is therefore not monitored. Please [open an issue](https://github.com/tigerblue77/Dell_iDRAC_fan_controller_Docker/issues) with your server model and that output if a CPU that does report a temperature is missing from the table.

New readable entities are included on the next check. **A previously readable socket that disappears while other sensors remain is currently omitted, not tracked as a fault.** Verify every installed CPU is represented in the IPMI output; the hottest-source chart is not evidence that every socket is still readable.

Note that on chassis products (VRTX, FX2, M1000e, MX7000) each server node has its own iDRAC with its own address: point the container at a node's iDRAC, not at the chassis CMC, which doesn't answer IPMI at all.

### None of your CPUs appears in the temperatures table

If neither CPU nor enabled GPU temperature is readable, control mode restores Dell automatic cooling and keeps checking. Monitoring-only mode sends no commands. With an enabled, readable GPU, automatic mode can continue using that GPU even without CPU readings; that does **not** prove CPU cooling is supervised.

There is no `lm-sensors` fallback in the current Go controller. Check the IPMI temperature output above and your firmware's sensor support. All fan commands still go through iDRAC; changing a temperature source cannot make unsupported fan-control commands work.

<p align="right">(<a href="#top">back to top</a>)</p>

<!-- CONTRIBUTING -->
## Contributing

Contributions are what make the open source community such an amazing place to learn, inspire, and create. Any contributions you make are **greatly appreciated**.

If you have a suggestion that would make this better, please fork the repo and create a pull request. You can also simply open an issue with the tag "enhancement".
Don't forget to give the project a star! Thanks again!

1. Fork the Project
2. Create your Feature Branch (`git checkout -b feature/AmazingFeature`)
3. Commit your Changes, signed off (`git commit -s -m 'Add some AmazingFeature'`)
4. Push to the Branch (`git push origin feature/AmazingFeature`)
5. Open a Pull Request

Please read [`CONTRIBUTING.md`](./CONTRIBUTING.md) before you do : it explains the sign-off in step 3, and the terms your contribution arrives under in a project that is [dual-licensed](#license).

To test locally, use either :
```bash
docker build -t tigerblue77/dell_idrac_fan_controller:dev .
docker run -d ...
```
or
```bash
export IDRAC_HOST=<iDRAC IP address>
export IDRAC_USERNAME=<iDRAC username>
export IDRAC_PASSWORD=<iDRAC password>
export FAN_SPEED=<fan speed in %, from 0 to 100, or hexadecimal from 0x00 to 0x64>
export CPU_TEMPERATURE_THRESHOLD=<decimal temperature threshold in °C, from 20 to 125, or auto>
export CHECK_INTERVAL=<seconds between each check, or a suffixed duration like 5m, up to 15 minutes>
export MAXIMUM_IPMI_UNREACHABLE_DURATION=<how long the iDRAC may stay unreachable before exiting, in seconds or suffixed like 5m, or empty>
export MAXIMUM_CONSECUTIVE_IPMI_FAILURES=<the same threshold in cycles instead, 1 or more, or empty>
export MONITORING_ONLY_MODE=<true or false>

go run ./cmd/controller
```

### Automated tests

The controller's unit tests need no Dell hardware, iDRAC or network:

```bash
go test ./...
```

The [`CI`](.github/workflows/ci.yml) workflow runs Go tests and vet, cross-compiles the amd64 controller, and builds the Docker image on pushes to master and pull requests. Controller tests live beside the Go implementation and need no Dell hardware.

<p align="right">(<a href="#top">back to top</a>)</p>

<!-- LICENSE -->
## License

[![License: AGPL v3][agpl-shield]][agpl] [![Commercial licence available][commercial-shield]][link-to-commercial-license-file]

This project is dual-licensed.

**By default, it is free software under the [GNU Affero General Public License version 3][agpl]** (`AGPL-3.0-only`). You may use it, study it, modify it and redistribute it, at no cost and with no formality. The one thing asked in return is reciprocity : if you distribute the program — as-is or modified, as scripts, as an image, or inside a product — the people who receive it must get the corresponding source under those same terms. The full text is in [`LICENSE`][link-to-license-file].

Running the container is never restricted. On a homelab, on a company's own servers, in production, at any scale : the AGPL asks nothing of you for that, and no permission is needed.

**A [separate commercial licence][link-to-commercial-license-file] is available** for the parties who cannot meet those obligations — typically a vendor embedding the controller in a product whose source cannot be published, or anyone needing a warranty, an indemnity or a support commitment, none of which the AGPL provides. The choice between the two is yours ; see [`LICENSE-COMMERCIAL.md`][link-to-commercial-license-file].

### What you may do

**Using it**

| | Under `AGPL-3.0-only` |
|---|---|
| Run it in a homelab | ✅ |
| Run it at work, in production, at any scale | ✅ no permission needed |
| Modify it for your own use, without distributing it | ✅ |
| Redistribute it, modified or not, commercially or not | ✅ provided the corresponding source goes with it |
| Combine it with GPL / AGPL code | ✅ |
| Patent licence | ✅ granted (§11) |

**Putting it inside something you ship**

| | What you need |
|---|---|
| Ship it in your product, publishing your modified source | ✅ nothing — the AGPL covers it |
| Ship it in your product, keeping your source closed | 💼 commercial licence |
| Build a service on it and decline the §13 source obligation | 💼 commercial licence |
| Get a warranty, an indemnity or a support commitment | 💼 commercial licence |

The short version : **using** it never requires a commercial licence, and never requires permission. Only **conveying** it while withholding the corresponding source does.

Copyright and attribution notices, the licence history and the third-party terms that apply to the published Docker image are recorded in [`NOTICE`][link-to-notice-file].

> **Already running a version from before the change ?** Nothing is withdrawn from you. This project was under CC BY-NC-SA 4.0 until the relicensing tracked in [#304](https://github.com/tigerblue77/Dell_iDRAC_fan_controller_Docker/issues/304), and that licence is irrevocable (§2(a)(1)) : the copies obtained under it keep those terms for good. Everything released from that point on comes under the AGPL, which permits more than the old licence did — commercial use included, for everybody — at the cost of the one obligation in the first table, which never triggers unless you redistribute the program.

[agpl]: https://www.gnu.org/licenses/agpl-3.0
[agpl-shield]: https://img.shields.io/badge/License-AGPL%20v3-blue.svg
[commercial-shield]: https://img.shields.io/badge/Commercial%20licence-available-brightgreen.svg
[link-to-license-file]: ./LICENSE
[link-to-commercial-license-file]: ./LICENSE-COMMERCIAL.md
[link-to-notice-file]: ./NOTICE

<p align="right">(<a href="#top">back to top</a>)</p>
