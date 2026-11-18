package _189pc

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/casmeta"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
)

func (y *Cloud189PC) shouldPlayCAS(file model.Obj, args model.LinkArgs) bool {
	if !isCASName(file.GetName()) {
		return false
	}
	switch strings.ToLower(args.Type) {
	case "cas_video":
		return true
	default:
		return false
	}
}

func (y *Cloud189PC) CASDownloadRestoreEnabled() bool {
	return y.CASDownloadRestore
}

func (y *Cloud189PC) CASPreviewName(ctx context.Context, file model.Obj) (string, error) {
	if !isCASName(file.GetName()) {
		return file.GetName(), nil
	}
	info, err := y.parseCASFromObjCached(ctx, file)
	if err != nil {
		return "", err
	}
	previewName, err := resolveCASRestoreName(file.GetName(), info)
	if err != nil {
		return "", err
	}
	if !casmeta.ExtAllowed(previewName, y.CASExtAllowlist) {
		return file.GetName(), nil
	}
	if !y.CASDownloadRestore && !isVideoName(previewName) {
		return file.GetName(), nil
	}
	return previewName, nil
}

// .cas 内容生成后不会再变，但 FsGet 会被前端/媒体库/缩略图反复调用，
// 每次都去云端拉一遍 .cas 会明显拖慢首帧，这里做一层进程内短缓存。
type casPreviewCacheEntry struct {
	info   *casUploadInfo
	expire time.Time
}

var casPreviewCache sync.Map

const casPreviewCacheTTL = 5 * time.Minute

func (y *Cloud189PC) parseCASFromObjCached(ctx context.Context, file model.Obj) (*casUploadInfo, error) {
	key := fmt.Sprintf("%d|%s|%d", y.GetStorage().ID, file.GetID(), file.GetSize())
	if v, ok := casPreviewCache.Load(key); ok {
		entry := v.(*casPreviewCacheEntry)
		if time.Now().Before(entry.expire) {
			return entry.info, nil
		}
		casPreviewCache.Delete(key)
	}
	info, err := y.parseCASFromObj(ctx, file)
	if err != nil {
		return nil, err
	}
	casPreviewCache.Store(key, &casPreviewCacheEntry{info: info, expire: time.Now().Add(casPreviewCacheTTL)})
	return info, nil
}

func (y *Cloud189PC) linkCASVideo(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	info, err := y.parseCASFromObj(ctx, file)
	if err != nil {
		return nil, err
	}
	previewName, err := resolveCASRestoreName(file.GetName(), info)
	if err != nil {
		return nil, err
	}
	if !casmeta.ExtAllowed(previewName, y.CASExtAllowlist) {
		return y.Link(ctx, file, model.LinkArgs{IP: args.IP, Header: args.Header, Type: "raw_cas", Redirect: args.Redirect})
	}
	if !y.CASDownloadRestore && !isVideoName(previewName) {
		return y.Link(ctx, file, model.LinkArgs{IP: args.IP, Header: args.Header, Type: "raw_cas", Redirect: args.Redirect})
	}
	y.beginCleanupTask()
	tempRoot, err := y.ensureTempDir(ctx)
	if err != nil {
		y.endCleanupTask()
		return nil, err
	}
	tempIsFamily := y.isFamily()
	tempDir, err := y.createTempSubDir(ctx, tempRoot, casTempSubDirName(), tempIsFamily)
	if err != nil {
		y.endCleanupTask()
		return nil, err
	}

	restoreInfo := *info
	restoreInfo.Name = previewName
	tempObj, err := y.restoreCASDirect(ctx, tempDir, &restoreInfo, tempIsFamily, false)
	if err != nil {
		_ = y.Delete(context.TODO(), IF(tempIsFamily, y.FamilyID, ""), tempDir)
		y.endCleanupTask()
		return nil, err
	}
	link, err := y.linkObj(ctx, tempObj, model.LinkArgs{IP: args.IP, Header: args.Header, Type: "raw_video", Redirect: args.Redirect}, tempIsFamily)
	if err != nil {
		_ = y.Delete(context.TODO(), IF(tempIsFamily, y.FamilyID, ""), tempDir)
		y.endCleanupTask()
		return nil, err
	}
	go func() {
		if err := y.Delete(context.TODO(), IF(tempIsFamily, y.FamilyID, ""), tempDir); err != nil {
			utils.Log.Errorf("casPlayTempDeleteError:%s", err)
		}
		y.endCleanupTask()
	}()
	return link, nil
}

func (y *Cloud189PC) linkObj(ctx context.Context, file model.Obj, args model.LinkArgs, isFamily bool) (*model.Link, error) {
	var downloadUrl struct {
		URL string `json:"fileDownloadUrl"`
	}
	fullUrl := API_URL
	if isFamily {
		fullUrl += "/family/file"
	}
	fullUrl += "/getFileDownloadUrl.action"
	_, err := y.get(fullUrl, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetQueryParam("fileId", file.GetID())
		if isFamily {
			r.SetQueryParam("familyId", y.FamilyID)
		} else {
			r.SetQueryParams(map[string]string{
				"dt":   "3",
				"flag": "1",
			})
		}
	}, &downloadUrl, isFamily)
	if err != nil {
		return nil, err
	}
	downloadUrl.URL = strings.Replace(strings.ReplaceAll(downloadUrl.URL, "&amp;", "&"), "http://", "https://", 1)
	res, err := base.NoRedirectClient.R().SetContext(ctx).SetDoNotParseResponse(true).Get(downloadUrl.URL)
	if err != nil {
		return nil, err
	}
	defer res.RawBody().Close()
	if res.StatusCode() == 302 {
		downloadUrl.URL = res.Header().Get("location")
	}
	return &model.Link{
		URL: downloadUrl.URL,
		Header: http.Header{
			"User-Agent": []string{base.UserAgent},
		},
	}, nil
}

func isVideoName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".mkv", ".avi", ".mov", ".webm", ".flv", ".ts", ".m2ts", ".wmv", ".rmvb", ".m4v", ".mpg", ".mpeg", ".3gp":
		return true
	default:
		return false
	}
}
