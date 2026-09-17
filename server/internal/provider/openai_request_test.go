package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// capturedRequest 记录上游实际收到的一次请求。
type capturedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// captureServer 起一个把请求原样录下来的 httptest 服务，响应由 responder 决定。
func captureServer(t *testing.T, responder func(w http.ResponseWriter, r *http.Request, body []byte)) (*httptest.Server, func() []capturedRequest) {
	t.Helper()
	mu := &sync.Mutex{}
	var captured []capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		captured = append(captured, capturedRequest{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
		mu.Unlock()
		responder(w, r, body)
	}))
	t.Cleanup(server.Close)
	return server, func() []capturedRequest {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

// staticResponder 返回固定状态码与响应体的 responder。
func staticResponder(status int, contentType string, body []byte) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

// fakeImageResponse 生成一张可被 parseOpenAIImages 接受的响应，避免测试被解析失败干扰。
func fakeImageResponse() []byte {
	encoded := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	return []byte(`{"data":[{"b64_json":"` + encoded + `"}]}`)
}

// logJSONBody 把实际发出的 JSON 请求体逐字段打出来，供与常量表逐值核对。
func logJSONBody(t *testing.T, body []byte) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Logf("请求体不是 JSON: %q", body)
		return
	}
	keys := make([]string, 0, len(decoded))
	for key := range decoded {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		t.Logf("  %s = %v", key, decoded[key])
	}
}

// logMultipartForm 把实际发出的 multipart 表单逐字段、逐文件打出来。
func logMultipartForm(t *testing.T, form *multipart.Form) {
	t.Helper()
	fields := make([]string, 0, len(form.Value))
	for field := range form.Value {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		t.Logf("  字段 %s = %v", field, form.Value[field])
	}
	names := make([]string, 0, len(form.File))
	for name := range form.File {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, file := range form.File[name] {
			t.Logf("  文件 %s filename=%q content-type=%q", name, file.Filename, file.Header.Get("Content-Type"))
		}
	}
}

// parseMultipartBody 按 Content-Type 里的 boundary 解析录到的 multipart 请求体。
func parseMultipartBody(t *testing.T, request capturedRequest) *multipart.Form {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("解析 Content-Type 失败: %v", err)
	}
	if mediaType != "multipart/form-data" {
		t.Fatalf("Content-Type = %q, want multipart/form-data", mediaType)
	}
	form, err := multipart.NewReader(bytes.NewReader(request.Body), params["boundary"]).ReadForm(8 << 20)
	if err != nil {
		t.Fatalf("解析 multipart 请求体失败: %v", err)
	}
	return form
}

// assertMultipartFields 逐字段断言 multipart 表单值。
func assertMultipartFields(t *testing.T, form *multipart.Form, want map[string]string) {
	t.Helper()
	for field, wantValue := range want {
		got, ok := form.Value[field]
		if !ok {
			t.Fatalf("缺少表单字段 %s", field)
		}
		if len(got) != 1 || got[0] != wantValue {
			t.Fatalf("字段 %s = %v, want [%q]", field, got, wantValue)
		}
	}
}

// assertMultipartAbsent 断言这些字段没有下发。
func assertMultipartAbsent(t *testing.T, form *multipart.Form, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if _, ok := form.Value[field]; ok {
			t.Fatalf("字段 %s 不应下发", field)
		}
		if _, ok := form.File[field]; ok {
			t.Fatalf("文件字段 %s 不应下发", field)
		}
	}
}

// assertFilePart 断言 multipart 文件字段第 index 个 part 的文件名、媒体类型与内容。
func assertFilePart(t *testing.T, form *multipart.Form, field string, index int, filename, contentType string, data []byte) {
	t.Helper()
	files, ok := form.File[field]
	if !ok {
		t.Fatalf("缺少文件字段 %s", field)
	}
	if index >= len(files) {
		t.Fatalf("文件字段 %s 只有 %d 个 part, 不存在下标 %d", field, len(files), index)
	}
	file := files[index]
	if file.Filename != filename {
		t.Fatalf("字段 %s[%d] 文件名 = %q, want %q", field, index, file.Filename, filename)
	}
	if got := file.Header.Get("Content-Type"); got != contentType {
		t.Fatalf("字段 %s[%d] 媒体类型 = %q, want %q", field, index, got, contentType)
	}
	opened, err := file.Open()
	if err != nil {
		t.Fatalf("读取文件字段 %s[%d] 失败: %v", field, index, err)
	}
	defer opened.Close()
	got, err := io.ReadAll(opened)
	if err != nil {
		t.Fatalf("读取文件字段 %s[%d] 失败: %v", field, index, err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("字段 %s[%d] 内容 = %q, want %q", field, index, got, data)
	}
}

// TestOpenAIImagesRequestPayload 录制 /images/generations 实际发出的 JSON。
// 期望值全部写字面量，防止尺寸换算、质量别名、output_format 等归一逻辑被静默改坏。
func TestOpenAIImagesRequestPayload(t *testing.T) {
	cases := []struct {
		name string
		req  ImageRequest
		want map[string]any
	}{
		{
			name: "gpt-image 带 4k 别名与 16:9 尺寸",
			req: ImageRequest{
				Model:      "gpt-image-1",
				Prompt:     "一只橘猫",
				N:          2,
				Size:       "16:9",
				Quality:    "4k",
				Background: " TRANSPARENT ",
			},
			want: map[string]any{
				"model":         "gpt-image-1",
				"prompt":        "一只橘猫",
				"n":             float64(2),
				"output_format": "png",
				"size":          "3840x2160",
				"quality":       "high",
				"background":    "transparent",
			},
		},
		{
			name: "dall-e 恒发 b64_json 且缺省字段不下发",
			req:  ImageRequest{Model: "dall-e-3", Prompt: "海报", N: 1, Quality: "2K"},
			want: map[string]any{
				"model":           "dall-e-3",
				"prompt":          "海报",
				"n":               float64(1),
				"output_format":   "png",
				"quality":         "medium",
				"response_format": "b64_json",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, captured := captureServer(t, staticResponder(http.StatusOK, "application/json", fakeImageResponse()))
			_, err := NewOpenAI(server.URL, "test-key", server.Client()).Images(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Images 返回错误: %v", err)
			}
			requests := captured()
			if len(requests) != 1 {
				t.Fatalf("应只发出 1 次请求, got %d", len(requests))
			}
			got := requests[0]
			if got.Path != "/v1/images/generations" {
				t.Fatalf("路径 = %q, want /v1/images/generations", got.Path)
			}
			if got.Header.Get("Authorization") != "Bearer test-key" {
				t.Fatalf("Authorization = %q, want Bearer test-key", got.Header.Get("Authorization"))
			}
			if got.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got.Header.Get("Content-Type"))
			}
			t.Logf("上游实际收到的 JSON（%s）:", tc.name)
			logJSONBody(t, got.Body)
			var decoded map[string]any
			if err := json.Unmarshal(got.Body, &decoded); err != nil {
				t.Fatalf("请求体不是合法 JSON: %v", err)
			}
			// 与期望整表比对：多字段、少字段、值不同都算失败。
			if !reflect.DeepEqual(decoded, tc.want) {
				t.Fatalf("请求体字段不符:\n got  %#v\n want %#v", decoded, tc.want)
			}
		})
	}
}

// TestOpenAIImagesEditsRequestPayload 录制 /images/edits 的 multipart 表单：
// 归一字段与 image[]/mask 的文件名、媒体类型逐项断言。
func TestOpenAIImagesEditsRequestPayload(t *testing.T) {
	refBytes := []byte("fake-ref-png")
	maskBytes := []byte("fake-mask-png")
	server, captured := captureServer(t, staticResponder(http.StatusOK, "application/json", fakeImageResponse()))
	_, err := NewOpenAI(server.URL, "test-key", server.Client()).Images(context.Background(), ImageRequest{
		Model:      "gpt-image-1",
		Prompt:     "改成夜景",
		N:          1,
		Size:       "1024x1024",
		Quality:    "HD",
		Background: "opaque",
		References: []InlineMedia{
			{Data: refBytes, MimeType: "image/png", Name: "ref-0.png"},
			{Data: refBytes, MimeType: "image/png", Name: "ref-1.png"},
		},
		Mask: &InlineMedia{Data: maskBytes, MimeType: "image/png", Name: "mask.png"},
	})
	if err != nil {
		t.Fatalf("Images 返回错误: %v", err)
	}
	requests := captured()
	if len(requests) != 1 {
		t.Fatalf("应只发出 1 次请求, got %d", len(requests))
	}
	got := requests[0]
	if got.Path != "/v1/images/edits" {
		t.Fatalf("路径 = %q, want /v1/images/edits", got.Path)
	}
	form := parseMultipartBody(t, got)
	t.Logf("上游实际收到的 multipart 表单（images/edits）:")
	logMultipartForm(t, form)
	assertMultipartFields(t, form, map[string]string{
		"model":         "gpt-image-1",
		"prompt":        "改成夜景",
		"n":             "1",
		"output_format": "png",
		"size":          "1024x1024",
		"quality":       "hd",
	})
	// opaque 不是 transparent，不下发；gpt-image 不带 response_format。
	assertMultipartAbsent(t, form, "background", "response_format")
	// 多张参考图使用重复的 image[] 字段，文件名 ref-0 / ref-1。
	files, ok := form.File["image[]"]
	if !ok || len(files) != 2 {
		t.Fatalf("应有 2 个 image[] 文件 part, got %v", form.File)
	}
	for index, file := range files {
		if file.Filename != fmt.Sprintf("ref-%d", index) {
			t.Fatalf("image[%d] 文件名 = %q, want ref-%d", index, file.Filename, index)
		}
		if file.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("image[%d] 媒体类型 = %q, want image/png", index, file.Header.Get("Content-Type"))
		}
	}
	assertFilePart(t, form, "image[]", 0, "ref-0", "image/png", refBytes)
	assertFilePart(t, form, "mask", 0, "mask", "image/png", maskBytes)
}

// TestOpenAICreateVideoRequestPayload 录制 /videos 的 multipart 表单：
// 覆盖时长裁剪、比例换算、分辨率归一与 mode 判定（mode 曾漏传导致上游按默认首尾帧处理）。
func TestOpenAICreateVideoRequestPayload(t *testing.T) {
	respondCreated := staticResponder(http.StatusOK, "application/json", []byte(`{"id":"video_123"}`))

	t.Run("首尾帧模式与时长/尺寸归一", func(t *testing.T) {
		server, captured := captureServer(t, respondCreated)
		_, err := NewOpenAI(server.URL, "test-key", server.Client()).CreateVideo(context.Background(), VideoRequest{
			Model:         "sora-2",
			Prompt:        "城市夜景延时",
			Duration:      999,
			Ratio:         "16:9",
			Resolution:    "1080p",
			GenerateAudio: true,
			References: []InlineMedia{
				{Data: []byte("frame-a"), MimeType: "image/png"},
				{Data: []byte("frame-b"), MimeType: "image/png"},
			},
		})
		if err != nil {
			t.Fatalf("CreateVideo 返回错误: %v", err)
		}
		requests := captured()
		if len(requests) != 1 {
			t.Fatalf("应只发出 1 次请求, got %d", len(requests))
		}
		got := requests[0]
		if got.Path != "/v1/videos" {
			t.Fatalf("路径 = %q, want /v1/videos", got.Path)
		}
		form := parseMultipartBody(t, got)
		t.Logf("上游实际收到的 multipart 表单（videos 首尾帧）:")
		logMultipartForm(t, form)
		assertMultipartFields(t, form, map[string]string{
			"model":           "sora-2",
			"prompt":          "城市夜景延时",
			"seconds":         "30",
			"size":            "1920x1080",
			"resolution_name": "1080p",
			"generate_audio":  "true",
			"watermark":       "false",
			"mode":            "frames",
		})
		assertFilePart(t, form, "first_frame", 0, "first.png", "image/png", []byte("frame-a"))
		assertFilePart(t, form, "last_frame", 0, "last.png", "image/png", []byte("frame-b"))
		assertMultipartAbsent(t, form, "image[]", "video[]", "audio[]")
	})

	t.Run("参考图模式显式下发 mode 且秒数下限裁剪", func(t *testing.T) {
		server, captured := captureServer(t, respondCreated)
		_, err := NewOpenAI(server.URL, "test-key", server.Client()).CreateVideo(context.Background(), VideoRequest{
			Model:      "sora-2",
			Prompt:     "按参考图生成",
			Duration:   2,
			Ratio:      "9:16",
			Resolution: "low",
			Mode:       "reference",
			References: []InlineMedia{{Data: []byte("ref-img"), MimeType: "image/png"}},
		})
		if err != nil {
			t.Fatalf("CreateVideo 返回错误: %v", err)
		}
		requests := captured()
		if len(requests) != 1 {
			t.Fatalf("应只发出 1 次请求, got %d", len(requests))
		}
		form := parseMultipartBody(t, requests[0])
		t.Logf("上游实际收到的 multipart 表单（videos 参考图）:")
		logMultipartForm(t, form)
		assertMultipartFields(t, form, map[string]string{
			"model":           "sora-2",
			"prompt":          "按参考图生成",
			"seconds":         "4",
			"size":            "480x854",
			"resolution_name": "480p",
			"generate_audio":  "false",
			"watermark":       "false",
			"mode":            "reference",
		})
		assertFilePart(t, form, "image[]", 0, "ref-0", "image/png", []byte("ref-img"))
		assertMultipartAbsent(t, form, "first_frame", "last_frame", "video[]", "audio[]")
	})
}

// TestOpenAISpeechRequestPayload 录制 /audio/speech 实际发出的 JSON：
// voice / response_format 恒发且走白名单归一，speed 保留两位小数。
func TestOpenAISpeechRequestPayload(t *testing.T) {
	server, captured := captureServer(t, staticResponder(http.StatusOK, "audio/mpeg", []byte("mp3-bytes")))
	result, err := NewOpenAI(server.URL, "test-key", server.Client()).Speech(context.Background(), SpeechRequest{
		Model:        "tts-1",
		Input:        "你好，世界",
		Voice:        " Nova ",
		Format:       "WAV",
		Speed:        1.234,
		Instructions: "放慢语速",
	})
	if err != nil {
		t.Fatalf("Speech 返回错误: %v", err)
	}
	requests := captured()
	if len(requests) != 1 {
		t.Fatalf("应只发出 1 次请求, got %d", len(requests))
	}
	got := requests[0]
	if got.Path != "/v1/audio/speech" {
		t.Fatalf("路径 = %q, want /v1/audio/speech", got.Path)
	}
	if got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got.Header.Get("Content-Type"))
	}
	t.Logf("上游实际收到的 JSON（audio/speech）:")
	logJSONBody(t, got.Body)
	var decoded map[string]any
	if err := json.Unmarshal(got.Body, &decoded); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	want := map[string]any{
		"model":           "tts-1",
		"input":           "你好，世界",
		"voice":           "nova",
		"response_format": "wav",
		"speed":           1.23,
		"instructions":    "放慢语速",
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("请求体字段不符:\n got  %#v\n want %#v", decoded, want)
	}
	if string(result.Data) != "mp3-bytes" || result.MimeType != "audio/mpeg" {
		t.Fatalf("音频结果不符: data=%q mime=%q", result.Data, result.MimeType)
	}
}

// TestOpenAISpeechRejectsJSONErrorBody 上游 200 却回 JSON 错误体时必须报错，
// 不能把错误体当音频字节返回（用户会拿到损坏音频且点数已扣）。
func TestOpenAISpeechRejectsJSONErrorBody(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantContain string
	}{
		{"error 对象消息", `{"error":{"message":"insufficient quota"}}`, "insufficient quota"},
		{"msg 信封", `{"code":1001,"msg":"余额不足"}`, "余额不足"},
		{"无法识别的 JSON", `{"foo":"bar"}`, "JSON 错误响应"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := captureServer(t, staticResponder(http.StatusOK, "application/json", []byte(tc.body)))
			result, err := NewOpenAI(server.URL, "test-key", server.Client()).Speech(context.Background(), SpeechRequest{
				Model: "tts-1", Input: "你好", Voice: "alloy", Format: "mp3",
			})
			var upstream *ErrUpstream
			if !errors.As(err, &upstream) {
				t.Fatalf("应返回 ErrUpstream, got %v", err)
			}
			if upstream.Status != http.StatusBadGateway {
				t.Fatalf("状态码 = %d, want 502", upstream.Status)
			}
			if !strings.Contains(upstream.Body, tc.wantContain) {
				t.Fatalf("错误信息 %q 应包含 %q", upstream.Body, tc.wantContain)
			}
			if len(result.Data) != 0 {
				t.Fatalf("错误体不应作为音频数据返回, got %d 字节", len(result.Data))
			}
		})
	}
}

// TestOpenAIPollVideoEnvelopeError 信封 code 非 0 是终态：必须立刻判失败，
// 否则会被当成 pending 轮询空转到 20 分钟总超时。
func TestOpenAIPollVideoEnvelopeError(t *testing.T) {
	openai := func(server *httptest.Server) *OpenAIProvider {
		return NewOpenAI(server.URL, "test-key", server.Client())
	}
	task := VideoTask{Provider: "openai", UpstreamTaskID: "video_1"}

	t.Run("信封 code 非 0 立即判失败", func(t *testing.T) {
		server, _ := captureServer(t, staticResponder(http.StatusOK, "application/json", []byte(`{"code":1001,"msg":"任务不存在"}`)))
		state, err := openai(server).PollVideo(context.Background(), task)
		if err != nil {
			t.Fatalf("PollVideo 返回错误: %v", err)
		}
		if state.Status != "failed" || state.Error != "任务不存在" {
			t.Fatalf("state = %+v, want failed/任务不存在", state)
		}
	})

	t.Run("无消息时带上业务错误码", func(t *testing.T) {
		server, _ := captureServer(t, staticResponder(http.StatusOK, "application/json", []byte(`{"code":7}`)))
		state, err := openai(server).PollVideo(context.Background(), task)
		if err != nil {
			t.Fatalf("PollVideo 返回错误: %v", err)
		}
		if state.Status != "failed" || !strings.Contains(state.Error, "7") {
			t.Fatalf("state = %+v, want failed 且带错误码 7", state)
		}
	})

	t.Run("code 为 0 的信封仍拆包取数据", func(t *testing.T) {
		server, _ := captureServer(t, staticResponder(http.StatusOK, "application/json",
			[]byte(`{"code":0,"data":{"status":"completed","video_url":"https://cdn.example.com/v.mp4"}}`)))
		state, err := openai(server).PollVideo(context.Background(), task)
		if err != nil {
			t.Fatalf("PollVideo 返回错误: %v", err)
		}
		if state.Status != "succeeded" || state.Video == nil || state.Video.URL != "https://cdn.example.com/v.mp4" {
			t.Fatalf("state = %+v, want succeeded 且带结果 URL", state)
		}
	})

	t.Run("信封外的正常任务负载不受影响", func(t *testing.T) {
		server, _ := captureServer(t, staticResponder(http.StatusOK, "application/json", []byte(`{"id":"video_1","status":"queued"}`)))
		state, err := openai(server).PollVideo(context.Background(), task)
		if err != nil {
			t.Fatalf("PollVideo 返回错误: %v", err)
		}
		if state.Status != "pending" {
			t.Fatalf("state = %+v, want pending", state)
		}
	})
}

// TestOpenAICreateVideoHandleFailure 创建视频拿不到任务句柄时的行为：
// 信封业务错误按上游错误返回；认不出句柄时对遗留任务做 best-effort 取消。
func TestOpenAICreateVideoHandleFailure(t *testing.T) {
	request := VideoRequest{Model: "sora-2", Prompt: "测试", Duration: 5}

	t.Run("信封业务错误按上游错误返回且不取消", func(t *testing.T) {
		server, captured := captureServer(t, staticResponder(http.StatusOK, "application/json", []byte(`{"code":1002,"msg":"内容审核未通过"}`)))
		_, err := NewOpenAI(server.URL, "test-key", server.Client()).CreateVideo(context.Background(), request)
		var upstream *ErrUpstream
		if !errors.As(err, &upstream) {
			t.Fatalf("应返回 ErrUpstream, got %v", err)
		}
		if upstream.Status != http.StatusBadGateway || !strings.Contains(upstream.Body, "内容审核未通过") {
			t.Fatalf("err = %v, want 502 且带上游消息", err)
		}
		for _, req := range captured() {
			if req.Method != http.MethodPost {
				t.Fatalf("信封错误说明上游没建出任务，不应发起取消: %s %s", req.Method, req.Path)
			}
		}
	})

	t.Run("拿不到句柄时尽力取消遗留任务", func(t *testing.T) {
		server, captured := captureServer(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"task_id":"task_9","status":"queued"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
		_, err := NewOpenAI(server.URL, "test-key", server.Client()).CreateVideo(context.Background(), request)
		if err == nil {
			t.Fatalf("拿不到任务句柄应返回错误")
		}
		found := false
		for _, req := range captured() {
			if req.Method == http.MethodDelete && req.Path == "/v1/videos/task_9" {
				found = true
			}
		}
		if !found {
			t.Fatalf("应对遗留任务发起 DELETE /v1/videos/task_9, captured %+v", captured())
		}
	})

	t.Run("识别不出任务 id 时放弃取消", func(t *testing.T) {
		server, captured := captureServer(t, staticResponder(http.StatusOK, "application/json", []byte(`{}`)))
		_, err := NewOpenAI(server.URL, "test-key", server.Client()).CreateVideo(context.Background(), request)
		if err == nil {
			t.Fatalf("拿不到任务句柄应返回错误")
		}
		for _, req := range captured() {
			if req.Method != http.MethodPost {
				t.Fatalf("识别不出任务 id 不应发起取消: %s %s", req.Method, req.Path)
			}
		}
	})

	t.Run("正常信封拆包拿到任务 id", func(t *testing.T) {
		server, _ := captureServer(t, staticResponder(http.StatusOK, "application/json", []byte(`{"code":0,"data":{"id":"video_77"}}`)))
		task, err := NewOpenAI(server.URL, "test-key", server.Client()).CreateVideo(context.Background(), request)
		if err != nil {
			t.Fatalf("CreateVideo 返回错误: %v", err)
		}
		if task.Provider != "openai" || task.UpstreamTaskID != "video_77" || task.PollAfterMs != 5000 {
			t.Fatalf("task = %+v, want openai/video_77/5000", task)
		}
	})
}

// TestOpenAIChatStreamRejectsNonSSEBody 上游 200 但正文不是 SSE（JSON 错误体、HTML
// 错误页、空响应）时必须报错，不能当成空流成功收场让调用方扣点。
func TestOpenAIChatStreamRejectsNonSSEBody(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		wantContain string
	}{
		{"JSON 错误体", "application/json", `{"error":{"message":"model not found"}}`, "model not found"},
		{"HTML 错误页", "text/html", "<html><body>502 Bad Gateway</body></html>", "502 Bad Gateway"},
		{"空响应体", "text/event-stream", "", "SSE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := captureServer(t, staticResponder(http.StatusOK, tc.contentType, []byte(tc.body)))
			sink := &recordingSink{}
			err := NewOpenAI(server.URL, "test-key", server.Client()).ChatStream(context.Background(), ChatRequest{Model: "gpt-5"}, sink)
			var upstream *ErrUpstream
			if !errors.As(err, &upstream) {
				t.Fatalf("应返回 ErrUpstream, got %v", err)
			}
			if upstream.Status != http.StatusBadGateway {
				t.Fatalf("状态码 = %d, want 502", upstream.Status)
			}
			if !strings.Contains(upstream.Body, tc.wantContain) {
				t.Fatalf("错误信息 %q 应包含 %q", upstream.Body, tc.wantContain)
			}
			if len(sink.deltas) != 0 || len(sink.calls) != 0 || sink.done != "" {
				t.Fatalf("非 SSE 响应不应产出内容或调用 Done: deltas=%v calls=%v done=%q", sink.deltas, sink.calls, sink.done)
			}
		})
	}
}

// TestOpenAIChatStreamSSEFrameCriterion 固化「是不是 SSE」的判据：
// 有任意 data 行（含 [DONE]）即算 SSE，没有文本增量也按成功收场；
// 整条流一个 data 行都没有（纯心跳注释后关闭）说明上游什么都没给，按失败处理。
func TestOpenAIChatStreamSSEFrameCriterion(t *testing.T) {
	t.Run("只有心跳注释没有事件按失败处理", func(t *testing.T) {
		sink, err := runChatStream(t, ": ping\n\n: ping\n\n")
		if err == nil {
			t.Fatalf("纯心跳流不应按成功收场")
		}
		if len(sink.deltas) != 0 || sink.done != "" {
			t.Fatalf("失败流不应产出内容: deltas=%v done=%q", sink.deltas, sink.done)
		}
	})

	t.Run("心跳之间夹正常事件按成功处理", func(t *testing.T) {
		sink, err := runChatStream(t, ": keepalive\n\n"+sseBlock("data: [DONE]"))
		if err != nil {
			t.Fatalf("ChatStream 返回错误: %v", err)
		}
		if sink.done != "stop" {
			t.Fatalf("finishReason = %q, want stop", sink.done)
		}
	})

	t.Run("只有 DONE 没有文本仍算成功", func(t *testing.T) {
		sink, err := runChatStream(t, sseBlock("data: [DONE]"))
		if err != nil {
			t.Fatalf("ChatStream 返回错误: %v", err)
		}
		if len(sink.deltas) != 0 || sink.done != "stop" {
			t.Fatalf("deltas=%v done=%q, want 空 deltas + stop", sink.deltas, sink.done)
		}
	})
}
