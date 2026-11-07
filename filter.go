package privacy

import (
	"context"
	"strings"

	"aerosync/telemetry-pipeline/pipeline"
	"aerosync/telemetry-pipeline/telemetry"
)

// Filter removes media and payload-bearing fields. It is designed to be the
// first stage after a vendor adapter and must run before the ingress buffer.
type Filter struct{}

var _ pipeline.Stage = (*Filter)(nil)

func NewFilter() *Filter { return &Filter{} }

func (f *Filter) Name() string { return "privacy-filter" }

func (f *Filter) Process(_ context.Context, record telemetry.RawRecord) (telemetry.RawRecord, error) {
	if record == nil {
		return nil, nil
	}
	cleaned := make(telemetry.RawRecord, len(record))
	for key, value := range record {
		if isPrivateKey(key) {
			continue
		}
		cleaned[key] = cleanNested(value)
	}
	return cleaned, nil
}

func cleanNested(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			if !isPrivateKey(key) {
				out[key] = cleanNested(child)
			}
		}
		return out
	case telemetry.RawRecord:
		out := make(telemetry.RawRecord, len(v))
		for key, child := range v {
			if !isPrivateKey(key) {
				out[key] = cleanNested(child)
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, child := range v {
			out = append(out, cleanNested(child))
		}
		return out
	default:
		return value
	}
}

func isPrivateKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(key))
	parts := strings.Split(normalized, "_")
	for _, part := range parts {
		switch part {
		case "image", "images", "imagery", "photo", "photos", "video", "videos", "payload", "payloads":
			return true
		}
	}
	// Catch compound keys such as cameraImageData or videoStream.
	compact := strings.ReplaceAll(normalized, "_", "")
	for _, token := range []string{"image", "imagery", "photo", "video", "payload"} {
		if strings.Contains(compact, token) {
			return true
		}
	}
	return false
}
