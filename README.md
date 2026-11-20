<p align="center">
  <img src="assets/logo.svg" alt="ampere" width="420">
</p>

<p align="center">
  <a href="https://github.com/sathiraumesh/ampere/actions/workflows/test.yml">
    <img src="https://github.com/sathiraumesh/ampere/actions/workflows/test.yml/badge.svg" alt="test">
  </a>
</p>

# ampere

`ampere` estimates how much energy each process uses on a Linux machine.
Support for containers and Kubernetes pods is planned.

It combines two kernel sources:

- **Energy** from RAPL counters in `/sys/class/powercap`
- **CPU time** per process from `/proc`

```text
process energy = CPU energy (RAPL) × process CPU time / active CPU time
```

> Status: early development. `ampere status` and `ampere measure`
> (with `--pid` for one process) work.

## Requirements

- Linux with RAPL (Intel, or AMD Zen via the same `intel-rapl` driver)
- Go 1.26 or newer to build
- Root to read energy: `energy_uj` has been root-only since the PLATYPUS
  side-channel fix (2020)

## Build

```bash
make build            # → bin/ampere
make help             # list all targets
```

The version, commit and build date are embedded at build time:

```bash
make build VERSION=v0.1.0
./bin/ampere --version
# ampere v0.1.0 (commit a1b2c3d, built 2026-11-20T10:00:00Z, go1.26.2 linux/amd64)
```

## Usage

### Check what is available

```bash
./bin/ampere status         # as a normal user
sudo ./bin/ampere status    # as root, energy becomes readable
make status-root            # same, builds first
```

Example output (laptop, AMD Ryzen 7 PRO 8840U, not root):

```text
CPU energy sources:
 rapl (2 zones)
      ID              NAME       PARENT        MAX (µJ)     READABLE
      intel-rapl:0    package-0  -             65532610987  false
      intel-rapl:0:0  core       intel-rapl:0  65532610987  false
Using: rapl
```

| Column | Meaning |
|---|---|
| ID | powercap folder name |
| NAME | zone type: `package-N`, `core`, `uncore`, `dram`, `psys` |
| PARENT | zone this one is nested in; its energy is already in the parent |
| MAX (µJ) | value where `energy_uj` wraps back to 0 |
| READABLE | `energy_uj` can be read (needs root) |

### Read CPU energy and power

```bash
sudo ./bin/ampere measure                 # over 1 s
sudo ./bin/ampere measure --interval 5s   # over 5 s
sudo ./bin/ampere measure -i 1s -n 20     # 20 samples, 1 s each, back to back
sudo ./bin/ampere measure --source rapl   # force a source (default: first available)
sudo ./bin/ampere measure -o json         # JSON instead of a table
make measure-root                         # same, builds first
```

```text
DEVICE  SOURCE  INTERVAL (s)  ENERGY (J)  POWER (W)
cpu     rapl    1.000213      12.345678   12.34
```

With `-o json`, each sample is one JSON object per line (JSON Lines), so
repeated runs can be appended to a file:

```bash
sudo ./bin/ampere measure -o json -f samples.jsonl   # appends one line per run
```

```json
{"device":"cpu","source":"rapl","start":"2026-11-20T10:00:00Z","end":"2026-11-20T10:00:01.000213Z","interval_s":1.000213,"energy_j":12.345678,"power_w":12.34}
```

`measure` takes two readings of the preferred source, `--interval` apart.
With `--count N` it takes N samples in a row: each one starts at the
previous one's end reading, so there are no gaps between them. The table
header is printed once; with `-f`, every sample is appended as it is taken.
If the counter wraps to 0 during a sample, it reports an error; run it again.

### Energy of one process

```bash
sudo ./bin/ampere measure --pid 1234               # over 1 s
sudo ./bin/ampere measure --pid 1234 -i 1s -n 20   # 20 samples, back to back
sudo ./bin/ampere measure --pid 1234 -o json -f pid.jsonl
```

For the device total and **every zone the source reports** (on RAPL:
`package-N`, `core`, `uncore`, `dram`, `psys`, whichever this machine has),
the energy of each interval is split by CPU time from `/proc`:

```text
active energy  = energy × active ticks / (active + idle ticks)
idle energy    = energy × idle ticks   / (active + idle ticks)
process energy = active energy × process ticks / active ticks
```

- **active** ticks: user, nice, system, irq, softirq (`/proc/stat`)
- **idle** ticks: idle, iowait. Steal is left out (another VM ran then).
- **process** ticks: utime + stime of the process (`/proc/<pid>/stat`)

Non-CPU zones such as `dram` are split by the same CPU time, so their
active/idle/process numbers are an estimate, not a separate measurement.
Parent zones include their children: do not sum zone rows.

```text
PID   COMM    INTERVAL (s)  CPU ACTIVE  PID CPU  ENERGY (J)  ACTIVE (J)  IDLE (J)  PID (J)   PID (W)
1234  worker  1.000210      25.0%       40.0%    12.000000   3.000000    9.000000  1.200000  1.20

ZONE            NAME       PARENT        ENERGY (J)  ACTIVE (J)  IDLE (J)  PID (J)   PID (W)
intel-rapl:0    package-0  -             12.000000   3.000000    9.000000  1.200000  1.20
intel-rapl:0:0  core       intel-rapl:0  5.000000    1.250000    3.750000  0.500000  0.50
```

The command fails if the process exits, or its PID is reused by a new
process, during the run.

## How it works

### CPU energy sources

Each energy source implements one interface, so new sources can be added
without changing the rest of the tool:

```go
type Source interface {
	Name() string                      // "rapl", ...
	Err() error                        // nil if available here
	Read() (energyUJ uint64, err error) // current counter in µJ
}
```

`ampere` checks all sources in priority order and measures with the first
available one. Sources are never summed: on AMD, RAPL and hwmon read the
same hardware counter.

| Source | Status |
|---|---|
| RAPL (`/sys/class/powercap`) | ✓ implemented |
| hwmon (AMD energy driver) | planned |

### Which RAPL zones are counted

`Read()` sums only the **`package-N`** zones (one per CPU socket):

- **Subzones** (`core`, `uncore`, `dram` under a package) are skipped
  because their energy is already inside the package.
- **`psys`** is skipped because it covers the package and more.

## Project layout

```text
ampere/
├── cmd/main.go                 entry point
├── internal/
│   ├── cli/                    cobra commands (root, status) + version info
│   ├── devices/                status of all devices
│   │   └── cpu/                cpu.Source interface, source selection
│   │       └── rapl/           RAPL zones and rapl.Source
│   ├── host/                   host-level data and energy attribution
│   │   ├── attribution/        active/idle/process energy split (--pid)
│   │   └── proc/               CPU time from /proc (host active/idle, per process)
│   └── utils/                  small helpers (ReadUint)
├── assets/logo.svg
└── Makefile
```

## Development

```bash
make test     # run all tests (no root or RAPL hardware needed)
make check    # fmt + vet + test
```

Tests build a fake `/sys/class/powercap` tree in a temp folder, so they run
anywhere, including CI and machines without RAPL.

## Roadmap

- [x] `status`: list energy sources and RAPL zones
- [x] `--version` with build info
- [x] `measure`: whole-CPU energy and power over an interval
- [x] `measure --pid <pid>`: energy of one process over an interval, active/idle per zone
- [ ] Measured idle baseline as an alternative to the time-based split
- [ ] Containers and pods via cgroup v2
- [ ] More sources: hwmon, BMC/Redfish wall power
