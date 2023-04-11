package metrics

import stats "github.com/lyft/gostats"

type MetricReporter interface {
	NewGauge(name string) Gauge
	NewCounter(name string) Counter
	NewCounterWithTags(name string, tags map[string]string) Counter
	NewTimer(name string) Timer
	Scope(name string) MetricReporter
}

func NewStatsMetricReporter(scope stats.Scope) *StatsMetricReporter {
	return &StatsMetricReporter{scope: scope}
}

type StatsMetricReporter struct {
	scope stats.Scope
}

func (s *StatsMetricReporter) NewGauge(name string) Gauge {
	return s.scope.NewGauge(name)
}

func (s *StatsMetricReporter) NewCounter(name string) Counter {
	return s.scope.NewCounter(name)
}

func (s *StatsMetricReporter) NewCounterWithTags(name string, tags map[string]string) Counter {
	return s.scope.NewCounterWithTags(name, tags)
}

func (s *StatsMetricReporter) NewTimer(name string) Timer {
	return s.scope.NewTimer(name)
}

func (s *StatsMetricReporter) Scope(name string) MetricReporter {
	return NewStatsMetricReporter(s.scope.Scope(name))
}

// A Counter is an always incrementing stat.
type Counter interface {
	// Add increments the Counter by the argument's value.
	Add(uint64)

	// Inc increments the Counter by 1.
	Inc()

	// Value returns the current value of the Counter as a uint64.
	Value() uint64
}

// A Timer is used to flush timing statistics.
type Timer interface {
	// AddValue flushs the timer with the argument's value.
	AddValue(float64)
}

// A Gauge is a stat that can increment and decrement.
type Gauge interface {
	// Add increments the Gauge by the argument's value.
	Add(uint64)

	// Sub decrements the Gauge by the argument's value.
	Sub(uint64)

	// Inc increments the Gauge by 1.
	Inc()

	// Dec decrements the Gauge by 1.
	Dec()

	// Set sets the Gauge to a value.
	Set(uint64)

	// String returns the current value of the Gauge as a string.
	String() string

	// Value returns the current value of the Gauge as a uint64.
	Value() uint64
}
