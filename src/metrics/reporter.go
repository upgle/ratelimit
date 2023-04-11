package metrics

import stats "github.com/lyft/gostats"

type MetricReporter interface {
	NewCounter(name string) Counter
	NewTimer(name string) Timer
}

func NewStatsMetricReporter(scope stats.Scope) *StatsMetricReporter {
	return &StatsMetricReporter{scope: scope}
}

type StatsMetricReporter struct {
	scope stats.Scope
}

func (s StatsMetricReporter) NewCounter(name string) Counter {
	return s.scope.NewCounter(name)
}

func (s StatsMetricReporter) NewTimer(name string) Timer {
	return s.scope.NewTimer(name)
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
