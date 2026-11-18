package _189pc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/casmeta"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
)

func resolveCASRestoreName(casName string, info *casUploadInfo) (string, error) {
	return casmeta.ResolveRestoreName(casName, info)
}

func (y *Cloud189PC) restoreCAS(ctx context.Context, dstDir model.Obj, info *casUploadInfo, casName string, temp bool) (model.Obj, error) {
	targetName, err := resolveCASRestoreName(casName, info)
	if err != nil {
		return nil, err
	}
	if !casmeta.ExtAllowed(targetName, y.CASExtAllowlist) {
		return nil, fmt.Errorf("cas restore skipped: extension of %q is not allowed", targetName)
	}
	if temp {
		targetName = fmt.Sprintf("TEMP_%d_%s_%s", time.Now().UnixNano()/1e6, uuid.NewString()[:5], targetName)
	}
	if existing, err := y.findFileByName(ctx, targetName, dstDir.GetID(), y.isFamily()); err == nil && !temp {
		return existing, nil
	}
	restoreInfo := *info
	restoreInfo.Name = targetName

	// 天翼的秒传判重是账号级（个人云/家庭云同一份数据），
	// 家庭云中转只是上传通道，恢复时直接落到目标目录即可。
	return y.restoreCASDirect(ctx, dstDir, &restoreInfo, y.isFamily(), true)
}

// restoreCASDirect 用 .cas 里的 md5/sliceMd5 走秒传把原文件"变"回来。
//
// 分片大小按候选逐档探测：`.cas` 不记录它当初是按哪一档算的，而天翼是按
// 上报的 sliceSize 现场重算判重。历史生成的 .cas 有一批用了 1000 片口径
// （见 casSliceSizeCandidates 注释），用标准档位查不到，只能换一档再问。
func (y *Cloud189PC) restoreCASDirect(ctx context.Context, dstDir model.Obj, info *casUploadInfo, isFamily bool, overwrite bool) (model.Obj, error) {
	fullUrl := UPLOAD_URL
	if isFamily {
		fullUrl += "/family"
	} else {
		fullUrl += "/person"
	}

	candidates := casSliceSizeCandidates(info.Size)
	tried := make([]string, 0, len(candidates))
	var partSizeRejected error
	for _, sliceSize := range candidates {
		tried = append(tried, formatSliceSize(sliceSize))
		uploadFileID, err := y.initCASRestore(ctx, fullUrl, dstDir, info, isFamily, sliceSize)
		if err != nil {
			// 档位被服务端判非法（invalid part Size）：换下一档
			if isPartSizeRejected(err) {
				partSizeRejected = err
				continue
			}
			return nil, err
		}
		if uploadFileID == "" {
			// 该档位合法但摘要对不上（生成端口径不同，或云端数据块已清）
			continue
		}
		return y.commitCASRestore(ctx, fullUrl, uploadFileID, isFamily, overwrite)
	}

	if partSizeRejected != nil {
		return nil, fmt.Errorf("cas restore failed: the cloud rejected every candidate slice size (%s): %w",
			strings.Join(tried, ", "), partSizeRejected)
	}
	return nil, fmt.Errorf("cas restore failed: source file data does not exist in cloud "+
		"(fileMD5=%s, size=%d, tried slice size %s); "+
		"the .cas may be produced with a different slice-size rule, or the cloud data blocks were purged",
		strings.ToUpper(info.MD5), info.Size, strings.Join(tried, ", "))
}

// initCASRestore 返回 uploadFileId；摘要未命中时返回空串（不是错误）。
func (y *Cloud189PC) initCASRestore(ctx context.Context, fullUrl string, dstDir model.Obj, info *casUploadInfo, isFamily bool, sliceSize int64) (string, error) {
	params := Params{
		"parentFolderId": dstDir.GetID(),
		"fileName":       url.QueryEscape(info.Name),
		"fileSize":       fmt.Sprint(info.Size),
		"fileMd5":        strings.ToUpper(info.MD5),
		"sliceSize":      fmt.Sprint(sliceSize),
		"sliceMd5":       strings.ToUpper(info.SliceMD5),
	}
	if isFamily {
		params.Set("familyId", y.FamilyID)
	}
	var uploadInfo InitMultiUploadResp
	if _, err := y.request(fullUrl+"/initMultiUpload", http.MethodGet, func(req *resty.Request) {
		req.SetContext(ctx)
	}, params, &uploadInfo, isFamily); err != nil {
		return "", err
	}
	if uploadInfo.Data.FileDataExists != 1 {
		return "", nil
	}
	return uploadInfo.Data.UploadFileID, nil
}

func (y *Cloud189PC) commitCASRestore(ctx context.Context, fullUrl string, uploadFileID string, isFamily bool, overwrite bool) (model.Obj, error) {
	var resp CommitMultiUploadFileResp
	if _, err := y.request(fullUrl+"/commitMultiUploadFile", http.MethodGet, func(req *resty.Request) {
		req.SetContext(ctx)
	}, Params{
		"uploadFileId": uploadFileID,
		"isLog":        "0",
		"opertype":     IF(overwrite, "3", "1"),
	}, &resp, isFamily); err != nil {
		return nil, err
	}
	return resp.toFile(), nil
}

// isPartSizeRejected 判断错误是不是"分片大小不合法"。
// 天翼会回 `code: InvalidPartSize ,msg: ...,invalid part Size,...`。
func isPartSizeRejected(err error) bool {
	if err == nil {
		return false
	}
	var respErr *RespErr
	if errors.As(err, &respErr) {
		code := strings.ToLower(respErr.Code)
		if strings.Contains(code, "invalidpartsize") || strings.Contains(code, "invalidpart") {
			return true
		}
	}
	return strings.Contains(strings.ToLower(err.Error()), "invalid part size")
}
