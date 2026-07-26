package metrics_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zalando/skipper/metrics"
)

func TestHandlerPrometheusBadRequests(t *testing.T) {
	o := metrics.Options{
		Format:               metrics.PrometheusKind,
		EnableRuntimeMetrics: true,
	}
	mh := metrics.NewDefaultHandler(o)

	r, _ := http.NewRequest("GET", "/", nil)
	rw := httptest.NewRecorder()

	mh.ServeHTTP(rw, r)
	if rw.Code != http.StatusNotFound {
		t.Error("The root resource should not provide a valid response")
	}
}

func TestHandlerPrometheusMetricsRequest(t *testing.T) {
	o := metrics.Options{
		Format:               metrics.PrometheusKind,
		EnableRuntimeMetrics: true,
	}
	mh := metrics.NewDefaultHandler(o)

	r, _ := http.NewRequest("GET", "/metrics", nil)
	rw := httptest.NewRecorder()

	mh.ServeHTTP(rw, r)
	if rw.Code != http.StatusOK {
		t.Error("Metrics endpoint should provide a valid response")
	}
	b := rw.Body.Bytes()
	if len(b) == 0 {
		t.Error("Metrics endpoint should've returned some runtime metrics in it")
	}
}

func TestHandlerCodaHaleBadRequests(t *testing.T) {
	o := metrics.Options{
		Format:               metrics.CodaHaleKind,
		EnableRuntimeMetrics: true,
	}
	m := metrics.NewMetrics(o)
	defer m.Close()

	mh := metrics.NewHandler(o, m)

	r1, _ := http.NewRequest("GET", "/", nil)
	rw1 := httptest.NewRecorder()

	mh.ServeHTTP(rw1, r1)
	if rw1.Code != http.StatusNotFound {
		t.Error("The root resource should not provide a valid response")
	}

	r2, _ := http.NewRequest("POST", "/metrics", nil)
	rw2 := httptest.NewRecorder()
	mh.ServeHTTP(rw2, r2)
	if rw2.Code != http.StatusMethodNotAllowed {
		t.Error("POST method should not provide a valid response")
	}
}

func TestHandlerCodaHaleEmptyMetricsRequest(t *testing.T) {
	o := metrics.Options{Format: metrics.CodaHaleKind}
	m := metrics.NewCodaHale(o)
	defer m.Close()

	mh := metrics.NewHandler(o, m)
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rw := httptest.NewRecorder()
	mh.ServeHTTP(rw, r)

	if rw.Code != http.StatusNotFound {
		t.Fatalf("empty Coda Hale registry returned %d, want %d", rw.Code, http.StatusNotFound)
	}
}

func TestHandlerCodaHaleAllMetricsRequest(t *testing.T) {
	o := metrics.Options{
		Format:               metrics.CodaHaleKind,
		EnableRuntimeMetrics: true,
	}
	m := metrics.NewCodaHale(o)
	defer m.Close()

	mh := metrics.NewHandler(o, m)
	m.IncCounter("TestHandlerCodaHaleAllMetricsRequest")

	r, _ := http.NewRequest("GET", "/metrics", nil)
	rw := httptest.NewRecorder()
	mh.ServeHTTP(rw, r)

	if rw.Code != http.StatusOK {
		t.Fatalf("Metrics endpoint should provide a valid response, got: %d", rw.Code)
	}

	var data map[string]map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &data); err != nil {
		t.Fatalf("Unable to unmarshal metrics response: %v", err)
	}

	if _, ok := data["counters"]["TestHandlerCodaHaleAllMetricsRequest"]; !ok {
		t.Error("Metrics endpoint should've returned some runtime metrics in it")
	}
}

func TestHandlerCodaHaleSingleMetricsRequest(t *testing.T) {
	o := metrics.Options{
		Format:               metrics.CodaHaleKind,
		EnableRuntimeMetrics: true,
	}
	m := metrics.NewCodaHale(o)
	defer m.Close()

	mh := metrics.NewHandler(o, m)
	m.IncCounter("TestHandlerCodaHaleSingleMetricsRequest")

	r, _ := http.NewRequest("GET", "/metrics/TestHandlerCodaHaleSingleMetricsRequest", nil)
	rw := httptest.NewRecorder()
	mh.ServeHTTP(rw, r)
	if rw.Code != http.StatusOK {
		t.Error("Metrics endpoint should provide a valid response")
	}

	var data map[string]map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &data); err != nil {
		t.Error("Unable to unmarshal metrics response")
	}

	if len(data) != 1 {
		t.Error("Metrics endpoint for exact match should've returned exactly the requested item")
	}

	if _, ok := data["counters"]["TestHandlerCodaHaleSingleMetricsRequest"]; !ok {
		t.Error("Metrics endpoint should've returned some runtime metrics in it")
	}
}

func TestHandlerCodaHaleSingleMetricsRequestWhenUsingPrefix(t *testing.T) {
	o := metrics.Options{
		Format:               metrics.CodaHaleKind,
		Prefix:               "zmon.",
		EnableRuntimeMetrics: true,
	}
	m := metrics.NewCodaHale(o)
	defer m.Close()

	mh := metrics.NewHandler(o, m)
	m.IncCounter("TestHandlerCodaHaleSingleMetricsRequestWhenUsingPrefix")

	r, _ := http.NewRequest("GET", "/metrics/zmon.TestHandlerCodaHaleSingleMetricsRequestWhenUsingPrefix", nil)
	rw := httptest.NewRecorder()
	mh.ServeHTTP(rw, r)
	if rw.Code != http.StatusOK {
		t.Error("Metrics endpoint should provide a valid response for exact match using prefix")
	}

	var data map[string]map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &data); err != nil {
		t.Error("Unable to unmarshal metrics response for exact match using prefix")
	}

	if len(data) != 1 {
		t.Error("Metrics endpoint for exact match using prefix should've returned exactly the requested item")
	}

	if _, ok := data["counters"]["zmon.TestHandlerCodaHaleSingleMetricsRequestWhenUsingPrefix"]; !ok {
		t.Error("Metrics endpoint for exact match using prefix should've returned some runtime metrics in it")
	}
}

func TestHandlerCodaHaleMetricsRequestWithPattern(t *testing.T) {
	o := metrics.Options{
		Format:               metrics.CodaHaleKind,
		EnableRuntimeMetrics: true,
	}
	m := metrics.NewCodaHale(o)
	defer m.Close()

	mh := metrics.NewHandler(o, m)
	m.UpdateGauge("runtime.Num", 5.0)

	r, _ := http.NewRequest("GET", "/metrics/runtime.Num", nil)
	rw := httptest.NewRecorder()
	mh.ServeHTTP(rw, r)
	if rw.Code != http.StatusOK {
		t.Error("Metrics endpoint should provide a valid response")
	}

	var data map[string]map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &data); err != nil {
		t.Error("Unable to unmarshal metrics response")
	}

	if len(data) < 1 {
		t.Error("Metrics endpoint for prefix should've returned some runtime metrics in it")
	}

	for k, v := range data {
		if k != "gauges" {
			t.Error("Metrics should report `gauges` metrics")
		} else {
			for k2 := range v {
				if !strings.HasPrefix(k2, "runtime.Num") {
					t.Error("Metrics endpoint returned metrics with the wrong prefix")
				}
			}
		}
	}
}

func TestHandlerCodaHaleUnknownMetricRequest(t *testing.T) {
	o := metrics.Options{
		Format:               metrics.CodaHaleKind,
		EnableRuntimeMetrics: true,
	}
	m := metrics.NewCodaHale(o)
	defer m.Close()

	mh := metrics.NewHandler(o, m)

	r, _ := http.NewRequest("GET", "/metrics/DOES-NOT-EXIST", nil)
	rw := httptest.NewRecorder()

	mh.ServeHTTP(rw, r)
	if rw.Code != http.StatusNotFound {
		t.Error("Request for unknown metrics should return a Not Found status")
	}
}

func TestHandlerPrometheusMetricsSuffixRequest(t *testing.T) {
	for _, format := range []metrics.Kind{metrics.PrometheusKind, metrics.CodaHaleKind | metrics.PrometheusKind} {
		t.Run("format", func(t *testing.T) {
			o := metrics.Options{Format: format}
			m := metrics.NewMetrics(o)
			defer m.Close()
			keys := []string{
				"TestHandlerPrometheusMetricsSuffixRequestFirst",
				"TestHandlerPrometheusMetricsSuffixRequestSecond",
			}
			for _, key := range keys {
				m.IncCounter(key)
			}
			m.MeasureSince("TestHandlerPrometheusMetricsSuffixRequestDuration", time.Now())
			m.UpdateGauge("TestHandlerPrometheusMetricsSuffixRequestGauge", 1)

			mh := metrics.NewHandler(o, m)
			bodies := make(map[string]string)
			for _, path := range []string{"/metrics", "/metrics/any-suffix"} {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				rw := httptest.NewRecorder()
				mh.ServeHTTP(rw, r)

				if rw.Code != http.StatusOK {
					t.Fatalf("%s returned %d, want %d", path, rw.Code, http.StatusOK)
				}
				bodies[path] = rw.Body.String()
				for _, key := range keys {
					series := "skipper_custom_total{key=\"" + key + "\"}"
					if !strings.Contains(bodies[path], series) {
						t.Fatalf("%s did not return %q from the Prometheus registry", path, series)
					}
				}
				for _, series := range []string{
					"skipper_custom_duration_seconds_count{key=\"TestHandlerPrometheusMetricsSuffixRequestDuration\"} 1",
					"skipper_custom_gauges{key=\"TestHandlerPrometheusMetricsSuffixRequestGauge\"} 1",
				} {
					if !strings.Contains(bodies[path], series) {
						t.Fatalf("%s did not return %q from the Prometheus registry", path, series)
					}
				}
			}
			if bodies["/metrics/any-suffix"] != bodies["/metrics"] {
				t.Fatal("Prometheus suffix response differs from the complete metrics registry")
			}
		})
	}
}

func TestHandlerCombinedMetricsContentNegotiation(t *testing.T) {
	o := metrics.Options{Format: metrics.CodaHaleKind | metrics.PrometheusKind}
	m := metrics.NewMetrics(o)
	defer m.Close()
	m.IncCounter("TestHandlerCombinedMetricsContentNegotiation")

	mh := metrics.NewHandler(o, m)
	for _, test := range []struct {
		name        string
		path        string
		accept      string
		contentType string
		body        string
	}{
		{name: "Prometheus by default", path: "/metrics/any-suffix", contentType: "text/plain", body: "skipper_custom_total"},
		{name: "Coda Hale by Accept header", path: "/metrics/TestHandlerCombinedMetricsContentNegotiation", accept: "application/codahale+json", contentType: "application/json", body: "TestHandlerCombinedMetricsContentNegotiation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.accept != "" {
				r.Header.Set("Accept", test.accept)
			}
			rw := httptest.NewRecorder()
			mh.ServeHTTP(rw, r)

			if rw.Code != http.StatusOK {
				t.Fatalf("metrics handler returned %d, want %d", rw.Code, http.StatusOK)
			}
			if !strings.HasPrefix(rw.Header().Get("Content-Type"), test.contentType) {
				t.Fatalf("content type %q, want prefix %q", rw.Header().Get("Content-Type"), test.contentType)
			}
			if !strings.Contains(rw.Body.String(), test.body) {
				t.Fatalf("response does not contain %q", test.body)
			}
		})
	}
}

func BenchmarkMeasureSincePrometheus(b *testing.B) {
	m := metrics.NewMetrics(metrics.Options{Format: metrics.PrometheusKind})
	benchmarkMeasureSince(b, m)
}

func BenchmarkMeasureSinceCodaHale(b *testing.B) {
	m := metrics.NewMetrics(metrics.Options{Format: metrics.CodaHaleKind})
	benchmarkMeasureSince(b, m)
}

func BenchmarkIncCounterPrometheus(b *testing.B) {
	m := metrics.NewMetrics(metrics.Options{Format: metrics.PrometheusKind})
	benchmarkIncCounter(b, m)
}

func BenchmarkIncCounterCodaHale(b *testing.B) {
	m := metrics.NewMetrics(metrics.Options{Format: metrics.CodaHaleKind})
	benchmarkIncCounter(b, m)
}

func benchmarkMeasureSince(b *testing.B, m metrics.Metrics) {
	start := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.MeasureSince("MeasureSince", start)
	}
}

func benchmarkIncCounter(b *testing.B, m metrics.Metrics) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.IncCounter("IncCounter")
	}
}
