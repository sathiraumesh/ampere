<p align="center">
  <img src="assets/logo.svg" alt="ampere" width="420">
</p>

<p align="center">
  <a href="https://github.com/sathiraumesh/ampere/actions/workflows/test.yml">
    <img src="https://github.com/sathiraumesh/ampere/actions/workflows/test.yml/badge.svg" alt="test">
  </a>
</p>

# ampere

`ampere` is command line tool that estimates how much energy of running process on a Linux machines.
It also supports estimation of energy of containers with runtimes Docker, Podman and Containerd.


It currently supports only

-  Energy estimation through RAPL counters.

```text
process energy = CPU energy (RAPL) × process CPU time / active CPU time
```

## Requirements

- Linux with RAPL (Intel, or AMD Zen via the same `intel-rapl` driver)
- Go 1.26 or newer to build

## Build

```bash
make build            # → bin/ampere
make help             # list all targets
```

The version, commit and build date are embedded at build time:



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
## Energy attribution

`ampere` measures the machine's energy over an interval and gives a process
or container its share, by CPU time. It reads three counters at the start
and end of every interval:

| Counter | From | Gives |
|---|---|---|
| Energy | RAPL, `/sys/class/powercap/intel-rapl:*/energy_uj` | joules used by the CPU, total and per zone |
| Host CPU time | `/proc/stat` | ticks the CPUs were **active** and **idle** |
| Target CPU time | process: `/proc/<pid>/stat` (utime + stime)<br>container: cgroup `cpu.stat` (`usage_usec`) | CPU time the target used |

The energy of each interval is split in two steps:

```text
active energy = energy × active ticks / (active + idle ticks)
target energy = active energy × target CPU time / active CPU time
target power  = target energy / interval
```

1. **Active vs idle.** Only the part of the energy spent while the CPUs were
   doing work is shared out. The idle part is what the machine uses just by
   being on; it belongs to the machine, not to any process or container.
2. **Target's share.** The target gets the fraction of the active CPU time
   that it used, capped at 100% because the counters are read a moment apart.

The same split is applied to the total and to every RAPL zone
(`package-N`, `core`, `uncore`, `dram`, `psys`, whichever the machine has).

**Example.** In 1 s the machine used 20 J and its CPUs were 25% active, so
5 J is active energy and 15 J idle energy. A container used 25% of the
active CPU time, so it gets 5 J × 25% = **1.25 J (1.25 W)**.

**What counts as active and idle:**

- active: user, nice, system, irq, softirq
- idle: idle, iowait
- steal (time the hypervisor gave to other VMs) is left out of both: this
  machine's CPUs did not run then

**Limits.** CPU time is a proxy for energy: two processes with the same CPU
time can use different energy (vector instructions, cache misses, frequency).
Zones such as `dram` are shared by CPU time too, so a target's share of them
is an estimate, not a measurement of that component.

## Roadmap

- [x] `status`: list energy sources and RAPL zones
- [x] `--version` with build info
- [x] `measure`: whole-CPU energy and power over an interval
- [x] `measure process <pid>`: energy of one process over an interval, active/idle per zone
- [x] `measure container <id>`: same for a Docker, Podman or containerd container
- [ ] Measured idle baseline as an alternative to the time-based split
- [ ] Kubernetes pods via cgroup v2
- [ ] More sources: hwmon, BMC/Redfish wall power
