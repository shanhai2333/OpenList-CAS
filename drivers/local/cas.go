package local

import (
	"context"
	"os"
	"path/filepath"

	"github.com/OpenListTeam/OpenList/v4/internal/casmeta"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

type localCASInfo = casmeta.Info

// newLocalCASHasherWriter 分片大小必须与天翼云端规则一致：
// 不同大小档位的分片大小不同，写成固定 10MB 会让大文件的 .cas 无法秒传。
func newLocalCASHasherWriter(size int64) *casmeta.HasherWriter {
	return casmeta.NewHasherWriter(casmeta.SliceSizeFor(size))
}

func localIsCASName(name string) bool {
	return casmeta.IsName(name)
}

func (d *Local) shouldUploadCAS(name string) bool {
	return d.GenerateCAS && !localIsCASName(name) && casmeta.GlobalExtAllowed(name)
}

func (d *Local) uploadCAS(ctx context.Context, dstDir model.Obj, info *localCASInfo) error {
	if info == nil || !d.shouldUploadCAS(info.Name) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	content, err := casmeta.Encode(info)
	if err != nil {
		return err
	}
	casPath := filepath.Join(dstDir.GetPath(), casmeta.FileName(info.Name))
	if err = os.WriteFile(casPath, content, 0o666); err != nil {
		return err
	}
	if d.directoryMap.Has(dstDir.GetPath()) {
		d.directoryMap.UpdateDirSize(dstDir.GetPath())
		d.directoryMap.UpdateDirParents(dstDir.GetPath())
	}
	return nil
}

func (d *Local) deleteSource(ctx context.Context, fullPath string, info *localCASInfo) error {
	if info == nil || !d.DeleteSource || !d.shouldUploadCAS(info.Name) {
		return nil
	}
	// 配置了回收站时，默认的 Remove 只是把文件挪进回收站，空间不会释放，
	// 这里显式要求永久删除。
	removeCtx := context.WithValue(ctx, conf.RemovePermanentlyKey, struct{}{})
	return d.Remove(removeCtx, &model.Object{
		Path: fullPath,
		Name: info.Name,
		Size: info.Size,
	})
}
