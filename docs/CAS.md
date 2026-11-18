# CAS 双载体说明（`.cas` 与 `.cas.torrent`）

本分支在上游 OpenList 的基础上，同时保留两种 CAS 载体：

| 载体 | 产出者 | 用途 |
|---|---|---|
| `<name>.cas` | `cas_mode=sidecar\|both` | 极小的元数据文件（`base64(json)`），保存 `name/size/md5/sliceMd5/create_time`，可据此秒传恢复原文件 |
| `<name>.cas.torrent` | `cas_mode=torrent\|both`（或旧开关 `generate_torrent`） | 标准 torrent 文件 + CAS 扩展信息，可交给上游的 seed 解析/秒传流程 |

两者格式同构：`.cas` 就是 torrent 里 CAS 载荷那段 JSON 的 base64 形式，字段名逐字一致。
因此双向兼容：

- 旧版 OpenList-CAS 生成的 `.cas` → 本分支 `casmeta.Decode` / 上游 `torrent.DecodeCAS` 均可读；
- 本分支/上游生成的带扩展字段（`partsMd5`/`slice_size`/`cloud`/`files`）的 `.cas` → 旧版解码器会忽略扩展字段，仍能恢复。

## 关键实现约束

1. **分片大小不是固定 10MiB**。天翼按文件大小决定：≤9.76GiB 用 10MiB，更大用 20MiB 或继续上浮（`pkg/torrent.CASSliceSize`）。
   `.cas` 里的 `sliceMd5` 必须按同一规则计算，否则秒传永远失败。统一入口：`casmeta.SliceSizeFor(size)`。
2. **上传只读一遍**。`StreamUpload` / `fastUpload` 里已有的整文件 MD5、分片 MD5、SHA-1 piece 在同一次 `io.MultiWriter` 中被复用，
   `.cas` 侧车不额外扫描文件。
3. **侧车不生成侧车**。`.cas` / `.cas.torrent` 自身永远不再产出侧车，避免 `x.cas.cas` 递归。
4. **`delete_source` 依赖回收站**。天翼云删除是移入回收站，数据块仍在，秒传才能命中；清空回收站会让对应 `.cas` 永久失效。
   Local 存储的 `delete_source` 走 `conf.RemovePermanentlyKey`，是真正的删除。

## 配置项（189CloudPC）

- `cas_mode`：`auto` / `off` / `sidecar` / `torrent` / `both`（默认 `auto`，即沿用 `generate_torrent` 旧行为）
- `delete_source`：生成 `.cas` 后删除源文件并清回收站
- `restore_source_from_cas`：上传 `.cas` 时直接恢复源文件
- `delete_cas_after_restore`：恢复后删除 `.cas`
- `auto_restore_existing_cas` + `auto_restore_existing_cas_paths`：按目录定时/手动刷新时自动恢复
- `cas_ext_allowlist`：参与 CAS 的扩展名白名单（与 Local 共用）
- `cas_download_restore`：`/d/*` 直接下载 `.cas` 时返回恢复后的真实文件

Local 存储只有 `generate_cas` 与 `delete_source`。

## 与旧文档的差异

`cloud189-cas-implementation.md`、`cloud189-cas-method-map.md` 描述的是合并前的实现，仍可用于理解 `.cas` 与天翼接口；
其中提到的 `ComputeSliceMD5sFromReader` 已经删除——它与上游 `drivers/189pc/torrent.go` 中的同名函数冲突，
现在统一使用上游实现（按 `torrent.CASSliceSize` 切片）。
