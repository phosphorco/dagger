package daggercmd

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Check exporter admission at the real command/frontend boundary, before any
// engine connection. Check exit semantics are exercised by native workloads.
func TestHeadlessCommandTelemetry(t *testing.T) {
	t.Chdir(t.TempDir())
	oldFrontend, oldSilent, oldSkip := Frontend, silent, skipSharedTelemetryExporters
	oldList := checksListMode
	oldCheckContext, oldRunContext := checksCmd.Context(), apiWithSessionCmd.Context()
	t.Cleanup(func() {
		Frontend, silent, skipSharedTelemetryExporters = oldFrontend, oldSilent, oldSkip
		checksListMode = oldList
		checksCmd.SetContext(oldCheckContext)
		apiWithSessionCmd.SetContext(oldRunContext)
	})
	skipSharedTelemetryExporters = false
	for _, command := range []string{"check", "check-list", "with-session"} {
		for _, headless := range []bool{false, true} {
			name := command + "/visible"
			if headless {
				name = command + "/silent"
			}
			t.Run(name, func(t *testing.T) {
				silent = headless
				checksListMode = command == "check-list"
				localSpans := tracetest.NewInMemoryExporter()
				localLogs := new(countingLogExporter)
				localMetrics := new(countingMetricExporter)
				remoteSpans := tracetest.NewInMemoryExporter()
				remoteLogs := new(countingLogExporter)
				remoteMetrics := new(countingMetricExporter)
				var observed bool
				frontend := &idtui.FrontendMock{
					SpanExporterFunc:   func() sdktrace.SpanExporter { return localSpans },
					LogExporterFunc:    func() sdklog.Exporter { return localLogs },
					MetricExporterFunc: func() sdkmetric.Exporter { return localMetrics },
					RunFunc: func(ctx context.Context, _ dagui.FrontendOpts, _ func(context.Context) (cleanups.CleanupF, error)) error {
						observed = true
						cfg, cloud := engineTelemetryConfigWithCloud(ctx, func(context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
							return remoteSpans, remoteLogs, remoteMetrics, true
						})
						defer func() {
							for _, processor := range cfg.SpanProcessors {
								require.NoError(t, processor.Shutdown(ctx))
							}
							for _, exporter := range cfg.LiveMetricExporters {
								require.NoError(t, exporter.Shutdown(ctx))
							}
						}()
						if headless && !checksListMode {
							require.Empty(t, cfg.LiveTraceExporters, "silent commands must not retain the progress graph")
						} else {
							require.Equal(t, []sdktrace.SpanExporter{localSpans}, cfg.LiveTraceExporters)
						}
						// Headless operation must retain independently configured telemetry.
						require.True(t, cfg.Detect)
						require.True(t, cloud.configured())
						require.Equal(t, []sdklog.Exporter{remoteLogs}, cfg.LiveLogExporters)
						require.Len(t, cfg.SpanProcessors, 1)
						require.Len(t, cfg.LiveMetricExporters, 1+len(cfg.LiveTraceExporters))
						return nil
					},
				}
				Frontend = frontend
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if command != "with-session" {
					checksCmd.SetContext(ctx)
					require.NoError(t, runChecksCommand(checksCmd, nil))
				} else {
					apiWithSessionCmd.SetContext(ctx)
					require.NoError(t, run(apiWithSessionCmd, []string{"true"}))
				}
				require.True(t, observed)
			})
		}
	}
}
