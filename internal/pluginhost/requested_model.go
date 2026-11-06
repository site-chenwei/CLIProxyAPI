package pluginhost

import (
	"context"

	"github.com/MoeForge/moego/ojson"
)

// requestedModelContextKey carries the client-requested model through response
// translation so streaming normalizers do not need to rescan the original
// request payload on every chunk.
type requestedModelContextKey struct{}

func withRequestedModel(ctx context.Context, requestedModel string) context.Context {
	if requestedModel == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestedModelContextKey{}, requestedModel)
}

func requestedModelFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestedModel, _ := ctx.Value(requestedModelContextKey{}).(string)
	return requestedModel
}

func requestedModelFromJSON(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	requested := ojson.GetBytes(raw, "model")
	if requested.Type != ojson.String || requested.Str == "" {
		return ""
	}
	return requested.Str
}

func resolveRequestedModel(originalRequest, payload []byte, fallback string) string {
	source := originalRequest
	if len(source) == 0 {
		source = payload
	}
	if requestedModel := requestedModelFromJSON(source); requestedModel != "" {
		return requestedModel
	}
	return fallback
}
