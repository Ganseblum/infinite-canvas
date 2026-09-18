package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

// geminiVideoPayload 按 predictLongRunning 请求体的实际形状解码。
type geminiVideoPayload struct {
	Instances  []map[string]any `json:"instances"`
	Parameters map[string]any   `json:"parameters"`
}

// TestGeminiCreateVideoRequestPayload 录制 predictLongRunning 实际发出的 JSON：
// 首尾帧 / 参考图模式的 instance 字段映射与 parameters 归一逐值断言，
// 正是能抓住「参考图漏传」「时长越界」这类缺陷的层次。
func TestGeminiCreateVideoRequestPayload(t *testing.T) {
	respondOperation := staticResponder(http.StatusOK, "application/json", []byte(`{"name":"models/veo-3/operations/op_1"}`))
	// decodeInstance 发起一次 CreateVideo 并解出 instance 与 parameters。
	decodeInstance := func(t *testing.T, req VideoRequest) (map[string]any, map[string]any, VideoTask) {
		t.Helper()
		server, captured := captureServer(t, respondOperation)
		task, err := NewGemini(server.URL, "test-key", server.Client()).CreateVideo(context.Background(), req)
		if err != nil {
			t.Fatalf("CreateVideo 返回错误: %v", err)
		}
		requests := captured()
		if len(requests) != 1 {
			t.Fatalf("应只发出 1 次请求, got %d", len(requests))
		}
		if requests[0].Path != "/v1beta/models/"+req.Model+":predictLongRunning" {
			t.Fatalf("路径 = %q, want /v1beta/models/%s:predictLongRunning", requests[0].Path, req.Model)
		}
		if requests[0].Header.Get("x-goog-api-key") != "test-key" {
			t.Fatalf("x-goog-api-key = %q, want test-key", requests[0].Header.Get("x-goog-api-key"))
		}
		var payload geminiVideoPayload
		if err := json.Unmarshal(requests[0].Body, &payload); err != nil {
			t.Fatalf("请求体不是合法 JSON: %v", err)
		}
		if len(payload.Instances) != 1 {
			t.Fatalf("应只有 1 个 instance, got %d", len(payload.Instances))
		}
		return payload.Instances[0], payload.Parameters, task
	}
	// assertInline 断言 instance 里的内联图片字段映射为 bytesBase64Encoded + mimeType。
	assertInline := func(t *testing.T, instance map[string]any, field, wantData, wantMime string) {
		t.Helper()
		media, ok := instance[field].(map[string]any)
		if !ok {
			t.Fatalf("instance 缺少字段 %s", field)
		}
		decoded, err := base64.StdEncoding.DecodeString(media["bytesBase64Encoded"].(string))
		if err != nil || string(decoded) != wantData {
			t.Fatalf("字段 %s 的 base64 内容不符: got %q err=%v, want %q", field, decoded, err, wantData)
		}
		if media["mimeType"] != wantMime {
			t.Fatalf("字段 %s 的 mimeType = %v, want %q", field, media["mimeType"], wantMime)
		}
	}

	t.Run("首尾帧模式映射 image 与 lastFrame", func(t *testing.T) {
		instance, parameters, task := decodeInstance(t, VideoRequest{
			Model: "veo-3.0-generate-001", Prompt: "海边日落延时", Duration: 999,
			Ratio: "9:16", Resolution: "low", GenerateAudio: true, Watermark: true,
			References: []InlineMedia{
				{Data: []byte("first-frame"), MimeType: "image/png"},
				{Data: []byte("last-frame"), MimeType: "image/png"},
			},
		})
		if instance["prompt"] != "海边日落延时" {
			t.Fatalf("prompt = %v, want 海边日落延时", instance["prompt"])
		}
		assertInline(t, instance, "image", "first-frame", "image/png")
		assertInline(t, instance, "lastFrame", "last-frame", "image/png")
		if _, ok := instance["referenceImages"]; ok {
			t.Fatalf("首尾帧模式不应下发 referenceImages")
		}
		wantParameters := map[string]any{
			"aspectRatio":     "9:16",
			"durationSeconds": float64(30), // 999 裁剪到上界
			"resolution":      "480p",
			"generateAudio":   true,
			"addWatermark":    true,
		}
		if !reflect.DeepEqual(parameters, wantParameters) {
			t.Fatalf("parameters 不符:\n got  %#v\n want %#v", parameters, wantParameters)
		}
		if task.Provider != "gemini" || task.UpstreamTaskID != "models/veo-3/operations/op_1" || task.PollAfterMs != 10000 {
			t.Fatalf("task = %+v, want gemini/models/veo-3/operations/op_1/10000", task)
		}
	})

	t.Run("参考图模式映射 referenceImages 且时长裁到下界", func(t *testing.T) {
		instance, parameters, _ := decodeInstance(t, VideoRequest{
			Model: "veo-3.0-generate-001", Prompt: "按参考图生成", Duration: 2, Mode: "reference",
			References: []InlineMedia{
				{Data: []byte("ref-a"), MimeType: "image/png"},
				{Data: []byte("ref-b"), MimeType: "image/jpeg"},
			},
		})
		// 与原版一致：参考图模式只发 referenceImages，不发首尾帧字段。
		for _, field := range []string{"image", "lastFrame"} {
			if _, ok := instance[field]; ok {
				t.Fatalf("参考图模式不应下发 %s", field)
			}
		}
		references, ok := instance["referenceImages"].([]any)
		if !ok || len(references) != 2 {
			t.Fatalf("referenceImages 应为 2 项, got %v", instance["referenceImages"])
		}
		wantData := []string{"ref-a", "ref-b"}
		wantMime := []string{"image/png", "image/jpeg"}
		for index, item := range references {
			entry, ok := item.(map[string]any)
			if !ok || entry["referenceType"] != "asset" {
				t.Fatalf("referenceImages[%d] 缺少 referenceType=asset: %v", index, item)
			}
			media, _ := entry["image"].(map[string]any)
			if media == nil {
				t.Fatalf("referenceImages[%d] 的 image 映射缺失: %v", index, item)
			}
			decoded, err := base64.StdEncoding.DecodeString(media["bytesBase64Encoded"].(string))
			if err != nil || string(decoded) != wantData[index] {
				t.Fatalf("referenceImages[%d] 的 base64 内容不符: got %q err=%v, want %q", index, decoded, err, wantData[index])
			}
			if media["mimeType"] != wantMime[index] {
				t.Fatalf("referenceImages[%d] 的 mimeType = %v, want %q", index, media["mimeType"], wantMime[index])
			}
		}
		if parameters["durationSeconds"] != float64(4) {
			t.Fatalf("durationSeconds = %v, want 4（下界裁剪）", parameters["durationSeconds"])
		}
	})

	t.Run("缺省比例与分辨率按原版兜底", func(t *testing.T) {
		instance, parameters, _ := decodeInstance(t, VideoRequest{
			Model: "veo-3.0-generate-001", Prompt: "纯文本生视频", Duration: 8,
		})
		for _, field := range []string{"image", "lastFrame", "referenceImages"} {
			if _, ok := instance[field]; ok {
				t.Fatalf("无参考图时不应下发 %s", field)
			}
		}
		if parameters["aspectRatio"] != "16:9" || parameters["resolution"] != "720p" {
			t.Fatalf("缺省兜底不符: %v", parameters)
		}
		if parameters["durationSeconds"] != float64(8) {
			t.Fatalf("durationSeconds = %v, want 8", parameters["durationSeconds"])
		}
	})
}
