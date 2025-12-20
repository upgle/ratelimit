package redis

import (
	"sync"

	"github.com/cespare/xxhash/v2"
)

// CountMinSketch is a probabilistic data structure for estimating frequencies of elements.
// It uses multiple hash functions and a 2D array of counters to estimate the frequency
// of any element with a small probability of overestimation.
type CountMinSketch struct {
	width    uint32
	depth    uint32
	counters [][]uint32
	seeds    []uint64
	mu       sync.RWMutex
}

// NewCountMinSketch creates a new Count-Min Sketch with the given memory budget and depth.
// memoryBytes: total memory to use for counters (will be rounded down to fit depth)
// depth: number of hash functions (typically 4-5, more depth = lower error rate)
func NewCountMinSketch(memoryBytes int, depth int) *CountMinSketch {
	if depth < 2 {
		depth = 2
	}
	if depth > 8 {
		depth = 8
	}

	// Each counter is 4 bytes (uint32)
	// Total counters = width * depth
	// Memory = width * depth * 4
	width := uint32(memoryBytes / (depth * 4))
	if width < 256 {
		width = 256 // Minimum width
	}

	counters := make([][]uint32, depth)
	seeds := make([]uint64, depth)

	for i := 0; i < depth; i++ {
		counters[i] = make([]uint32, width)
		// Use different seeds for each row's hash function
		seeds[i] = uint64(i)*0x9E3779B97F4A7C15 + 0x517CC1B727220A95
	}

	return &CountMinSketch{
		width:    width,
		depth:    uint32(depth),
		counters: counters,
		seeds:    seeds,
	}
}

// hash computes the hash for a given key and seed, returning an index within width
func (cms *CountMinSketch) hash(key string, seed uint64) uint32 {
	h := xxhash.New()
	// Write seed as bytes
	seedBytes := []byte{
		byte(seed), byte(seed >> 8), byte(seed >> 16), byte(seed >> 24),
		byte(seed >> 32), byte(seed >> 40), byte(seed >> 48), byte(seed >> 56),
	}
	h.Write(seedBytes)
	h.Write([]byte(key))
	return uint32(h.Sum64() % uint64(cms.width))
}

// Increment adds delta to the frequency estimate of the key and returns the new minimum estimate.
func (cms *CountMinSketch) Increment(key string, delta uint32) uint32 {
	cms.mu.Lock()
	defer cms.mu.Unlock()

	minCount := uint32(0xFFFFFFFF)

	for i := uint32(0); i < cms.depth; i++ {
		idx := cms.hash(key, cms.seeds[i])
		// Prevent overflow
		newVal := cms.counters[i][idx] + delta
		if newVal < cms.counters[i][idx] {
			newVal = 0xFFFFFFFF // Cap at max uint32
		}
		cms.counters[i][idx] = newVal

		if newVal < minCount {
			minCount = newVal
		}
	}

	return minCount
}

// Estimate returns the estimated frequency of the key (minimum across all rows).
func (cms *CountMinSketch) Estimate(key string) uint32 {
	cms.mu.RLock()
	defer cms.mu.RUnlock()

	minCount := uint32(0xFFFFFFFF)

	for i := uint32(0); i < cms.depth; i++ {
		idx := cms.hash(key, cms.seeds[i])
		count := cms.counters[i][idx]
		if count < minCount {
			minCount = count
		}
	}

	return minCount
}

// Decay multiplies all counters by the given factor (0 < factor < 1).
// This is used to adapt to changing traffic patterns and prevent counter overflow.
func (cms *CountMinSketch) Decay(factor float64) {
	if factor <= 0 || factor >= 1 {
		return
	}

	cms.mu.Lock()
	defer cms.mu.Unlock()

	for i := uint32(0); i < cms.depth; i++ {
		for j := uint32(0); j < cms.width; j++ {
			cms.counters[i][j] = uint32(float64(cms.counters[i][j]) * factor)
		}
	}
}

// Reset clears all counters to zero.
func (cms *CountMinSketch) Reset() {
	cms.mu.Lock()
	defer cms.mu.Unlock()

	for i := uint32(0); i < cms.depth; i++ {
		for j := uint32(0); j < cms.width; j++ {
			cms.counters[i][j] = 0
		}
	}
}

// MemoryUsage returns the approximate memory usage in bytes.
func (cms *CountMinSketch) MemoryUsage() int {
	return int(cms.width) * int(cms.depth) * 4
}

// Width returns the width of the sketch.
func (cms *CountMinSketch) Width() uint32 {
	return cms.width
}

// Depth returns the depth of the sketch.
func (cms *CountMinSketch) Depth() uint32 {
	return cms.depth
}
