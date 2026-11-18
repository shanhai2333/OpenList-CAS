package _189pc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
)

const casTempDirName = "TEMP"

func casTempSubDirName() string {
	return fmt.Sprintf("TEMP_%d_%s", time.Now().UnixNano()/1e6, uuid.NewString()[:5])
}

func (y *Cloud189PC) ensureTempDir(ctx context.Context) (model.Obj, error) {
	isFamily := y.isFamily()
	if obj, err := y.findFolderByName(ctx, casTempDirName, IF(isFamily, "", y.RootFolderID), isFamily); err == nil {
		return obj, nil
	}
	fullUrl := API_URL
	if isFamily {
		fullUrl += "/family/file"
	}
	fullUrl += "/createFolder.action"
	var newFolder Cloud189Folder
	_, err := y.post(fullUrl, func(req *resty.Request) {
		req.SetContext(ctx)
		req.SetQueryParams(map[string]string{
			"folderName":   casTempDirName,
			"relativePath": "",
		})
		if isFamily {
			req.SetQueryParams(map[string]string{
				"familyId": y.FamilyID,
				"parentId": "",
			})
		} else {
			req.SetQueryParam("parentFolderId", y.RootFolderID)
		}
	}, &newFolder, isFamily)
	if err != nil {
		return nil, err
	}
	return &newFolder, nil
}

func (y *Cloud189PC) createTempSubDir(ctx context.Context, parentDir model.Obj, dirName string, isFamily bool) (model.Obj, error) {
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
			req.SetQueryParam("parentFolderId", parentDir.GetID())
		}
	}, &newFolder, isFamily)
	if err != nil {
		return nil, err
	}
	return &newFolder, nil
}

func (y *Cloud189PC) findFolderByName(ctx context.Context, searchName string, folderId string, isFamily bool) (*Cloud189Folder, error) {
	for pageNum := 1; ; pageNum++ {
		resp, err := y.getFilesWithPage(ctx, folderId, isFamily, pageNum, 10, "filename", "asc")
		if err != nil {
			return nil, err
		}
		if resp.FileListAO.Count == 0 {
			return nil, errs.ObjectNotFound
		}
		for i := 0; i < len(resp.FileListAO.FolderList); i++ {
			folder := resp.FileListAO.FolderList[i]
			if folder.Name == searchName {
				return &folder, nil
			}
		}
	}
}

func (y *Cloud189PC) notifyTaskDone() {
	if y.debounceClean != nil {
		y.debounceClean()
	}
}

func (y *Cloud189PC) beginCleanupTask() {
	y.cleanupMu.Lock()
	y.cleanupActive++
	y.cleanupMu.Unlock()
}

func (y *Cloud189PC) endCleanupTask() {
	y.cleanupMu.Lock()
	if y.cleanupActive > 0 {
		y.cleanupActive--
	}
	y.cleanupMu.Unlock()
	y.notifyTaskDone()
}

func (y *Cloud189PC) newDebounceCleaner() func() {
	return func() {
		y.cleanupMu.Lock()
		defer y.cleanupMu.Unlock()
		y.scheduleDebounceCleanLocked()
	}
}

func (y *Cloud189PC) scheduleDebounceCleanLocked() {
	if y.cleanupActive > 0 {
		return
	}
	if y.cleanupTimer != nil {
		y.cleanupTimer.Stop()
	}
	y.cleanupTimer = time.AfterFunc(5*time.Second, y.runDebounceClean)
}

func (y *Cloud189PC) runDebounceClean() {
	y.cleanupMu.Lock()
	shouldCleanAll := y.cleanupActive == 0
	y.cleanupTimer = nil
	y.cleanupMu.Unlock()

	if !shouldCleanAll {
		return
	}
	if err := y.cleanTempFolder(context.TODO()); err != nil {
		utils.Log.Errorf("casTempCleanError:%s", err)
	}
}

func (y *Cloud189PC) cleanTempFolder(ctx context.Context) error {
	isFamily := y.isFamily()
	tempDir, err := y.findFolderByName(ctx, casTempDirName, IF(isFamily, "", y.RootFolderID), isFamily)
	if err != nil {
		return nil
	}
	files, err := y.getFiles(ctx, tempDir.GetID(), isFamily)
	if err != nil {
		return err
	}
	for _, obj := range files {
		if !strings.HasPrefix(obj.GetName(), "TEMP_") {
			continue
		}
		if err := y.Delete(ctx, IF(isFamily, y.FamilyID, ""), obj); err != nil {
			utils.Log.Errorf("casTempDeleteError:%s", err)
		}
	}
	return nil
}
