package clientdb

import (
	"encoding/binary"
	"fmt"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestCausalLinkTargetsPreservesFirstSeenCauses(t *testing.T) {
	const traceID = "00000000000000000000000000000001"
	row := Span{SpanID: "0000000000000009", Links: linksJSON(t,
		spanLink(t, traceID, "0000000000000002", ""),
		spanLink(t, traceID, "0000000000000003", "wait"),
		spanLink(t, traceID, "0000000000000001", telemetry.LinkPurposeCause),
		spanLink(t, traceID, "0000000000000002", telemetry.LinkPurposeCause),
		spanLink(t, traceID, "0000000000000009", ""),
		spanLink(t, traceID, "0000000000000004", telemetry.LinkPurposeErrorOrigin),
		sdktrace.Link{},
	)}
	require.Equal(t, []string{"0000000000000002", "0000000000000001"}, causalLinkTargets(row))
	require.Nil(t, causalLinkTargets(Span{}))
}

func causalFanoutRow(t testing.TB, count int) Span {
	t.Helper()
	links := make([]*otlptracev1.Span_Link, count)
	for i := range links {
		id := make([]byte, 8)
		binary.BigEndian.PutUint64(id, uint64(i+1))
		links[i] = &otlptracev1.Span_Link{TraceId: []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, SpanId: id}
	}
	encoded, err := MarshalProtoList(links)
	require.NoError(t, err)
	return Span{SpanID: "ffffffffffffffff", Links: encoded}
}

func TestCausalLinkTargetsRetainsLargeFanout(t *testing.T) {
	const count = 2000
	got := causalLinkTargets(causalFanoutRow(t, count))
	require.Len(t, got, count)
	for i, target := range got {
		require.Equal(t, fmt.Sprintf("%016x", i+1), target)
	}
}

func BenchmarkCausalLinkTargets(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			row := causalFanoutRow(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if got := causalLinkTargets(row); len(got) != count {
					b.Fatalf("lost causal targets: got %d, want %d", len(got), count)
				}
			}
		})
	}
}
