package _189pc

import (
	"context"
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

func (y *Cloud189PC) restoreCASDirect(ctx context.Context, dstDir model.Obj, info *casUploadInfo, isFamily bool, overwrite bool) (model.Obj, error) {
	sliceSize := partSize(info.Size)
	fullUrl := UPLOAD_URL
	if isFamily {
		fullUrl += "/family"
	} else {
		fullUrl += "/person"
	}
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
	_, err := y.request(fullUrl+"/initMultiUpload", http.MethodGet, func(req *resty.Request) {
		req.SetContext(ctx)
	}, params, &uploadInfo, isFamily)
	if err != nil {
		return nil, err
	}
	if uploadInfo.Data.FileDataExists != 1 {
		return nil, fmt.Errorf("cas restore failed: source file data does not exist in cloud")
	}
	var resp CommitMultiUploadFileResp
	_, err = y.request(fullUrl+"/commitMultiUploadFile", http.MethodGet, func(req *resty.Request) {
		req.SetContext(ctx)
	}, Params{
		"uploadFileId": uploadInfo.Data.UploadFileID,
		"isLog":        "0",
		"opertype":     IF(overwrite, "3", "1"),
	}, &resp, isFamily)
	if err != nil {
		return nil, err
	}
	return resp.toFile(), nil
}
