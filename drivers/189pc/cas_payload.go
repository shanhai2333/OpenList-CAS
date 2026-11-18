package _189pc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/casmeta"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/torrent"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

type casUploadInfo = casmeta.Info

func isCASName(name string) bool {
	return casmeta.IsName(name)
}

// isSidecarName 判断文件名是否已经是某种 CAS 侧车（.cas 或 .cas.torrent），
// 侧车自身绝不能再生成侧车，否则会无限递归。
func isSidecarName(name string) bool {
	return isCASName(name) || isCASTorrentFile(name)
}

func (y *Cloud189PC) casSidecarEnabled() bool {
	return y.CASMode == "sidecar" || y.CASMode == "both"
}

func (y *Cloud189PC) torrentSidecarEnabled() bool {
	switch y.CASMode {
	case "torrent", "both":
		return true
	case "off", "sidecar":
		return false
	default:
		// auto / 未设置：沿用上游行为，由 generate_torrent 决定
		return y.Addition.GenerateTorrent
	}
}

func (y *Cloud189PC) shouldUploadCAS(name string) bool {
	return y.casSidecarEnabled() && !isSidecarName(name) && casmeta.ExtAllowed(name, y.CASExtAllowlist)
}

func (y *Cloud189PC) shouldDeleteSource() bool {
	return y.casSidecarEnabled() && y.DeleteSource
}

func (y *Cloud189PC) uploadCAS(ctx context.Context, dstDir model.Obj, info *casUploadInfo) (model.Obj, error) {
	if info == nil || !y.shouldUploadCAS(info.Name) {
		return nil, nil
	}
	content, err := casmeta.Encode(info)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	casObj := &model.Object{
		Name:     casmeta.FileName(info.Name),
		Size:     int64(len(content)),
		Modified: now,
		Ctime:    now,
		HashInfo: utils.NewHashInfo(utils.MD5, utils.HashData(utils.MD5, content)),
	}
	casStream := &stream.FileStream{
		Ctx:      ctx,
		Obj:      casObj,
		Reader:   bytes.NewReader(content),
		Mimetype: "text/plain",
	}
	// 侧车走 fastUpload：不经过家庭云中转，也不会再生成 torrent/ .cas 侧车
	uploadedCASObj, _, err := y.fastUpload(ctx, dstDir, casStream, func(float64) {}, y.isFamily(), true, false)
	if err != nil {
		return nil, err
	}
	if uploadedCASObj != nil {
		return uploadedCASObj, nil
	}
	return casObj, nil
}

func (y *Cloud189PC) deleteSource(ctx context.Context, dstDir model.Obj, uploadedObj model.Obj, info *casUploadInfo) error {
	if info == nil || !y.shouldDeleteSource() || !y.shouldUploadCAS(info.Name) {
		return nil
	}
	if uploadedObj == nil {
		var err error
		uploadedObj, err = y.findFileByName(ctx, info.Name, dstDir.GetID(), y.isFamily())
		if err != nil {
			return err
		}
	}
	return y.Delete(ctx, IF(y.isFamily(), y.FamilyID, ""), uploadedObj)
}

func (y *Cloud189PC) parseCAS(data []byte) (*casUploadInfo, error) {
	return casmeta.Decode(data)
}

func (y *Cloud189PC) parseCASFromStreamer(file model.FileStreamer) (*casUploadInfo, error) {
	data, err := io.ReadAll(io.LimitReader(file, torrent.DefaultMaxSeedSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > torrent.DefaultMaxSeedSize {
		return nil, fmt.Errorf("cas payload exceeds %d bytes", torrent.DefaultMaxSeedSize)
	}
	return y.parseCAS(data)
}

func (y *Cloud189PC) parseCASFromObj(ctx context.Context, file model.Obj) (*casUploadInfo, error) {
	link, err := y.Link(ctx, file, model.LinkArgs{Type: "raw_cas"})
	if err != nil {
		return nil, err
	}
	defer link.Close()
	if link.URL == "" {
		return nil, fmt.Errorf("cas link has no url")
	}
	req := base.RestyClient.R().SetContext(ctx)
	if link.Header != nil {
		req.SetHeaders(headerToMap(link.Header))
	}
	resp, err := req.Get(link.URL)
	if err != nil {
		return nil, err
	}
	return y.parseCAS(resp.Body())
}

func headerToMap(header http.Header) map[string]string {
	headers := make(map[string]string, len(header))
	for key, values := range header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	return headers
}
