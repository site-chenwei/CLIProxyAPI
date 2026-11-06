package helps

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
)

// TestImmutableRequestBodyRetainsWithinBudget verifies that RecordImmutableRequestBody
// retains a full body without copying and charges its capacity to the deferred budget,
// while still truncating when the body does not fit.
func TestImmutableRequestBodyRetainsWithinBudget(t *testing.T) {
	const max = logging.MaxDeferredAPIRequestBodyBytes
	noopBuild := func(int, time.Time, []byte, bool, bool) logging.DeferredAPIRequest {
		return func() []byte { return nil }
	}
	for _, tt := range []struct {
		name          string
		length        int
		capacity      int
		prefill       int
		wantCharge    int
		wantTruncated bool
	}{
		{name: "complete body", length: 8, capacity: 8, wantCharge: 8},
		{name: "spare capacity charged", length: 8, capacity: 12, wantCharge: 12},
		{name: "spare capacity exceeds remaining", length: 8, capacity: 32, prefill: max - 16, wantCharge: 8},
		{name: "truncated body", length: 8, capacity: 8, prefill: max - 4, wantCharge: 4, wantTruncated: true},
		{name: "exhausted budget", length: 8, capacity: 8, prefill: max, wantCharge: 0, wantTruncated: true},
		{name: "empty body with spare capacity", capacity: 32, wantCharge: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			capture := logging.NewDeferredAPICapture()
			if tt.prefill > 0 {
				capture.RecordRequest(make([]byte, tt.prefill), noopBuild)
			}
			body := make([]byte, tt.length, tt.capacity)
			copy(body, "abcdefgh")
			fn, used, ok := capture.RecordImmutableRequestBody(body, func(index int, capturedAt time.Time, capturedBody []byte, bodyEmpty, bodyTruncated bool) logging.DeferredAPIRequest {
				return buildDeferredAPIRequestCallback(UpstreamRequestLog{URL: "https://example.test/responses", Method: http.MethodPost}, index, capturedAt, capturedBody, bodyEmpty, bodyTruncated)
			})
			if !ok {
				t.Fatal("RecordImmutableRequestBody returned ok=false")
			}
			if gotCharge := used - tt.prefill; gotCharge != tt.wantCharge {
				t.Errorf("charged bytes=%d, want %d", gotCharge, tt.wantCharge)
			}
			result := string(fn())
			if strings.Contains(result, "TRUNCATED") != tt.wantTruncated {
				t.Errorf("truncation mismatch, want %t: %s", tt.wantTruncated, result)
			}
			remaining := max - tt.prefill
			if remaining < 0 {
				remaining = 0
			}
			wantBody := string(body[:min(tt.length, remaining)])
			if tt.length == 0 {
				if !strings.Contains(result, "\nBody:\n<empty>") {
					t.Errorf("missing <empty> body: %s", result)
				}
			} else if wantBody != "" && !strings.Contains(result, "\nBody:\n"+wantBody) {
				t.Errorf("missing captured body %q: %s", wantBody, result)
			}
		})
	}
}

// TestRecordAPIRequestImmutableBodyChargesCapacity exercises the full helps path and
// confirms the retained capacity (not just the length) is charged to the budget key.
func TestRecordAPIRequestImmutableBodyChargesCapacity(t *testing.T) {
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx := context.WithValue(t.Context(), "gin", ginCtx)
	body := make([]byte, 8, 12)
	copy(body, "abcdefgh")
	RecordAPIRequestImmutableBody(ctx, &config.Config{}, UpstreamRequestLog{URL: "https://example.test/responses", Method: http.MethodPost, Body: body})
	got, _ := ginCtx.Get(deferredAPIRequestBytesKey)
	if got != 12 {
		t.Errorf("charged bytes=%v, want 12 (retained capacity)", got)
	}
	raw, exists := ginCtx.Get(logging.DeferredAPIResponseContextKey)
	if !exists {
		t.Fatal("missing deferred capture")
	}
	capture, ok := raw.(*logging.DeferredAPICapture)
	if !ok || capture == nil {
		t.Fatalf("unexpected capture type %T", raw)
	}
	if out := string(capture.RenderAPIRequests()); !strings.Contains(out, "abcdefgh") {
		t.Errorf("capture missing body: %s", out)
	}
}

func TestImmutableRequestLogMatchesSnapshotOutput(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			cfg := &config.Config{SDKConfig: config.SDKConfig{RequestLog: enabled}}
			info := UpstreamRequestLog{URL: "https://example.test/responses", Method: http.MethodPost, Headers: http.Header{"X-Debug": {"one", "two"}}, Body: []byte(`{"model":"test","input":"hello"}`)}
			var results []string
			for _, record := range []func(context.Context, *config.Config, UpstreamRequestLog){RecordAPIRequest, RecordAPIRequestImmutableBody} {
				ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
				record(context.WithValue(t.Context(), "gin", ginCtx), cfg, info)
				var result string
				if enabled {
					result = string(ginCtx.MustGet(apiRequestKey).([]byte))
				} else {
					result = string(ginCtx.MustGet(logging.DeferredAPIRequestContextKey).([]logging.DeferredAPIRequest)[0]())
				}
				var lines []string
				for _, line := range strings.Split(result, "\n") {
					if !strings.HasPrefix(line, "Timestamp:") {
						lines = append(lines, line)
					}
				}
				results = append(results, strings.Join(lines, "\n"))
			}
			if results[0] != results[1] {
				t.Fatalf("immutable log differs:\n%s\n%s", results[0], results[1])
			}
		})
	}
}

func BenchmarkDeferredAPIRequestBody(b *testing.B) {
	gin.SetMode(gin.TestMode)
	for _, size := range []int{1024, 1 << 20, 8 << 20} {
		for _, immutable := range []bool{false, true} {
			b.Run(fmt.Sprintf("bytes=%d/immutable=%t", size, immutable), func(b *testing.B) {
				ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx := context.WithValue(b.Context(), "gin", ginCtx)
				cfg := &config.Config{}
				info := UpstreamRequestLog{Body: bytes.Repeat([]byte("x"), size)}
				record := RecordAPIRequest
				if immutable {
					record = RecordAPIRequestImmutableBody
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					ginCtx.Keys = nil
					record(ctx, cfg, info)
				}
			})
		}
	}
}
