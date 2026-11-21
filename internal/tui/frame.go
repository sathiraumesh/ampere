package tui

import "time"

// Row is one process or container in a frame.
type Row struct {
	PID       int    // process only
	Comm      string // process only
	Container string // container only: full ID
	Name      string // container only: name, e.g. "web" ("" if unknown)
	Runtime   string // container only: docker, podman, containerd

	CPU    float64 // share of the machine's active CPU time, 0 to 1
	W      float64 // average power over the last interval
	TotalJ float64 // energy since ampere top started
}

// ZoneRow is one RAPL zone in a frame.
type ZoneRow struct {
	ID, Name, Parent string
	W                float64
}

// Frame is everything shown for one interval.
type Frame struct {
	At          time.Time
	IntervalS   float64
	Device      string
	Source      string
	PowerW      float64 // the machine (device total)
	ActiveShare float64 // share of CPU time that was active, 0 to 1
	TotalJ      float64 // machine energy since start
	Zones       []ZoneRow
	Processes   []Row // sorted by W, highest first
	Containers  []Row // sorted by W, highest first
}
