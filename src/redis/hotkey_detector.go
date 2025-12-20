package redis

import (
	"sync"
	"time"
)

// HotKeyDetector detects hot keys using Count-Min Sketch and maintains
// a set of currently hot keys with LRU eviction.
type HotKeyDetector struct {
	cms           *CountMinSketch
	hotThreshold  uint32              // Frequency threshold to be considered hot
	hotKeys       map[string]struct{} // Current set of hot keys (fast lookup)
	hotKeysList   []string            // LRU-ordered list for eviction (most recent at end)
	maxHotKeys    int                 // Maximum number of hot keys to track
	decayInterval time.Duration       // How often to decay CMS counters
	decayFactor   float64             // Decay factor (0.5 = halve counters)
	lastDecayTime time.Time
	mu            sync.RWMutex
}

// HotKeyDetectorConfig holds configuration for the hot key detector.
type HotKeyDetectorConfig struct {
	SketchMemoryBytes int           // Memory for Count-Min Sketch
	SketchDepth       int           // Depth of Count-Min Sketch (number of hash functions)
	HotThreshold      uint32        // Frequency threshold to consider a key hot
	MaxHotKeys        int           // Maximum number of hot keys to track
	DecayInterval     time.Duration // Interval for decaying CMS counters
	DecayFactor       float64       // Factor to multiply counters by during decay (0-1)
}

// DefaultHotKeyDetectorConfig returns a default configuration.
func DefaultHotKeyDetectorConfig() HotKeyDetectorConfig {
	return HotKeyDetectorConfig{
		SketchMemoryBytes: 10 * 1024 * 1024, // 10MB
		SketchDepth:       4,
		HotThreshold:      100,
		MaxHotKeys:        10000,
		DecayInterval:     10 * time.Second,
		DecayFactor:       0.5,
	}
}

// NewHotKeyDetector creates a new hot key detector with the given configuration.
func NewHotKeyDetector(config HotKeyDetectorConfig) *HotKeyDetector {
	if config.DecayFactor <= 0 || config.DecayFactor >= 1 {
		config.DecayFactor = 0.5
	}
	if config.MaxHotKeys <= 0 {
		config.MaxHotKeys = 10000
	}
	if config.HotThreshold <= 0 {
		config.HotThreshold = 100
	}

	return &HotKeyDetector{
		cms:           NewCountMinSketch(config.SketchMemoryBytes, config.SketchDepth),
		hotThreshold:  config.HotThreshold,
		hotKeys:       make(map[string]struct{}),
		hotKeysList:   make([]string, 0, config.MaxHotKeys),
		maxHotKeys:    config.MaxHotKeys,
		decayInterval: config.DecayInterval,
		decayFactor:   config.DecayFactor,
		lastDecayTime: time.Now(),
	}
}

// RecordAccess records an access to the key and returns whether the key is hot.
// This method increments the CMS counter and may promote the key to hot status.
func (d *HotKeyDetector) RecordAccess(key string) bool {
	// First, check for periodic decay
	d.maybeDecay()

	// Increment CMS counter (CMS has its own lock)
	count := d.cms.Increment(key, 1)

	// Fast path: check if already hot
	if d.isHot(key) {
		// Move to end of LRU list (most recently used)
		d.touchHotKey(key)
		return true
	}

	// Check if should become hot
	if count >= d.hotThreshold {
		d.promoteToHot(key)
		return true
	}

	return false
}

// RecordAccessWithDelta records multiple accesses to the key and returns whether the key is hot.
func (d *HotKeyDetector) RecordAccessWithDelta(key string, delta uint32) bool {
	d.maybeDecay()

	count := d.cms.Increment(key, delta)

	if d.isHot(key) {
		d.touchHotKey(key)
		return true
	}

	if count >= d.hotThreshold {
		d.promoteToHot(key)
		return true
	}

	return false
}

// IsHot checks if a key is currently in the hot key set.
func (d *HotKeyDetector) IsHot(key string) bool {
	return d.isHot(key)
}

// isHot is the internal lock-free hot check (caller must handle synchronization if needed).
func (d *HotKeyDetector) isHot(key string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, exists := d.hotKeys[key]
	return exists
}

// touchHotKey moves the key to the end of the LRU list.
func (d *HotKeyDetector) touchHotKey(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Find and remove from current position
	for i, k := range d.hotKeysList {
		if k == key {
			d.hotKeysList = append(d.hotKeysList[:i], d.hotKeysList[i+1:]...)
			break
		}
	}
	// Add to end (most recently used)
	d.hotKeysList = append(d.hotKeysList, key)
}

// promoteToHot adds a key to the hot key set, evicting LRU if necessary.
func (d *HotKeyDetector) promoteToHot(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Double-check after acquiring lock
	if _, exists := d.hotKeys[key]; exists {
		return
	}

	// Evict LRU if at capacity
	for len(d.hotKeysList) >= d.maxHotKeys {
		evictKey := d.hotKeysList[0]
		d.hotKeysList = d.hotKeysList[1:]
		delete(d.hotKeys, evictKey)
	}

	// Add new hot key
	d.hotKeys[key] = struct{}{}
	d.hotKeysList = append(d.hotKeysList, key)
}

// maybeDecay performs periodic decay of CMS counters if the decay interval has elapsed.
func (d *HotKeyDetector) maybeDecay() {
	d.mu.RLock()
	shouldDecay := time.Since(d.lastDecayTime) >= d.decayInterval
	d.mu.RUnlock()

	if !shouldDecay {
		return
	}

	d.mu.Lock()
	// Double-check after acquiring write lock
	if time.Since(d.lastDecayTime) < d.decayInterval {
		d.mu.Unlock()
		return
	}
	d.lastDecayTime = time.Now()
	d.mu.Unlock()

	// Decay CMS counters (CMS has its own lock)
	d.cms.Decay(d.decayFactor)

	// Clean up keys that may have fallen below threshold
	d.cleanupColdKeys()
}

// cleanupColdKeys removes keys from the hot set that have fallen below the threshold.
func (d *HotKeyDetector) cleanupColdKeys() {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Build new list of still-hot keys
	newList := make([]string, 0, len(d.hotKeysList))
	for _, key := range d.hotKeysList {
		if d.cms.Estimate(key) >= d.hotThreshold {
			newList = append(newList, key)
		} else {
			delete(d.hotKeys, key)
		}
	}
	d.hotKeysList = newList
}

// GetHotKeyCount returns the current number of hot keys.
func (d *HotKeyDetector) GetHotKeyCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.hotKeys)
}

// GetEstimate returns the estimated frequency of a key.
func (d *HotKeyDetector) GetEstimate(key string) uint32 {
	return d.cms.Estimate(key)
}

// Reset clears all hot keys and resets the CMS.
func (d *HotKeyDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.cms.Reset()
	d.hotKeys = make(map[string]struct{})
	d.hotKeysList = make([]string, 0, d.maxHotKeys)
	d.lastDecayTime = time.Now()
}

// MemoryUsage returns the approximate memory usage in bytes.
func (d *HotKeyDetector) MemoryUsage() int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	// CMS memory
	cmsMemory := d.cms.MemoryUsage()

	// Hot keys map and list (approximate)
	// Each key in map: ~48 bytes overhead + key length
	// Each key in list: ~16 bytes overhead + key length
	hotKeyMemory := 0
	for key := range d.hotKeys {
		hotKeyMemory += 48 + len(key) + 16 + len(key)
	}

	return cmsMemory + hotKeyMemory
}
