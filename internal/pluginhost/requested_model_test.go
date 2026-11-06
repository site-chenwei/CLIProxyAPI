package pluginhost

import (
	"context"
	"fmt"
	"strings"
	"testing"

	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestResolveRequestedModel(t *testing.T) {
	tests := []struct {
		name            string
		originalRequest []byte
		payload         []byte
		fallback        string
		want            string
	}{
		{
			name:            "original request wins",
			originalRequest: []byte(`{"model":"client-alias"}`),
			payload:         []byte(`{"model":"translated-model"}`),
			fallback:        "fallback-model",
			want:            "client-alias",
		},
		{
			name:    "payload is the original when unset",
			payload: []byte(`{"model":"payload-model"}`),
			want:    "payload-model",
		},
		{
			name:            "fallback when original has no model",
			originalRequest: []byte(`{}`),
			payload:         []byte(`{"model":"translated-model"}`),
			fallback:        "fallback-model",
			want:            "fallback-model",
		},
		{
			name:            "fallback when model is non-string",
			originalRequest: []byte(`{"model":42}`),
			fallback:        "fallback-model",
			want:            "fallback-model",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveRequestedModel(test.originalRequest, test.payload, test.fallback); got != test.want {
				t.Fatalf("resolveRequestedModel() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResponseNormalizerUsesCachedRequestedModel(t *testing.T) {
	normalizer := responseNormalizerFunc(func(_ context.Context, req pluginapi.ResponseTransformRequest) (pluginapi.PayloadResponse, error) {
		if req.RequestedModel != "cached-model" {
			t.Fatalf("requested model = %q, want cached-model", req.RequestedModel)
		}
		return pluginapi.PayloadResponse{Body: req.Body}, nil
	})
	host := newHostWithRecords(capabilityRecord{id: "normalizer", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{
		ResponseNormalizerOmitRequestBody: true, ResponseAfterTranslator: normalizer,
	}}})
	ctx := withRequestedModel(context.Background(), "cached-model")
	host.NormalizeResponseAfter(ctx, sdktranslator.FormatOpenAI, sdktranslator.FormatOpenAI, "upstream-model", []byte(`{"model":"original-model"}`), nil, []byte("fragment"), true)
}

func TestExecutorRequestNeedsClone(t *testing.T) {
	if executorRequestNeedsClone(rpcProviderExecutor{}) {
		t.Fatal("RPC executor should not need request copies")
	}
	if !executorRequestNeedsClone(&fakeExecutor{}) {
		t.Fatal("in-process executor must keep request copies")
	}
}

func TestBuildExecutorRequestClonePolicy(t *testing.T) {
	host := New()
	original := []byte(`{"model":"m"}`)
	payload := []byte(`{"model":"m","input":"x"}`)
	req := coreexecutor.Request{Model: "m", Payload: payload}
	opts := coreexecutor.Options{OriginalRequest: original}

	cloned := buildExecutorRequestWithPolicy(host, "", nil, req, opts, true)
	if &cloned.OriginalRequest[0] == &original[0] || &cloned.Payload[0] == &payload[0] {
		t.Fatal("clone policy did not isolate request slices")
	}

	shared := buildExecutorRequestWithPolicy(host, "", nil, req, opts, false)
	if &shared.OriginalRequest[0] != &original[0] || &shared.Payload[0] != &payload[0] {
		t.Fatal("shared policy unexpectedly copied request slices")
	}
}

func BenchmarkResponseNormalizerRequestedModelLookup(b *testing.B) {
	original := []byte(`{"input":"` + strings.Repeat("x", 1024*1024) + `","model":"gpt-native"}`)
	normalizer := responseNormalizerFunc(func(_ context.Context, req pluginapi.ResponseTransformRequest) (pluginapi.PayloadResponse, error) {
		return pluginapi.PayloadResponse{Body: req.Body}, nil
	})
	host := newHostWithRecords(capabilityRecord{id: "normalizer", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{
		ResponseNormalizerOmitRequestBody: true, ResponseAfterTranslator: normalizer,
	}}})
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%t", cached), func(b *testing.B) {
			ctx := context.Background()
			if cached {
				ctx = withRequestedModel(ctx, "gpt-native")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				host.NormalizeResponseAfter(ctx, sdktranslator.FormatOpenAI, sdktranslator.FormatOpenAI, "gpt-native", original, original, []byte("fragment"), true)
			}
		})
	}
}
