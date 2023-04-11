package memcached

import (
	"github.com/bradfitz/gomemcache/memcache"
	"github.com/envoyproxy/ratelimit/src/metrics"
)

type statsCollectingClient struct {
	c Client

	multiGetSuccess  metrics.Counter
	multiGetError    metrics.Counter
	incrementSuccess metrics.Counter
	incrementMiss    metrics.Counter
	incrementError   metrics.Counter
	addSuccess       metrics.Counter
	addError         metrics.Counter
	addNotStored     metrics.Counter
	keysRequested    metrics.Counter
	keysFound        metrics.Counter
}

func CollectStats(c Client, reporter metrics.MetricReporter) Client {
	return statsCollectingClient{
		c:                c,
		multiGetSuccess:  reporter.NewCounterWithTags("multiget", map[string]string{"code": "success"}),
		multiGetError:    reporter.NewCounterWithTags("multiget", map[string]string{"code": "error"}),
		incrementSuccess: reporter.NewCounterWithTags("increment", map[string]string{"code": "success"}),
		incrementMiss:    reporter.NewCounterWithTags("increment", map[string]string{"code": "miss"}),
		incrementError:   reporter.NewCounterWithTags("increment", map[string]string{"code": "error"}),
		addSuccess:       reporter.NewCounterWithTags("add", map[string]string{"code": "success"}),
		addError:         reporter.NewCounterWithTags("add", map[string]string{"code": "error"}),
		addNotStored:     reporter.NewCounterWithTags("add", map[string]string{"code": "not_stored"}),
		keysRequested:    reporter.NewCounter("keys_requested"),
		keysFound:        reporter.NewCounter("keys_found"),
	}
}

func (scc statsCollectingClient) GetMulti(keys []string) (map[string]*memcache.Item, error) {
	scc.keysRequested.Add(uint64(len(keys)))

	results, err := scc.c.GetMulti(keys)

	if err != nil {
		scc.multiGetError.Inc()
	} else {
		scc.keysFound.Add(uint64(len(results)))
		scc.multiGetSuccess.Inc()
	}

	return results, err
}

func (scc statsCollectingClient) Increment(key string, delta uint64) (newValue uint64, err error) {
	newValue, err = scc.c.Increment(key, delta)
	switch err {
	case memcache.ErrCacheMiss:
		scc.incrementMiss.Inc()
	case nil:
		scc.incrementSuccess.Inc()
	default:
		scc.incrementError.Inc()
	}
	return
}

func (scc statsCollectingClient) Add(item *memcache.Item) error {
	err := scc.c.Add(item)

	switch err {
	case memcache.ErrNotStored:
		scc.addNotStored.Inc()
	case nil:
		scc.addSuccess.Inc()
	default:
		scc.addError.Inc()
	}

	return err
}
