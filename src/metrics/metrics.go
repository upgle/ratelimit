package metrics

import (
	"context"
	"time"

	"google.golang.org/grpc"
)

type serverMetrics struct {
	totalRequests Counter
	responseTime  Timer
}

// ServerReporter reports server-side metrics for ratelimit gRPC server
type ServerReporter struct {
	reporter MetricReporter
}

func newServerMetrics(reporter MetricReporter, fullMethod string) *serverMetrics {
	_, methodName := splitMethodName(fullMethod)
	ret := serverMetrics{}
	ret.totalRequests = reporter.NewCounter(methodName + ".total_requests")
	ret.responseTime = reporter.NewTimer(methodName + ".response_time")
	return &ret
}

// NewServerReporter returns a ServerReporter object.
func NewServerReporter(reporter MetricReporter) *ServerReporter {
	return &ServerReporter{
		reporter: reporter,
	}
}

// UnaryServerInterceptor is a gRPC server-side interceptor that provides server metrics for Unary RPCs.
func (r *ServerReporter) UnaryServerInterceptor() func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		s := newServerMetrics(r.reporter, info.FullMethod)
		s.totalRequests.Inc()
		resp, err := handler(ctx, req)
		s.responseTime.AddValue(float64(time.Since(start).Milliseconds()))
		return resp, err
	}
}
