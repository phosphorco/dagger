package idtui_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func reportSpan(id, parent byte, name string, complete bool) sdktrace.ReadOnlySpan {
	stub := tracetest.SpanStub{
		Name: name,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1},
			SpanID:  trace.SpanID{id},
		}),
		Parent: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1},
			SpanID:  trace.SpanID{parent},
		}),
		StartTime: time.Unix(100, 0),
	}
	if complete {
		stub.EndTime = stub.StartTime.Add(time.Second)
	}
	return stub.Snapshot()
}

func TestReportFinalRenderConcurrentExports(t *testing.T) {
	fe := idtui.NewASCIIReporterWithDB(io.Discard, dagui.NewDB())
	fe.SetPrimary(dagui.SpanID{SpanID: trace.SpanID{1}})
	fe.SetVerbosity(dagui.ShowCompletedVerbosity)
	exporter := fe.SpanExporter()
	require.NoError(t, exporter.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{
		reportSpan(1, 0, "command", false),
		reportSpan(2, 1, "collapsed", true),
		reportSpan(3, 2, "running descendant", false),
	}))

	start := make(chan struct{})
	exported := make(chan error, 1)
	go func() {
		<-start
		for id := byte(4); id < 68; id++ {
			if err := exporter.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{
				reportSpan(id, 2, "exported work", false),
			}); err != nil {
				exported <- err
				return
			}
			if err := exporter.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{
				reportSpan(id, 2, "exported work", true),
			}); err != nil {
				exported <- err
				return
			}
		}
		exported <- nil
	}()
	rendered := make(chan error, 1)
	go func() {
		<-start
		for range 32 {
			if err := fe.FinalRender(io.Discard); err != nil {
				rendered <- err
				return
			}
		}
		rendered <- nil
	}()
	close(start)
	exportErr, renderErr := <-exported, <-rendered
	require.NoError(t, exportErr)
	require.NoError(t, renderErr)

	failed := tracetest.SpanStubFromReadOnlySpan(reportSpan(68, 1, "retained failure", true))
	failed.Status = sdktrace.Status{Code: codes.Error, Description: "retained failure detail"}
	failed.Attributes = []attribute.KeyValue{attribute.String(telemetry.CheckNameAttr, "retained-check")}
	require.NoError(t, exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{failed.Snapshot()}))
	var output bytes.Buffer
	require.NoError(t, fe.FinalRender(&output))
	require.Contains(t, output.String(), "retained-check")
	require.Contains(t, output.String(), "retained failure detail")
}

func TestReportRunFinalizationConcurrentExports(t *testing.T) {
	t.Setenv("DAGGER_REPORT_HEARTBEAT", "0")
	fe := idtui.NewASCIIReporterWithDB(io.Discard, dagui.NewDB())
	fe.SetPrimary(dagui.SpanID{SpanID: trace.SpanID{1}})
	exporter := fe.SpanExporter()
	require.NoError(t, exporter.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{
		reportSpan(1, 0, "command", false),
		reportSpan(2, 1, "collapsed", true),
		reportSpan(3, 2, "running descendant", false),
	}))
	exported := make(chan error, 1)
	dotPath := filepath.Join(t.TempDir(), "trace.dot")
	err := fe.Run(t.Context(), dagui.FrontendOpts{DotOutputFilePath: dotPath}, func(ctx context.Context) (cleanups.CleanupF, error) {
		go func() {
			for id := byte(4); id < 68; id++ {
				for _, complete := range []bool{false, true} {
					if err := exporter.ExportSpans(ctx, []sdktrace.ReadOnlySpan{reportSpan(id, 2, "exported work", complete)}); err != nil {
						exported <- err
						return
					}
				}
			}
			exported <- nil
		}()
		return nil, nil
	})
	exportErr := <-exported
	require.NoError(t, err)
	require.NoError(t, exportErr)
	dot, readErr := os.ReadFile(dotPath)
	require.NoError(t, readErr)
	require.Contains(t, string(dot), "digraph")
	var output bytes.Buffer
	require.NoError(t, fe.FinalRender(&output))
}
