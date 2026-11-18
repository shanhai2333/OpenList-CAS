<div align="center">
  <img src="https://raw.githubusercontent.com/OpenListTeam/Logo/main/logo.svg" width="128" height="128" alt="logo" />

  <h1>OpenList-CAS</h1>

  <p><em>基于 <a href="https://github.com/OpenListTeam/OpenList">OpenList</a> 的「天翼云盘 CAS 秒传」增强分支</em></p>

  <a href="https://github.com/shanhai2333/OpenList-CAS/actions"><img src="https://img.shields.io/github/actions/workflow/status/shanhai2333/OpenList-CAS/build.yml?branch=main" alt="Build status" /></a>
  <a href="https://github.com/shanhai2333/OpenList-CAS/releases"><img src="https://img.shields.io/github/v/release/shanhai2333/OpenList-CAS" alt="latest version" /></a>
  <a href="./LICENSE"><img src="https://img.shields.io/github/license/shanhai2333/OpenList-CAS" alt="License" /></a>
</div>

---

## 这是什么

本仓库是 [OpenList](https://github.com/OpenListTeam/OpenList) 的非官方增强分支，在**完整保留上游功能**的前提下，围绕天翼云盘（189）的 `.cas` 秒传元数据增加了生成、恢复、临时播放与自动恢复能力。

- **代码基座跟随上游**：分支基于上游 `main`，通过 merge 同步更新，不另起一套实现。
- **两套 CAS 并存**：上游的 `.cas.torrent`（torrent 内嵌 CAS）与本分支的 `.cas`（纯元数据侧车）可以同时使用，格式互通。
- **版本号**：`vX.Y.Z-cas.N`，`X.Y.Z` 对应当前所基于的上游版本，`N` 是本分支的修订号。

## 特性

### CAS（本分支增强）

- 上传后自动生成 `<文件名>.cas`（`base64(json)`，只记录 `name / size / md5 / sliceMd5 / create_time`）
- 拖入 `.cas` 即按元数据秒传恢复原文件；可配置恢复后删除 `.cas`
- 生成 `.cas` 后可自动删除源文件，节省空间
- `.cas` 视频可在线播放：播放时在后端恢复临时文件，播完自动清理
- 按目录定时/手动刷新自动恢复已有 `.cas`
- 上传 `.cas` 侧车自身不会再生成侧车（不会出现 `x.cas.cas`）
- Local 本地存储同样支持生成 `.cas`（`delete_source` 为真删除，不依赖回收站）

`189CloudPC` 的 `cas_mode` 决定上传后产出什么：

| `cas_mode` | 上传后的产物 |
|---|---|
| `auto`（默认） | 沿用旧开关：仅在 `generate_torrent` 打开时生成 `.cas.torrent` |
| `off` | 不生成任何侧车 |
| `sidecar` | `<文件名>.cas` 元数据文件 |
| `torrent` | `<文件名>.cas.torrent`（带 CAS 扩展的种子） |
| `both` | 两种都生成 |

### 与上游一致的通用能力

- 多存储挂载：本地、阿里云盘、OneDrive / SharePoint（国际 / 世纪互联 / DE / US）、天翼云盘（个人 / 家庭）、Google Drive、123 网盘、FTP / SFTP、PikPak、S3、Seafile、又拍云、WebDAV、Teambition、MediaFire、Mediatrack、ProtonDrive、139 云盘、Yandex Disk、百度网盘、Terabox、UC、夸克、迅雷、蓝奏云、Google 相册、MEGA、百度相册、SMB、115、Cloudreve、Dropbox、飞机盘、DogeCloud OSS、Azure Blob、超星、CNB、Degoo、豆包、Febbox、GitHub、OpenList、Teldrive、微云、钉钉文档等
- 开箱即用的部署体验
- 文件预览（PDF / Markdown / 代码 / 纯文本等）、图片画廊、音视频播放（支持歌词与字幕）
- Office 文档预览（docx / pptx / xlsx 等）、`README.md` 渲染
- 直链复制与文件直链下载、深色模式、多语言
- 受保护路由（密码与身份认证）、WebDAV、Docker 部署
- 文件/文件夹打包下载、网页上传（可开放给访客）/ 删除 / 新建 / 重命名 / 移动 / 复制
- 离线下载、跨存储复制、单线程下载/串流的多线程加速

## 快速开始

### Docker Compose

```yaml
services:
  openlist-cas:
    image: shanhaijun/openlist-cas:beta
    container_name: openlist-cas
    restart: always
    user: '0:0'
    ports:
      - '5244:5244'
      - '5245:5245'
    environment:
      - UMASK=022
      - TZ=Asia/Shanghai
    volumes:
      - './data:/opt/openlist/data'
      - '/path/to/local:/mnt/local'
```

镜像地址取决于 DockerHub 的配置，见下文「构建与发布」。首次启动后用 `docker exec -it openlist-cas ./openlist admin set <新密码>` 设置管理员密码。

### 从源码构建

```bash
# 1) 拉取前端产物到 public/dist（从 OpenList-Frontend 的 release 下载，不需要 Node）
bash build.sh dev web

# 2) 编译当前平台（go.mod 要求 go 1.26.4 / toolchain go 1.27.3）
go build -tags jsoniter -o openlist .

# 运行
./openlist server
```

`bash build.sh release` 会一次性产出全部发布目标，但依赖 musl 交叉编译工具链（CI 里由 workflow 安装）；只想本地跑起来用上面两步即可。

## CAS 使用说明

以 `189CloudPC` 存储为例：

1. 添加存储并登录天翼账号；
2. 把 `cas_mode` 设为 `sidecar`（或 `both`）；
3. 按需打开：
   - `delete_source`：生成 `.cas` 后删除源文件（**注意下方警告**）
   - `restore_source_from_cas`：上传 `.cas` 时直接恢复源文件
   - `delete_cas_after_restore`：恢复后删除 `.cas`
   - `auto_restore_existing_cas` + `auto_restore_existing_cas_paths`：按目录自动恢复
   - `cas_ext_allowlist`：参与 CAS 的扩展名白名单（与 Local 共用，空表示全部允许）
   - `cas_download_restore`：通过 `/d/*` 直接下载 `.cas` 时返回恢复后的真实文件

Local 存储只有 `generate_cas` 与 `delete_source` 两个开关。

### ⚠️ 重要：`.cas` 不是备份

天翼云盘的「删除」是移入回收站，数据块仍然存在，秒传才能命中。**清空回收站会让对应 `.cas` 永久失效**；源文件被彻底删除后，`.cas` 只是一段无法恢复的文本。

### 分片档位与旧 `.cas` 的兼容性

天翼的分片大小由文件大小决定（≤9.77GiB 为 10MiB，更大为 20MiB 起）。不同生成端对「分片数上限」的取值可能差一位（999 与 1000），只在两个 10MiB 宽的边界区间内产生差异：

- `(9.7559, 9.7656] GiB`
- `(19.51, 19.53] GiB`

本分支恢复时会**逐档探测**分片大小，命中即恢复，因此由不同生成端产出的 `.cas` 都能用，不需要源文件、也不需要修改 `.cas`。

## 与 MyMediaHelper 的互通

本仓库的 `.cas` 与 **MyMediaHelper**（本地自用的媒体整理工具）生成端完全同构：外层 `base64`，内层 JSON 五字段逐字一致。任一端生成的 `.cas` 都可以被另一端读取；上游 `pkg/torrent` 的 CAS 载荷同样兼容，并额外支持 `partsMd5 / slice_size / cloud / files` 扩展字段（旧解码器会忽略）。

## 构建与发布

发布依赖 GitHub Actions：

| 工作流 | 触发 | 作用 |
|---|---|---|
| `Beta Release builds` | push `main` / 手动 | 多平台交叉编译并上传到 `beta` release |
| `Beta Release (Docker)` | push `main` / PR / 手动 | 构建并推送 `beta` 镜像 |
| `Release builds (Docker)` | push `v*` tag / 手动 | 构建并推送对应版本 tag 的镜像 |
| `Release Automatic changelog` | push `v*` tag | 生成 release notes |
| `Test Build` | PR / 手动 | 单元测试 + 多目标编译 |

### 仓库需要配置的 Secrets 与 Variables

位置：仓库 **Settings → Secrets and variables → Actions**。`GITHUB_TOKEN` 由 GitHub 自动注入，无需配置。

**Secrets（加密，必填项只有第一个）**

| 名称 | 必填 | 用途 |
|---|---|---|
| `DOCKERHUB_TOKEN` | ✅（要推镜像时） | DockerHub Access Token，需 `Read & Write` 权限；用于 `beta` 与 `v*` 镜像推送 |
| `GITEE_SSH_PRIVATE_KEY` | 可选 | 只有开启 Gitee 同步时才需要，配合变量 `GITEE_REPO_URL` |
| `EXTERNAL_REPO_TOKEN_LUCI_APP_OPENLIST` | 不需要 | 上游仓库通知 OpenWRT 打包用；本仓库的 workflow 已按仓库名跳过 |

**Variables（明文，全部可选）**

| 名称 | 默认值 | 用途 |
|---|---|---|
| `DOCKERHUB_ORG_NAME` | `shanhaijun` | 镜像命名空间，最终镜像为 `<该值>/openlist-cas` |
| `DOCKERHUB_ORG_NAME_BACKUP` | 空 | 当 DockerHub **登录用户名**与镜像命名空间不同时填写（例如镜像在组织下、用个人账号登录） |
| `FRONTEND_REPO` | `OpenListTeam/OpenList-Frontend` | 前端产物来源仓库，一般不用改 |
| `GITEE_REPO_URL` | 空 | 填了才会执行 Gitee 同步，为空时该工作流自动跳过 |
| `HOOK_REPO` | `OpenListTeam/OpenList-OpenWRT` | 仅在上游仓库生效，本仓库忽略 |

> 只需要镜像能推成功时，配 `DOCKERHUB_TOKEN` 一个 secret 就够了：镜像名与登录用户名默认都取 `shanhaijun`。推出来的镜像是 `shanhaijun/openlist-cas:<tag>`（`beta` 或 `vX.Y.Z-cas.N`），与同账号下的 `mymediahelper` 镜像是两个独立仓库。

### 版本号

`build.sh` 从最近的 git tag 推导版本，已是 `vX.Y.Z-cas.N` 时原样使用；从上游 tag（如 `v4.2.9`）出发时自动追加 `-cas.1`。发版打 tag 即可：

```bash
git tag v4.2.9-cas.3
git push origin v4.2.9-cas.3
```

## 文档

- 本分支的 CAS 设计说明：[docs/CAS.md](./docs/CAS.md)
- 合并前的实现整理：[docs/cloud189-cas-implementation.md](./docs/cloud189-cas-implementation.md)、[docs/cloud189-cas-method-map.md](./docs/cloud189-cas-method-map.md)
- 上游文档：<https://doc.oplist.org> ｜ 国内镜像：<https://doc.oplist.org.cn>
- 英文说明（上游）：[OpenListTeam/OpenList](https://github.com/OpenListTeam/OpenList)

## 免责声明

- 本项目是免费的开源软件，用于通过网盘进行文件共享，主要目的是学习 Go 语言。
- 请遵守所在地区的法律法规，禁止任何形式的滥用。
- 软件基于官方 SDK / API 实现，不修改、不干扰其行为；只做 HTTP 302 跳转或流量转发，不拦截、不存储、不篡改用户数据。
- 本项目与天翼云盘及任何官方平台、服务商均无关联，属于非官方分支。
- 软件按「现状」提供，不附带任何明示或暗示的担保。
- 使用本软件产生的风险（包括但不限于账号封禁、下载限速）由使用者自行承担。
- 本项目基于 [AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.txt) 许可，详见 [LICENSE](./LICENSE)。

## 致谢

感谢 [Xhofe](https://github.com/Xhofe) 与 [AlistGo/alist](https://github.com/AlistGo/alist) 的原始实现，感谢 [OpenListTeam](https://github.com/OpenListTeam) 维护的上游项目，以及所有为本项目提交代码、反馈问题的贡献者。

[![Contributors](https://contrib.rocks/image?repo=OpenListTeam/OpenList)](https://github.com/OpenListTeam/OpenList/graphs/contributors)
