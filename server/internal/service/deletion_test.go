package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/infinite-canvas/server/internal/model"
	"github.com/infinite-canvas/server/internal/storage"
)

// requestExpiredDeletion 预约注销并把冷静期推到已届满，供匿名化测试直接触发。
func requestExpiredDeletion(t *testing.T, svc *DeletionService, userID model.User) {
	t.Helper()
	// 预约时间往前推「冷静期 + 1」天，到期时间即落在昨天。
	expired := time.Now().AddDate(0, 0, -DeletionCoolingDays-1)
	if _, err := svc.Request(context.Background(), userID.ID, expired); err != nil {
		t.Fatalf("申请注销失败: %v", err)
	}
}

func TestAnonymizeExpiredDeletesMediaObjectsAndOrigs(t *testing.T) {
	g := newServiceDB(t)
	stor := newMapStorage()
	svc := NewDeletionService(g, stor)
	user := createUserRow(t, g)

	// 两条媒体：正常 key 带干净原件；无冒号 key 的异常行没有原件路径。
	withOrig := seedMedia(t, g, stor, user.ID, "image:Gone1", 100)
	origPath := storage.OrigPath(user.ID.String(), withOrig.StorageKey)
	stor.objects[origPath] = []byte("orig")
	plain := seedMedia(t, g, stor, user.ID, "plainkey", 50)

	requestExpiredDeletion(t, svc, user)
	count, err := svc.AnonymizeExpired(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("匿名化失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("应处理一个到期账号, got %d", count)
	}
	for _, path := range []string{withOrig.ObjectPath, origPath, plain.ObjectPath} {
		if _, ok := stor.objects[path]; ok {
			t.Fatalf("注销后对象应被删除: %s", path)
		}
	}
	// 删除调用恰好覆盖主对象与 orig；无冒号 key 不产生多余的 orig 删除调用。
	assertDeletedExactly(t, stor.deleted, map[string]bool{
		withOrig.ObjectPath: true,
		origPath:            true,
		plain.ObjectPath:    true,
	})
	var mediaCount int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&mediaCount)
	if mediaCount != 0 {
		t.Fatalf("注销后媒体记录应清零, got %d", mediaCount)
	}
}

func TestAnonymizeExpiredOrigDeleteFailureBestEffort(t *testing.T) {
	g := newServiceDB(t)
	stor := newMapStorage()
	svc := NewDeletionService(g, stor)
	user := createUserRow(t, g)

	file := seedMedia(t, g, stor, user.ID, "image:GoneF", 100)
	origPath := storage.OrigPath(user.ID.String(), file.StorageKey)
	stor.objects[origPath] = []byte("orig")
	stor.failDelete = map[string]error{origPath: errors.New("存储故障")}

	requestExpiredDeletion(t, svc, user)
	count, err := svc.AnonymizeExpired(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("orig 删除失败不应影响注销主流程: %v", err)
	}
	if count != 1 {
		t.Fatalf("账号仍应匿名化成功, got %d", count)
	}
	if _, ok := stor.objects[file.ObjectPath]; ok {
		t.Fatalf("主对象仍应被删除")
	}
	if _, ok := stor.objects[origPath]; !ok {
		t.Fatalf("orig 删除失败时对象应保留（best-effort 不吞错误也不假装成功）")
	}
	var mediaCount int64
	g.Model(&model.MediaFile{}).Where("user_id = ?", user.ID).Count(&mediaCount)
	if mediaCount != 0 {
		t.Fatalf("媒体记录仍应清零, got %d", mediaCount)
	}
}
