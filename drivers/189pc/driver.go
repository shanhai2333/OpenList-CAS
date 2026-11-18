package _189pc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/casmeta"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	istream "github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/cron"
	"github.com/OpenListTeam/OpenList/v4/pkg/torrent"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
)

type Cloud189PC struct {
	model.Storage
	Addition

	client *resty.Client

	loginParam  *LoginParam
	qrcodeParam *QRLoginParam

	tokenInfo *AppSessionResp

	uploadThread int

	familyTransferFolder    *Cloud189Folder
	cleanFamilyTransferFile func()

	// .cas 侧车相关状态
	debounceClean       func()
	cleanupMu           sync.Mutex
	cleanupTimer        *time.Timer
	cleanupActive       int
	autoRestoreMu       sync.Mutex
	autoRestoreInFlight sync.Map
	autoRestoreCron     *cron.Cron

	storageConfig driver.Config
	ref           *Cloud189PC
	cron          *cron.Cron
}

func (y *Cloud189PC) Config() driver.Config {
	if y.storageConfig.Name == "" {
		y.storageConfig = config
	}
	return y.storageConfig
}

func (y *Cloud189PC) GetAddition() driver.Additional {
	return &y.Addition
}

func (y *Cloud189PC) Init(ctx context.Context) (err error) {
	y.storageConfig = config
	casmeta.SetGlobalExtAllowlist(y.CASExtAllowlist)
	if y.isFamily() {
		// 兼容旧上传接口
		if y.Addition.RapidUpload || y.Addition.UploadMethod == "old" {
			y.storageConfig.NoOverwriteUpload = true
		}
	} else {
		// 家庭云转存，不支持覆盖上传
		if y.Addition.FamilyTransfer {
			y.storageConfig.NoOverwriteUpload = true
		}
	}
	// 处理个人云和家庭云参数
	if y.isFamily() && y.RootFolderID == "-11" {
		y.RootFolderID = ""
	}
	if !y.isFamily() && y.RootFolderID == "" {
		y.RootFolderID = "-11"
	}

	// 限制上传线程数
	y.uploadThread, _ = strconv.Atoi(y.UploadThread)
	if y.uploadThread < 1 || y.uploadThread > 32 {
		y.uploadThread, y.UploadThread = 3, "3"
	}

	if y.ref == nil {
		// 初始化请求客户端
		if y.client == nil {
			y.client = base.NewRestyClient().SetHeaders(map[string]string{
				"Accept":  "application/json;charset=UTF-8",
				"Referer": WEB_URL,
			})
		}

		// 先尝试用Token刷新，之后尝试登陆
		if y.Addition.AccessToken != "" {
			y.tokenInfo = &AppSessionResp{AccessToken: y.Addition.AccessToken, RefreshToken: y.Addition.RefreshToken}
			if err = y.refreshSession(); err != nil {
				return err
			}
		} else if y.Addition.RefreshToken != "" {
			y.tokenInfo = &AppSessionResp{RefreshToken: y.Addition.RefreshToken}
			if err = y.refreshToken(); err != nil {
				return err
			}
		} else {
			if err = y.login(); err != nil {
				return err
			}
		}

		// 初始化并启动 cron 任务
		y.cron = cron.NewCron(time.Duration(time.Minute * 5))
		// 每5分钟执行一次 keepAlive
		y.cron.Do(y.keepAlive)
	}

	// 处理家庭云ID
	if y.FamilyID == "" {
		if y.FamilyID, err = y.getFamilyID(); err != nil {
			return err
		}
	}

	// 创建中转文件夹
	if y.FamilyTransfer {
		if err := y.createFamilyTransferFolder(); err != nil {
			return err
		}
	}

	// 清理转存文件节流
	y.cleanFamilyTransferFile = utils.NewThrottle2(time.Minute, func() {
		if err := y.cleanFamilyTransfer(context.TODO()); err != nil {
			utils.Log.Errorf("cleanFamilyTransferFolderError:%s", err)
		}
	})

	// .cas 临时目录清理（防抖）+ 自动恢复定时任务
	y.debounceClean = y.newDebounceCleaner()
	if y.AutoRestoreExistingCAS && strings.TrimSpace(y.AutoRestoreExistingCASPaths) != "" {
		y.autoRestoreCron = cron.NewCron(y.autoRestoreInterval())
		y.autoRestoreCron.Do(func() {
			if err := y.autoRestoreCAS(context.TODO()); err != nil {
				utils.Log.Errorf("autoRestoreCASError:%s", err)
			}
		})
	}
	return err
}

func (d *Cloud189PC) InitReference(storage driver.Driver) error {
	refStorage, ok := storage.(*Cloud189PC)
	if ok {
		d.ref = refStorage
		return nil
	}
	return errs.NotSupport
}

func (y *Cloud189PC) Drop(ctx context.Context) error {
	y.ref = nil
	if y.cron != nil {
		y.cron.Stop()
		y.cron = nil
	}
	if y.autoRestoreCron != nil {
		y.autoRestoreCron.Stop()
		y.autoRestoreCron = nil
	}
	y.cleanupMu.Lock()
	if y.cleanupTimer != nil {
		y.cleanupTimer.Stop()
		y.cleanupTimer = nil
	}
	y.cleanupMu.Unlock()
	return nil
}

func (y *Cloud189PC) autoRestoreInterval() time.Duration {
	minutes := y.GetStorage().CacheExpiration
	if minutes <= 0 {
		minutes = 30
	}
	return time.Duration(minutes) * time.Minute
}

func (y *Cloud189PC) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	if dirPath, ok := y.manualRefreshAutoRestorePath(args); ok {
		y.startManualRefreshAutoRestore(ctx, dir, dirPath)
	}
	return y.getFiles(ctx, dir.GetID(), y.isFamily())
}

func (y *Cloud189PC) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	if y.shouldPlayCAS(file, args) {
		return y.linkCASVideo(ctx, file, args)
	}
	var downloadUrl struct {
		URL string `json:"fileDownloadUrl"`
	}

	isFamily := y.isFamily()
	fullUrl := API_URL
	if isFamily {
		fullUrl += "/family/file"
	}
	fullUrl += "/getFileDownloadUrl.action"

	_, err := y.get(fullUrl, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetQueryParam("fileId", file.GetID())
		if isFamily {
			r.SetQueryParams(map[string]string{
				"familyId": y.FamilyID,
			})
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

	// 重定向获取真实链接
	downloadUrl.URL = strings.Replace(strings.ReplaceAll(downloadUrl.URL, "&amp;", "&"), "http://", "https://", 1)
	res, err := base.NoRedirectClient.R().SetContext(ctx).SetDoNotParseResponse(true).Get(downloadUrl.URL)
	if err != nil {
		return nil, err
	}
	defer res.RawBody().Close()
	if res.StatusCode() == 302 {
		downloadUrl.URL = res.Header().Get("location")
	}

	like := &model.Link{
		URL: downloadUrl.URL,
		Header: http.Header{
			"User-Agent": []string{base.UserAgent},
		},
	}
	/*
		// 获取链接有效时常
		strs := regexp.MustCompile(`(?i)expire[^=]*=([0-9]*)`).FindStringSubmatch(downloadUrl.URL)
		if len(strs) == 2 {
			timestamp, err := strconv.ParseInt(strs[1], 10, 64)
			if err == nil {
				expired := time.Duration(timestamp-time.Now().Unix()) * time.Second
				like.Expiration = &expired
			}
		}
	*/
	return like, nil
}

func (y *Cloud189PC) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	isFamily := y.isFamily()
	fullUrl := API_URL
	if isFamily {
		fullUrl += "/family/file"
	}
	fullUrl += "/createFolder.action"

	var newFolder Cloud189Folder
	_, err := y.post(fullUrl, func(req *resty.Request) {
		req.SetContext(ctx)
		req.SetQueryParams(map[string]string{
			"folderName":   dirName,
			"relativePath": "",
		})
		if isFamily {
			req.SetQueryParams(map[string]string{
				"familyId": y.FamilyID,
				"parentId": parentDir.GetID(),
			})
		} else {
			req.SetQueryParams(map[string]string{
				"parentFolderId": parentDir.GetID(),
			})
		}
	}, &newFolder, isFamily)
	if err != nil {
		return nil, err
	}
	return &newFolder, nil
}

func (y *Cloud189PC) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	isFamily := y.isFamily()
	other := map[string]string{"targetFileName": dstDir.GetName()}

	resp, err := y.CreateBatchTask("MOVE", IF(isFamily, y.FamilyID, ""), dstDir.GetID(), other, BatchTaskInfo{
		FileId:   srcObj.GetID(),
		FileName: srcObj.GetName(),
		IsFolder: BoolToNumber(srcObj.IsDir()),
	})
	if err != nil {
		return nil, err
	}
	if err = y.WaitBatchTask("MOVE", resp.TaskID, time.Millisecond*400); err != nil {
		return nil, err
	}

	// 跟随移动 torrent 文件
	if !srcObj.IsDir() {
		var srcFolderId string
		if f, ok := srcObj.(*Cloud189File); ok {
			srcFolderId = f.ParentID
		}
		y.torrentFollowMove(srcFolderId, srcObj.GetName(), dstDir)
	}

	return srcObj, nil
}

func (y *Cloud189PC) Rename(ctx context.Context, srcObj model.Obj, newName string) (model.Obj, error) {
	isFamily := y.isFamily()
	queryParam := make(map[string]string)
	fullUrl := API_URL
	method := http.MethodPost
	if isFamily {
		fullUrl += "/family/file"
		method = http.MethodGet
		queryParam["familyId"] = y.FamilyID
	}

	switch srcObj.(type) {
	case *Cloud189File:
		fullUrl += "/renameFile.action"
		queryParam["fileId"] = srcObj.GetID()
		queryParam["destFileName"] = newName
	case *Cloud189Folder:
		fullUrl += "/renameFolder.action"
		queryParam["folderId"] = srcObj.GetID()
		queryParam["destFolderName"] = newName
	default:
		return nil, errs.NotSupport
	}
	var resp RenameResp
	_, err := y.request(fullUrl, method, func(req *resty.Request) {
		req.SetContext(ctx).SetQueryParams(queryParam)
	}, nil, &resp, isFamily)
	if err != nil {
		if code, ok := resp.ResCode.(string); ok && code == "FileAlreadyExists" {
			return nil, errs.ObjectAlreadyExists
		}
		return nil, err
	}

	// 跟随重命名 torrent 文件
	if f, ok := srcObj.(*Cloud189File); ok {
		y.torrentFollowRename(f.ParentID, srcObj.GetName(), newName)
	}

	switch f := srcObj.(type) {
	case *Cloud189File:
		return resp.toFile(f), nil
	case *Cloud189Folder:
		return resp.toFolder(), nil
	}
	return nil, errs.NotSupport
}

func (y *Cloud189PC) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	isFamily := y.isFamily()
	other := map[string]string{"targetFileName": dstDir.GetName()}

	resp, err := y.CreateBatchTask("COPY", IF(isFamily, y.FamilyID, ""), dstDir.GetID(), other, BatchTaskInfo{
		FileId:   srcObj.GetID(),
		FileName: srcObj.GetName(),
		IsFolder: BoolToNumber(srcObj.IsDir()),
	})
	if err != nil {
		return err
	}
	if err = y.WaitBatchTask("COPY", resp.TaskID, time.Second); err != nil {
		return err
	}

	// 跟随复制 torrent 文件
	if !srcObj.IsDir() {
		var srcFolderId string
		if f, ok := srcObj.(*Cloud189File); ok {
			srcFolderId = f.ParentID
		}
		y.torrentFollowCopy(srcFolderId, srcObj.GetName(), dstDir)
	}

	return nil
}

func (y *Cloud189PC) Remove(ctx context.Context, obj model.Obj) error {
	isFamily := y.isFamily()

	resp, err := y.CreateBatchTask("DELETE", IF(isFamily, y.FamilyID, ""), "", nil, BatchTaskInfo{
		FileId:   obj.GetID(),
		FileName: obj.GetName(),
		IsFolder: BoolToNumber(obj.IsDir()),
	})
	if err != nil {
		return err
	}
	// 批量任务数量限制，过快会导致无法删除
	return y.WaitBatchTask("DELETE", resp.TaskID, time.Millisecond*200)
}

func familyTransferTempName(srcName string) string {
	ext := path.Ext(srcName)
	if ext == "" {
		ext = ".transfer"
	}
	return fmt.Sprintf("0%s%s", uuid.NewString(), ext)
}

func (y *Cloud189PC) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (newObj model.Obj, err error) {
	sourceName := stream.GetName()
	sourceSize := stream.GetSize()

	// 拖入 .cas：直接按元数据恢复源文件（等同于秒传）
	if y.RestoreSourceFromCAS && isCASName(sourceName) {
		casData, readErr := io.ReadAll(io.LimitReader(stream, torrent.DefaultMaxSeedSize+1))
		if readErr != nil {
			return nil, readErr
		}
		info, parseErr := y.parseCAS(casData)
		if parseErr != nil {
			return nil, parseErr
		}
		restoredName, nameErr := resolveCASRestoreName(sourceName, info)
		if nameErr != nil {
			return nil, nameErr
		}
		if casmeta.ExtAllowed(restoredName, y.CASExtAllowlist) {
			restored, restoreErr := y.restoreCAS(ctx, dstDir, info, sourceName, false)
			if restoreErr != nil {
				return nil, restoreErr
			}
			y.notifyTaskDone()
			return restored, nil
		}
		// 扩展名不在白名单内：不恢复，按普通文件上传（流已读完，用缓存重建）
		stream = &istream.FileStream{
			Ctx:               ctx,
			Obj:               &model.Object{Name: sourceName, Size: int64(len(casData)), Modified: stream.ModTime(), Ctime: stream.CreateTime(), HashInfo: stream.GetHash()},
			Reader:            bytes.NewReader(casData),
			Mimetype:          stream.GetMimetype(),
			ForceStreamUpload: stream.IsForceStreamUpload(),
			Exist:             stream.GetExist(),
		}
	}

	newObj, info, err := y.uploadFile(ctx, dstDir, stream, up)
	if err != nil {
		return nil, err
	}
	if info != nil {
		// 家庭云中转会改临时名，这里以用户看到的源文件名为准
		info.Name = sourceName
		info.Size = sourceSize
	}
	casObj, casErr := y.uploadCAS(ctx, dstDir, info)
	if casErr != nil {
		utils.Log.Warnf("failed to generate .cas for %s: %v", sourceName, casErr)
	}
	if casObj != nil && y.shouldDeleteSource() {
		if err = y.deleteSource(ctx, dstDir, newObj, info); err != nil {
			return nil, err
		}
		// 源文件已删，目录缓存里的旧条目要清掉
		op.Cache.DeleteDirectory(y, dstDir.GetPath())
		y.notifyTaskDone()
		return casObj, nil
	}
	y.notifyTaskDone()
	return newObj, nil
}

// uploadFile 是原本的上传编排（含家庭云中转 defer），额外把哈希信息带出来给 .cas 用。
func (y *Cloud189PC) uploadFile(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (newObj model.Obj, info *casUploadInfo, err error) {
	overwrite := true
	isFamily := y.isFamily()

	// 响应时间长,按需启用
	if y.Addition.RapidUpload && !stream.IsForceStreamUpload() {
		// 尝试妙传
		if newObj, info, err := y.RapidUpload(ctx, dstDir, stream, isFamily, overwrite); err == nil {
			return newObj, info, nil
		}
	}

	uploadMethod := y.UploadMethod
	if stream.IsForceStreamUpload() {
		uploadMethod = "stream"
	} else if y.Addition.RapidUpload && stream.GetFile() != nil {
		// 文件流支持随机读取，走FastUpload计算MD5并尝试秒传
		uploadMethod = "rapid"
	} else if uploadMethod == "old" {
		// 旧版上传家庭云也有限制
		return y.OldUpload(ctx, dstDir, stream, up, isFamily, overwrite)
	}

	// 开启家庭云转存
	if !isFamily && y.FamilyTransfer {
		// 修改上传目标为家庭云文件夹
		transferDstDir := dstDir
		dstDir = y.familyTransferFolder

		// 使用临时文件名
		srcName := stream.GetName()
		stream = &WrapFileStreamer{
			FileStreamer: stream,
			Name:         familyTransferTempName(srcName),
		}

		// 使用家庭云上传
		isFamily = true
		overwrite = false

		defer func() {
			if newObj != nil {
				// 转存家庭云文件到个人云
				err = y.SaveFamilyFileToPersonCloud(context.TODO(), y.FamilyID, newObj, transferDstDir, true)
				// 删除家庭云源文件
				go y.Delete(context.TODO(), y.FamilyID, newObj)
				// 批量任务有概率删不掉
				go y.cleanFamilyTransferFile()
				// 转存失败返回错误
				if err != nil {
					return
				}

				// 查找转存文件
				var file *Cloud189File
				file, err = y.findFileByName(context.TODO(), newObj.GetName(), transferDstDir.GetID(), false)
				if err != nil {
					if err == errs.ObjectNotFound {
						err = fmt.Errorf("unknown error: No transfer file obtained %s", newObj.GetName())
					}
					return
				}

				// 重命名转存文件
				newObj, err = y.Rename(context.TODO(), file, srcName)
				if err != nil {
					// 重命名失败删除源文件
					_ = y.Delete(context.TODO(), "", file)
				}
				return
			}
		}()
	}

	switch uploadMethod {
	case "rapid":
		return y.FastUpload(ctx, dstDir, stream, up, isFamily, overwrite)
	case "stream":
		if stream.GetSize() == 0 {
			return y.FastUpload(ctx, dstDir, stream, up, isFamily, overwrite)
		}
		fallthrough
	default:
		return y.StreamUpload(ctx, dstDir, stream, up, isFamily, overwrite)
	}
}

func (y *Cloud189PC) GetDetails(ctx context.Context) (*model.StorageDetails, error) {
	capacityInfo, err := y.getCapacityInfo(ctx)
	if err != nil {
		return nil, err
	}
	var total, used int64
	if y.isFamily() {
		total = capacityInfo.FamilyCapacityInfo.TotalSize
		used = capacityInfo.FamilyCapacityInfo.UsedSize
	} else {
		total = capacityInfo.CloudCapacityInfo.TotalSize
		used = capacityInfo.CloudCapacityInfo.UsedSize
	}
	return &model.StorageDetails{
		DiskUsage: model.DiskUsage{
			TotalSpace: total,
			UsedSpace:  used,
		},
	}, nil
}
