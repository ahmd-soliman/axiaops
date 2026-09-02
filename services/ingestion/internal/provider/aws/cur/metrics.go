package cur

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	queryDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "axiaops_cur_query_duration_seconds",
			Help:    "Duration of Athena CUR queries",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"query_type"},
	)

	bytesScannedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "axiaops_cur_bytes_scanned_total",
			Help: "Total bytes scanned by Athena CUR queries",
		},
		[]string{"query_type"},
	)

	queryErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "axiaops_cur_query_errors_total",
			Help: "Total number of failed Athena CUR queries",
		},
		[]string{"query_type"},
	)
)
