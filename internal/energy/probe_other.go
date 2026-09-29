//go:build !darwin

package energy

// Read has no coalition counters to read off macOS.
func Read(named func(coalition uint64) bool) ([]Reading, error) {
	return nil, ErrUnsupported
}
