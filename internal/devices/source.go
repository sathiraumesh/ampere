package devices

// Device types. A source's Device() returns one of these.
const (
	CPU = "cpu"
	// later: GPU = "gpu", DRAM = "dram", ...
)

// Source is one way to measure a device's energy, e.g. RAPL for the CPU.
type Source interface {
	// Name identifies the implementation, e.g. "rapl".
	Name() string

	// Device is the device type this source measures, e.g. CPU ("cpu").
	Device() string

	// Err is nil if the source is available, otherwise the reason it is not.
	Err() error

	// Read returns the current energy counter in µJ (raw, no state).
	// Energy over an interval = Read() after − Read() before; the counter
	// can wrap to 0, so the caller must handle after < before.
	Read() (energyUJ uint64, err error)
}

// Available reports whether s was found on this machine.
func Available(s Source) bool { return s.Err() == nil }
