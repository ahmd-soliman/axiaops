package fake

import (
	"context"
	"testing"
	"time"

	"axiaops.io/shared/analyzer"
)

// BenchmarkFullPipeline_Enterprise tests the complete ingestion pipeline
// with the enterprise scenario (realistic multi-account data).
func BenchmarkFullPipeline_Enterprise(b *testing.B) {
	p := New("enterprise")
	ctx := context.Background()
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		records, _ := p.FetchCosts(ctx, start, end)
		usage, _ := p.FetchUsage(ctx, records, start, end)
		zombies := analyzer.Detect(records, usage, "test-account")
		_ = analyzer.Summarize(zombies)
	}
}

// BenchmarkFetchCosts measures cost fetching performance.
func BenchmarkFetchCosts(b *testing.B) {
	p := New("enterprise")
	ctx := context.Background()
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.FetchCosts(ctx, start, end)
	}
}

// BenchmarkFetchUsage measures usage fetching performance.
func BenchmarkFetchUsage(b *testing.B) {
	p := New("enterprise")
	ctx := context.Background()
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)

	records, _ := p.FetchCosts(ctx, start, end)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.FetchUsage(ctx, records, start, end)
	}
}

// BenchmarkDetection measures zombie detection performance.
func BenchmarkDetection(b *testing.B) {
	p := New("enterprise")
	ctx := context.Background()
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)

	records, _ := p.FetchCosts(ctx, start, end)
	usage, _ := p.FetchUsage(ctx, records, start, end)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = analyzer.Detect(records, usage, "test-account")
	}
}
