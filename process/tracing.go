// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package process

import (
	"context"
	"flag"
	"github.com/spacemonkeygo/monkit/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/sdk/trace"
	ctxtrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"storj.io/common/identity"
	"storj.io/common/telemetry"
)

var (
	tracingEnabled        = flag.Bool("tracing.enabled", true, "whether tracing collector is enabled")
	tracingSamplingRate   = flag.Float64("tracing.sample", 1, "samples a given fraction of traces. Fractions >= 1 will always sample. Fractions < 0 are treated as zero.")
	tracingAgent          = flag.String("tracing.agent-addr", flagDefault("127.0.0.1:5775", "agent.tracing.datasci.storj.io:5775"), "address for tracing agent/endpoint")
	tracingApp            = flag.String("tracing.app", filepath.Base(os.Args[0]), "application name for tracing identification")
	tracingAppEnvironment = flag.String("tracing.app-environment", flagDefault("dev", "release"), "application environment")
	tracingQueueSize      = flag.Int("tracing.queue-size", 2048, "the maximum queue size to buffer spans for delayed processing.")
	tracingBatchSize      = flag.Int("tracing.batch-size", 512, "the maximum number of spans to process in a single batch")
)

const (
	instanceIDKey  = "instanceID"
	hostnameKey    = "hostname"
	environmentKey = "environment"
)

// InitTracing initializes distributed tracing with an instance ID.
func InitTracing(ctx context.Context, log *zap.Logger, exp func(string) trace.SpanExporter, instanceID string) (cancel func(), err error) {
	return initTracing(ctx, log, exp, instanceID, "")
}

// InitTracingWithCertPath initializes distributed tracing with certificate path.
func InitTracingWithCertPath(ctx context.Context, log *zap.Logger, exp func(string) trace.SpanExporter, certDir string) (cancel func(), err error) {
	return initTracing(ctx, log, exp, nodeIDFromCertPath(ctx, log, certDir), "")
}

// InitTracingWithHostname initializes distributed tracing with nodeID and hostname.
func InitTracingWithHostname(ctx context.Context, log *zap.Logger, exp func(string) trace.SpanExporter, certDir string) (cancel func(), err error) {
	hostname, err := os.Hostname()
	if err != nil {
		log.Error("Could not read hostname for tracing setup", zap.Error(err))
		return nil, err
	}

	return initTracing(ctx, log, exp, nodeIDFromCertPath(ctx, log, certDir), hostname)
}

func initTracing(ctx context.Context, log *zap.Logger, exp func(string) trace.SpanExporter, instanceID, hostname string) (cancel func(), err error) {

	if exp == nil {
		log.Debug("Tracing exporter not provided")
		return nil, nil
	}

	if !*tracingEnabled {
		log.Debug("Anonymized tracing disabled")
		return nil, nil
	}

	log.Info("Anonymized tracing enabled")

	if len(instanceID) == 0 {
		instanceID = telemetry.DefaultInstanceID()
	}

	processName := *tracingApp + "-" + *tracingAppEnvironment
	if len(processName) > maxInstanceLength {
		processName = processName[:maxInstanceLength]
	}

	tp := trace.NewTracerProvider(
		trace.WithSampler(
			trace.ParentBased(
				trace.TraceIDRatioBased(*tracingSamplingRate),
				trace.WithRemoteParentSampled(trace.TraceIDRatioBased(*tracingSamplingRate)),
				trace.WithRemoteParentNotSampled(trace.TraceIDRatioBased(*tracingSamplingRate)),
				trace.WithLocalParentSampled(trace.TraceIDRatioBased(*tracingSamplingRate)),
				trace.WithLocalParentNotSampled(trace.TraceIDRatioBased(*tracingSamplingRate)))),
		trace.WithSpanProcessor(
			trace.NewBatchSpanProcessor(exp(*tracingAgent),
				trace.WithMaxExportBatchSize(*tracingBatchSize),
				trace.WithMaxQueueSize(*tracingQueueSize))),
		trace.WithResource(
			resource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceName(processName),
				attribute.String(instanceIDKey, instanceID),
				attribute.String(hostnameKey, hostname),
				attribute.String(environmentKey, *tracingAppEnvironment))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	monkit.Default.ObserveTraces(func(t *monkit.Trace) {
		t.ObserveSpansCtx(MyCoolObserver(func() {}))
	})
	return func() {
		if err := tp.Shutdown(ctx); err != nil {
			log.Error("failed to shutdown tracer provider", zap.Error(err))
		}
	}, nil

}

type MyCoolObserver func()

func (m MyCoolObserver) Start(ctx context.Context, s *monkit.Span) context.Context {
	s.Context, _ = otel.GetTracerProvider().Tracer("").Start(s.Context, s.Func().Scope().Name())
	otel.GetTracerProvider().Tracer("").Start(s.Context, "test_child_span")
	return s.Context
}

func (m MyCoolObserver) Finish(ctx context.Context, s *monkit.Span, err error, panicked bool, finish time.Time) {
	span := ctxtrace.SpanFromContext(s.Context)
	span.End()
}

func nodeIDFromCertPath(ctx context.Context, log *zap.Logger, certPath string) string {
	if certPath == "" {
		return ""
	}
	nodeID, err := identity.NodeIDFromCertPath(certPath)
	if err != nil {
		log.Debug("Could not read identity for tracing setup", zap.Error(err))
		return ""
	}

	return nodeID.String()
}
