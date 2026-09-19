package ai

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/storage"
	"github.com/infinite-canvas/server/internal/testutil"
)

// 泄漏面①：社区浏览者对已发布作品申请下载必须 404——POST /download 禁走
// findOwned 的社区放行分支，这是本 feature 最严重的泄漏面。
func TestDownloadRequestCommunityViewerForbidden(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	fake := testutil.NewFakeStorage("s3")
	fake.PresignURL = "https://s3.example.com/test-bucket/object?X-Amz-Signature=fake"
	r := newResourceRouter(t, g, cfg, fake)
	owner := testutil.CreateUser(t, g, "dlpub@example.com", "dlpub", "password123", true)
	viewer := testutil.CreateUser(t, g, "dlviewer@example.com", "dlviewer", "password123", true)
	viewerToken := testutil.AccessToken(t, cfg, &viewer)
	// 作者付费：一旦社区分支被误放行，浏览者就能借 /download 触达干净原件路径
	makePaid(t, g, owner)

	key := "image:Pub1"
	file := seedMediaFile(t, g, owner, key, "image/png", testutil.TestPNG)
	fake.Objects[file.ObjectPath] = testutil.TestPNG
	asset := model.Asset{ID: uuid.New(), UserID: owner.ID, Kind: "image", Title: "作品", Data: datatypes.JSON("{}"), StorageKey: key}
	if err := g.Create(&asset).Error; err != nil {
		t.Fatalf("写入素材失败: %v", err)
	}
	work := model.CommunityWork{ID: uuid.New(), UserID: owner.ID, AssetID: asset.ID, Title: "作品", Status: "published", ModerationOK: true}
	if err := g.Create(&work).Error; err != nil {
		t.Fatalf("写入社区作品失败: %v", err)
	}

	if w := testutil.DoRaw(r, http.MethodPost, "/api/v1/media/"+key+"/download", nil, "", viewerToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("社区浏览者申请下载应 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// 泄漏面⑥：严格归属——storageKey 存在但不属于当前用户 → 404（防探测）。
func TestDownloadRequestStrictOwnership(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	r := newResourceRouter(t, g, cfg, testutil.NewFakeStorage("s3"))
	owner := testutil.CreateUser(t, g, "dlown@example.com", "dlown", "password123", true)
	viewer := testutil.CreateUser(t, g, "dlown2@example.com", "dlown2", "password123", true)
	ownerToken := testutil.AccessToken(t, cfg, &owner)
	viewerToken := testutil.AccessToken(t, cfg, &viewer)

	seedMediaFile(t, g, owner, "image:Owned1", "image/png", testutil.TestPNG)

	// key 属于他人 → 404
	if w := testutil.DoRaw(r, http.MethodPost, "/api/v1/media/image:Owned1/download", nil, "", viewerToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("他人 storageKey 申请下载应 404, got %d body=%s", w.Code, w.Body.String())
	}
	// 本人但 key 不存在 → 404
	if w := testutil.DoRaw(r, http.MethodPost, "/api/v1/media/image:NoSuchKey/download", nil, "", ownerToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("不存在的 storageKey 申请下载应 404, got %d", w.Code)
	}
}

// 泄漏面⑤：POST /download 两态响应（free → media url + expiresAt null；
// paid 有 orig → token url + RFC3339），并凭取件链接取回干净原件。
func TestDownloadRequestTwoStateResponse(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	stor := testutil.NewLocalStorage(t.TempDir())
	r := newResourceRouter(t, g, cfg, stor)

	// 态一：免费档 → 现有下发 URL，expiresAt 为 null
	freeUser := testutil.CreateUser(t, g, "dlfree@example.com", "dlfree", "password123", true)
	freeToken := testutil.AccessToken(t, cfg, &freeUser)
	if w := testutil.DoRaw(r, http.MethodPut, "/api/v1/media/image:Dl1", testutil.TestPNG, "image/png", freeToken, nil); w.Code != http.StatusCreated {
		t.Fatalf("免费档上传失败: %d body=%s", w.Code, w.Body.String())
	}
	w := testutil.DoRaw(r, http.MethodPost, "/api/v1/media/image:Dl1/download", nil, "", freeToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("免费档申请下载应 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := testutil.DecodeBody(t, w)
	if body["url"] != "/api/v1/media/image:Dl1" {
		t.Fatalf("免费档应返回下发 URL: %v", body["url"])
	}
	if v, ok := body["expiresAt"]; !ok || v != nil {
		t.Fatalf("免费档 expiresAt 应为 null: %v", body["expiresAt"])
	}

	// 态二：付费且有干净原件 → 取件链接 + RFC3339 到期时间
	paid := testutil.CreateUser(t, g, "dlpaid@example.com", "dlpaid", "password123", true)
	paidToken := testutil.AccessToken(t, cfg, &paid)
	makePaid(t, g, paid)
	origBody := testutil.TestPNG2
	if w := testutil.DoRaw(r, http.MethodPut, "/api/v1/media/image:Dl2", testutil.TestPNG, "image/png", paidToken, nil); w.Code != http.StatusCreated {
		t.Fatalf("付费档上传失败: %d body=%s", w.Code, w.Body.String())
	}
	putObject(t, stor, storage.OrigPath(paid.ID.String(), "image:Dl2"), origBody)
	w = testutil.DoRaw(r, http.MethodPost, "/api/v1/media/image:Dl2/download", nil, "", paidToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("付费档申请下载应 200, got %d body=%s", w.Code, w.Body.String())
	}
	body = testutil.DecodeBody(t, w)
	tokenURL, _ := body["url"].(string)
	if !strings.HasPrefix(tokenURL, "/api/v1/media-download/") {
		t.Fatalf("paid+orig 应返回取件链接: %v", body["url"])
	}
	exp, err := time.Parse(time.RFC3339, body["expiresAt"].(string))
	if err != nil {
		t.Fatalf("expiresAt 应为 RFC3339: %v", body["expiresAt"])
	}
	if remain := time.Until(exp); remain > 300*time.Second || remain < 240*time.Second {
		t.Fatalf("TTL 应钳制在 ≤300s: %s", remain)
	}

	// 凭取件链接取回干净原件：嗅探类型 + 附件名 + no-store
	w = testutil.DoRaw(r, http.MethodGet, tokenURL, nil, "", paidToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("取件应 200, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != string(origBody) {
		t.Fatalf("取件应回流干净原件字节")
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("Content-Type 应按嗅探结果: %s", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Content-Disposition") != `attachment; filename="ic-Dl2.png"` {
		t.Fatalf("Content-Disposition 不符: %s", w.Header().Get("Content-Disposition"))
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("取件缓存头应为 no-store: %s", w.Header().Get("Cache-Control"))
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("取件应带 nosniff")
	}
}

// 泄漏面②：token 过期/篡改 sig/篡改 payload/跨用户/坏格式/归属行已删 → 一律 404。
func TestDownloadTokenValidationChain(t *testing.T) {
	g := testutil.NewTestDB(t)
	cfg := testutil.TestConfig()
	stor := testutil.NewLocalStorage(t.TempDir())
	r := newResourceRouter(t, g, cfg, stor)
	owner := testutil.CreateUser(t, g, "tokowner@example.com", "tokowner", "password123", true)
	other := testutil.CreateUser(t, g, "tokother@example.com", "tokother", "password123", true)
	ownerToken := testutil.AccessToken(t, cfg, &owner)
	otherToken := testutil.AccessToken(t, cfg, &other)
	makePaid(t, g, owner)
	origBody := testutil.TestPNG2
	file := seedMediaFile(t, g, owner, "image:Tok1", "image/png", testutil.TestPNG)
	putObject(t, stor, storage.OrigPath(owner.ID.String(), "image:Tok1"), origBody)

	secret := []byte(cfg.JWTSecret)
	token, _, err := mintDownloadToken(secret, owner.ID, "image:Tok1", downloadTokenTTL)
	if err != nil {
		t.Fatalf("签发令牌失败: %v", err)
	}
	// 对照组：合法令牌 + 本人凭据 → 200 回流原件
	if w := testutil.DoRaw(r, http.MethodGet, "/api/v1/media-download/"+token, nil, "", ownerToken, nil); w.Code != http.StatusOK {
		t.Fatalf("合法令牌取件应 200, got %d body=%s", w.Code, w.Body.String())
	}

	notFound := func(name, tok, bearer string) {
		t.Helper()
		w := testutil.DoRaw(r, http.MethodGet, "/api/v1/media-download/"+tok, nil, "", bearer, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s 应 404, got %d body=%s", name, w.Code, w.Body.String())
		}
	}

	// 坏格式：无签名段 / 多段 / 非 b64url
	notFound("坏格式-无签名段", "garbage", ownerToken)
	notFound("坏格式-三段", token+"."+token, ownerToken)
	notFound("坏格式-非法字符", "!!!!.$$$$", ownerToken)

	// 篡改签名：翻转 sig 最后一字节后重编码
	parts := strings.SplitN(token, ".", 2)
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("解码签名失败: %v", err)
	}
	flipped := append([]byte(nil), sig...)
	flipped[len(flipped)-1] ^= 0xff
	notFound("篡改签名", parts[0]+"."+base64.RawURLEncoding.EncodeToString(flipped), ownerToken)

	// 篡改 payload（未重新签名）：uid 字节翻转
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("解码 payload 失败: %v", err)
	}
	mutated := append([]byte(nil), payload...)
	mutated[9] ^= 0xff
	notFound("篡改 payload", base64.RawURLEncoding.EncodeToString(mutated)+"."+parts[1], ownerToken)

	// 过期：签名有效但 exp 已过
	expired, _, err := mintDownloadToken(secret, owner.ID, "image:Tok1", -time.Minute)
	if err != nil {
		t.Fatalf("签发过期令牌失败: %v", err)
	}
	notFound("过期令牌", expired, ownerToken)

	// 跨用户：令牌属 owner，other 持自己的合法凭据取件 → 404
	notFound("跨用户取件", token, otherToken)

	// 归属行已删：令牌有效但媒体行已删除 → 404
	if err := g.Where("id = ?", file.ID).Delete(&model.MediaFile{}).Error; err != nil {
		t.Fatalf("删除媒体行失败: %v", err)
	}
	fresh, _, err := mintDownloadToken(secret, owner.ID, "image:Tok1", downloadTokenTTL)
	if err != nil {
		t.Fatalf("重新签发令牌失败: %v", err)
	}
	notFound("归属行已删", fresh, ownerToken)
}
