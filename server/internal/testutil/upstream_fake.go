package testutil

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeOpenAIUpstream 模拟 OpenAI 兼容上游：生图返回 b64，视频创建与查询返回状态。
func FakeOpenAIUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/images/generations", func(w http.ResponseWriter, r *http.Request) {
		payload := base64.StdEncoding.EncodeToString([]byte("fake-png-data"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": payload}},
		})
	})
	mux.HandleFunc("/v1/videos", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "upstream-video-1"})
	})
	mux.HandleFunc("/v1/videos/upstream-video-1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "url": "http://127.0.0.1:1/result.mp4"})
	})
	mux.HandleFunc("/v1/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("fake-mp3"))
	})
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, chunk := range []string{"你好", "，世界"} {
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: " + `{"type":"response.output_text.delta","delta":"` + chunk + `"}` + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}
