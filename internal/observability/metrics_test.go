package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIMetricsHandlerExposesRecordedValues(t *testing.T) {
	metrics := NewAPIMetrics()
	metrics.Requests.WithLabelValues(http.MethodGet, "/healthz", "200").Inc()
	metrics.RequestDuration.WithLabelValues(http.MethodGet, "/healthz").Observe(0.01)
	metrics.ResponseBytes.WithLabelValues(http.MethodGet, "/healthz", "200").Add(17)

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `stealth_api_http_requests_total{method="GET",route="/healthz",status="200"} 1`) {
		t.Fatalf("request counter was not exposed:\n%s", body)
	}
	if !strings.Contains(body, `stealth_api_http_response_bytes_total{method="GET",route="/healthz",status="200"} 17`) {
		t.Fatalf("response bytes counter was not exposed:\n%s", body)
	}
}

func TestWorkerMetricsHandlerExposesBoundedResultLabels(t *testing.T) {
	metrics := NewWorkerMetrics()
	metrics.JobsClaimed.Inc()
	metrics.JobsCompleted.WithLabelValues("succeeded").Inc()
	metrics.JobDuration.WithLabelValues("finished").Observe(0.1)
	metrics.Errors.WithLabelValues("claim").Inc()
	metrics.AgentJobsClaimed.Inc()
	metrics.AgentJobsCompleted.WithLabelValues("completed").Inc()
	metrics.AgentErrors.WithLabelValues("transition").Inc()

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `stealth_functions_worker_jobs_completed_total{result="succeeded"} 1`) {
		t.Fatalf("worker metrics were not exposed (status=%d):\n%s", recorder.Code, body)
	}
	if !strings.Contains(body, `stealth_functions_worker_errors_total{operation="claim"} 1`) {
		t.Fatalf("worker error counter was not exposed:\n%s", body)
	}
	if !strings.Contains(body, `stealth_agent_worker_jobs_completed_total{result="completed"} 1`) || !strings.Contains(body, `stealth_agent_worker_errors_total{operation="transition"} 1`) {
		t.Fatalf("Agent worker metrics were not exposed:\n%s", body)
	}
}
