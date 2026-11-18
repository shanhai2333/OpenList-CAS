# OpenList-CAS 中 `.cas` 与天翼云盘实现整理

本文基于 `OpenList-CAS` 当前代码实现整理，目标是把下面两部分讲清楚，方便迁移到其他项目：

1. `.cas` 文件本身是什么，如何生成、恢复、临时播放、自动恢复
2. 项目里的天翼云盘接口是怎么封装和调用的，重点是 `189pc`

## 1. 项目里和 `.cas` / 天翼相关的主要文件

### `.cas` 核心

- `internal/casmeta/casmeta.go`
- `drivers/189pc/cas_payload.go`
- `drivers/189pc/cas_restore.go`
- `drivers/189pc/cas_preview.go`
- `drivers/189pc/cas_autorestore.go`
- `drivers/189pc/cas_cleanup.go`
- `drivers/local/cas.go`

### 天翼云盘核心

- `drivers/189pc/driver.go`
- `drivers/189pc/utils.go`
- `drivers/189pc/help.go`
- `drivers/189pc/meta.go`
- `drivers/189pc/types.go`

### 其他天翼驱动

- `drivers/189`
- `drivers/189_tv`

这两个也走天翼接口，但能力和复杂度都低于 `189pc`。`.cas` 功能只接在 `189pc` 和 `local` 上。

## 2. `.cas` 文件是什么

`.cas` 不是原文件，也不是完整备份，而是一个“恢复描述文件”。

它只保存恢复所需的几个关键信息：

- 原始文件名 `name`
- 原始文件大小 `size`
- 原文件完整 MD5 `md5`
- 分片摘要 `sliceMd5`
- 生成时间 `create_time`

对应结构在 `internal/casmeta/casmeta.go`：

- 内部运行结构：`Info`
- 落盘结构：`Payload`

## 3. `.cas` 文件格式

### 3.1 外层格式

`.cas` 文件内容不是纯 JSON，而是：

1. 先把 `Payload` 编成 JSON
2. 再把 JSON 做 Base64 编码
3. 最终文件内容就是一段 Base64 文本

也就是：

```text
.cas 文件内容 = base64(json(payload))
```

### 3.2 JSON 字段

`Payload` 字段如下：

```json
{
  "name": "movie.mkv",
  "size": 123456789,
  "md5": "原文件完整MD5",
  "sliceMd5": "分片摘要MD5",
  "create_time": "时间戳字符串"
}
```

其中：

- `md5` 是整个文件的 MD5
- `sliceMd5` 是天翼秒传/快速恢复需要的分片摘要
- `create_time` 只是记录生成时间，恢复逻辑并不依赖它

## 4. `.cas` 的命名规则

### 4.1 生成

规则非常直接：

```text
原文件名 + ".cas"
```

例如：

```text
movie.mkv -> movie.mkv.cas
```

### 4.2 识别

`casmeta.IsName(name)` 的逻辑就是判断文件名是否以 `.cas` 结尾。

### 4.3 恢复命名

恢复时不是直接使用 `payload.name`，而是“以当前 `.cas` 文件名为主，再保留原始扩展名”。

代码在 `casmeta.ResolveRestoreName` / `DeriveRestoreName`。

逻辑是：

1. 去掉当前文件名最后一个 `.cas`
2. 再去掉剩余名字最后一个扩展名
3. 再拼回 `payload.name` 的扩展名

示例：

```text
abc.mp4.cas + payload.name=movie.mkv -> abc.mkv
test.cas + payload.name=movie.mkv -> test.mkv
```

这么做的意义是：

- 允许你重命名 `.cas` 文件
- 恢复时按当前 `.cas` 文件名落地
- 但扩展名仍以原始源文件为准

## 5. `sliceMd5` 是怎么计算的

代码在 `internal/casmeta/casmeta.go` 的 `HasherWriter`。

### 5.1 默认分片大小

默认切片大小：

```text
10 * 1024 * 1024 = 10 MiB
```

即 `DefaultSliceSize = 10 MiB`。

### 5.2 计算过程

上传原文件时会边写边算：

- `fileMD5`: 整个文件的 MD5
- `sliceMD5`: 当前分片的 MD5

每个分片写满后：

1. 计算该分片 MD5
2. 转成大写十六进制
3. 追加到 `sliceMD5Hexs`

### 5.3 最终 `sliceMd5` 规则

- 如果文件大小 `<= 10 MiB`
  - `sliceMd5 = fileMD5`
- 如果文件大小 `> 10 MiB`
  - 先得到每个分片的 MD5
  - 用换行符 `\n` 把这些分片 MD5 拼接
  - 再对拼接后的字符串做一次 MD5

即：

```text
sliceMd5 = md5(strings.Join(sliceMd5Hexs, "\n"))
```

这个值就是天翼恢复时提交给 `initMultiUpload` 的关键参数之一。

## 6. `.cas` 的生成流程

入口在 `drivers/189pc/driver.go` 的 `Put` 和 `uploadFile`。

### 6.1 正常上传 + 生成 `.cas`

流程：

1. 上传原文件
2. 上传过程中计算 `Info{Name, Size, MD5, SliceMD5}`
3. 调用 `casmeta.Encode(info)` 生成 `.cas` 内容
4. 把 `.cas` 当普通文本文件再次上传到同目录

对应代码：

- `uploadFile(...)`
- `uploadCAS(...)`

### 6.2 删除源文件

如果同时开启：

- `GenerateCAS`
- `DeleteSource`

则在 `.cas` 上传成功后，删除原始源文件，并清理回收站。

这部分逻辑在：

- `shouldDeleteSource()`
- `deleteSource(...)`

### 6.3 `189pc` 的特殊情况：FamilyTransfer

如果当前是个人云，但开启了 `FamilyTransfer`：

1. 原文件先上传到家庭云 `FamilyTransferFolder`
2. 再从家庭云转存回个人云
3. 如果配置是“生成 `.cas` 后删源文件”
   - 项目会只保留 `.cas`
   - 家庭云里的临时源文件随后也会被清理

这套逻辑在：

- `driver.go` 的 `uploadFile(...)`
- `cas_cleanup.go`

## 7. `.cas` 的恢复流程

### 7.1 上传 `.cas` 触发恢复

如果启用了 `RestoreSourceFromCAS`，并且上传的是 `.cas` 文件：

1. 读取 `.cas`
2. `base64 -> json -> Info`
3. 解析恢复目标名
4. 检查扩展名是否在允许列表中
5. 调用 `restoreCAS(...)`

如果扩展名不允许，则把这个 `.cas` 当普通文件上传，不做恢复。

### 7.2 真正恢复的关键点

核心函数是 `restoreCASDirect(...)`。

它并不会上传原始数据，而是利用天翼已有的“秒传/快速上传”机制恢复文件：

1. 调 `initMultiUpload`
2. 传入：
   - `fileName`
   - `fileSize`
   - `fileMd5`
   - `sliceSize`
   - `sliceMd5`
3. 检查返回的 `FileDataExists`
4. 只有 `FileDataExists == 1` 才说明云端还保留这份文件数据
5. 然后调 `commitMultiUploadFile`
6. 天翼直接在目录里生成目标文件

所以本质上：

`.cas` 恢复依赖“云端仍然保留该文件数据块”，不是本地重建。

如果 `FileDataExists != 1`，代码会直接报错：

```text
cas restore failed: source file data does not exist in cloud
```

这也是为什么 `.cas` 不能当真正备份。

## 8. `.cas` 临时播放逻辑

代码在 `drivers/189pc/cas_preview.go`。

### 8.1 触发条件

当访问 `.cas` 且 `LinkArgs.Type == "cas_video"` 时，走特殊播放逻辑。

### 8.2 处理流程

1. 解析 `.cas`
2. 推导恢复后的目标文件名
3. 判断扩展名是否允许
4. 判断是否是视频扩展名
5. 在 `TEMP` 目录里临时恢复一份真实文件
6. 获取真实文件下载链接
7. 返回给播放器
8. 异步删除这个临时文件

### 8.3 `TEMP` 目录

临时恢复目录固定叫：

```text
TEMP
```

临时文件名格式：

```text
TEMP_<时间戳毫秒>_<短uuid>_<原恢复名>
```

例如：

```text
TEMP_1735630000123_ab123_movie.mkv
```

### 8.4 支持的视频扩展名

当前硬编码支持：

- `.mp4`
- `.mkv`
- `.avi`
- `.mov`
- `.webm`
- `.flv`
- `.ts`
- `.m2ts`
- `.wmv`
- `.rmvb`
- `.m4v`
- `.mpg`
- `.mpeg`
- `.3gp`

如果不是这些扩展名，即使是 `.cas`，也不会走视频恢复播放，只返回原 `.cas` 文件链接。

## 9. `.cas` 自动恢复

代码在 `drivers/189pc/cas_autorestore.go`。

### 9.1 开关

- `AutoRestoreExistingCAS`
- `AutoRestoreExistingCASPaths`

### 9.2 行为

系统会周期性扫描配置路径，每行一个目录，递归处理子目录。

对每个 `.cas`：

1. 解析 `.cas`
2. 推导恢复文件名
3. 检查扩展名白名单
4. 如果目标文件已存在
   - 若开启 `DeleteCASAfterRestore`，就删掉 `.cas`
5. 如果目标文件不存在
   - 调 `restoreCAS(...)`
   - 若开启 `DeleteCASAfterRestore`，恢复成功后删掉 `.cas`

### 9.3 并发保护

用 `autoRestoreInFlight` 避免同一路径被重复恢复。

## 10. 清理逻辑

代码在 `drivers/189pc/cas_cleanup.go`。

### 10.1 清理对象

主要清理两类：

- `TEMP` 目录下以 `TEMP_` 开头的临时恢复文件
- `FamilyTransferFolder` 里的家庭云中转文件

### 10.2 触发方式

清理不是每一步立即执行，而是走防抖：

- 有上传/恢复/预览任务时先记账
- 任务结束后延迟 5 秒触发清理

这样避免恢复还没完成，临时文件先被删掉。

## 11. 扩展名白名单

`.cas` 支持按扩展名限制。

相关配置：

- `CASExtAllowlist`

处理规则在 `casmeta.ExtAllowed(...)`：

- 空字符串：允许全部
- `*`：允许全部
- 其他值按逗号/分号/空白/换行拆分
- 大小写不敏感
- 比较时只看最终扩展名

示例：

```text
mp4,mkv,iso,zip
```

## 12. 项目里的天翼驱动概览

### 12.1 `189pc`

这是当前项目里最完整、也是和 `.cas` 结合最深的实现。

特点：

- 支持密码登录
- 支持二维码登录
- 支持 `RefreshToken`
- 支持个人云 / 家庭云
- 支持 FamilyTransfer
- 支持 `.cas` 生成、恢复、自动恢复、视频播放
- 支持快速上传、流式上传、旧上传

### 12.2 `189`

更老、更简单的个人云驱动。

特点：

- 独立登录
- 单独维护 `sessionKey`
- 上传用旧的 RSA/AES 流程
- 不带 `.cas`
- 不带家庭云中转逻辑

### 12.3 `189_tv`

基于电视端/家庭端流程。

特点：

- 用 `AppKey + AppSignature` 走二维码链路
- 拿到 `e189AccessToken`
- 再换成 `SessionKey/SessionSecret`
- 主要偏家庭云

## 13. `189pc` 认证流程

`189pc` 是最值得迁移的版本。

### 13.1 密码登录

完整顺序如下：

1. `GET https://cloud.189.cn/api/portal/unifyLoginForPC.action`
   - 取基础登录参数：
   - `captchaToken`
   - `lt`
   - `paramId`
   - `reqId`

2. `POST https://open.e.189.cn/api/logbox/config/encryptConf.do`
   - 获取 RSA 公钥和前缀 `pre`

3. 用 RSA 加密用户名和密码
   - `RsaUsername = pre + rsa(username)`
   - `RsaPassword = pre + rsa(password)`

4. `POST https://open.e.189.cn/api/logbox/oauth2/needcaptcha.do`
   - 判断是否需要验证码

5. 如果需要验证码：
   - `GET https://open.e.189.cn/api/logbox/oauth2/picCaptcha.do`
   - 可选走 OCR

6. `POST https://open.e.189.cn/api/logbox/oauth2/loginSubmit.do`
   - 提交账号、密码、验证码、`paramId` 等
   - 返回 `toUrl`

7. `POST https://api.cloud.189.cn/getSessionForPC.action`
   - 带 `redirectURL=toUrl`
   - 返回：
     - `accessToken`
     - `refreshToken`
     - `sessionKey`
     - `sessionSecret`
     - `familySessionKey`
     - `familySessionSecret`

### 13.2 二维码登录

完整顺序如下：

1. 同样先 `unifyLoginForPC.action` 拿基础参数
2. `POST https://open.e.189.cn/api/logbox/oauth2/getUUID.do`
   - 获取二维码内容
3. 前端展示二维码
4. `POST https://open.e.189.cn/api/logbox/oauth2/qrcodeLoginState.do`
   - 轮询状态
5. 登录成功后拿到 `redirectUrl`
6. `POST https://api.cloud.189.cn/getSessionForPC.action`
   - 带 `redirectURL=redirectUrl`
7. 返回同样的 token/session 信息

### 13.3 RefreshToken 刷新

接口：

```text
POST https://open.e.189.cn/api/oauth2/refreshToken.do
```

表单参数：

- `clientId`
- `refreshToken`
- `grantType=refresh_token`
- `format=json`

刷新成功后：

1. 更新 `RefreshToken`
2. 更新 `AccessToken`
3. 再调一次 `getSessionForPC.action` 刷新 `sessionKey/sessionSecret`

### 13.4 Session 刷新

接口：

```text
GET https://api.cloud.189.cn/getSessionForPC.action
```

参数：

- `appId`
- `accessToken`
- 还会带客户端公共参数 `clientSuffix()`

什么时候触发：

- 返回里出现 `userSessionBO is null`
- 返回里出现 `InvalidSessionKey`
- `keepAlive` 失败

### 13.5 keepAlive

接口：

```text
GET https://api.cloud.189.cn/keepUserSession.action
```

定时：

- 每 5 分钟一次

作用：

- 尽量延长会话可用时间

## 14. `189pc` 请求签名机制

### 14.1 客户端公共参数

`clientSuffix()` 会给每个请求带上：

- `clientType`
- `version`
- `channelId`
- `rand`

### 14.2 params 加密

普通业务接口不是明文传参数，而是：

1. 先把参数按项目 `Params.Encode()` 规则拼成字符串
2. 使用 `sessionSecret[:16]` 做 AES-ECB 加密
3. 十六进制大写输出
4. 作为查询参数 `params`

个人云用：

- `sessionSecret`

家庭云用：

- `familySessionSecret`

### 14.3 Signature 头

请求头包含：

- `Date`
- `SessionKey`
- `X-Request-ID`
- `Signature`

签名原文：

```text
SessionKey=<sessionKey>&Operate=<METHOD>&RequestURI=<path>&Date=<date>[&params=<encryptedParams>]
```

签名算法：

```text
HMAC-SHA1(sessionSecret, 原文)
```

结果转大写十六进制。

## 15. `189pc` 的主要天翼接口清单

下面只列项目里明确调用到的关键接口。

### 15.1 认证相关

- `GET https://cloud.189.cn/api/portal/unifyLoginForPC.action`
- `POST https://open.e.189.cn/api/logbox/config/encryptConf.do`
- `POST https://open.e.189.cn/api/logbox/oauth2/needcaptcha.do`
- `GET https://open.e.189.cn/api/logbox/oauth2/picCaptcha.do`
- `POST https://open.e.189.cn/api/logbox/oauth2/loginSubmit.do`
- `POST https://open.e.189.cn/api/logbox/oauth2/getUUID.do`
- `POST https://open.e.189.cn/api/logbox/oauth2/qrcodeLoginState.do`
- `POST https://open.e.189.cn/api/oauth2/refreshToken.do`
- `GET/POST https://api.cloud.189.cn/getSessionForPC.action`
- `GET https://api.cloud.189.cn/keepUserSession.action`

### 15.2 文件列表与下载

- `GET https://api.cloud.189.cn/listFiles.action`
- `GET https://api.cloud.189.cn/family/file/listFiles.action`
- `GET https://api.cloud.189.cn/getFileDownloadUrl.action`
- `GET https://api.cloud.189.cn/family/file/getFileDownloadUrl.action`

### 15.3 创建目录 / 重命名

- `POST https://api.cloud.189.cn/createFolder.action`
- `POST https://api.cloud.189.cn/family/file/createFolder.action`
- `POST https://api.cloud.189.cn/renameFile.action`

### 15.4 快速恢复 / 秒传相关

- `GET https://upload.cloud.189.cn/person/initMultiUpload`
- `GET https://upload.cloud.189.cn/person/commitMultiUploadFile`
- 家庭云时同路径改为 `/family/...`

`.cas` 恢复主要依赖这两个。

### 15.5 家庭云转存相关

项目里通过封装方法 `SaveFamilyFileToPersonCloud(...)` 调家庭云转个人云流程。

从代码行为上看，它承担的是：

- 家庭云文件复制到个人云
- `FamilyTransferFolder` 中转

如果你要迁移，建议直接把该方法整段一起搬，不要只抄接口名。

## 16. `189` 驱动的接口特点

`drivers/189` 和 `189pc` 不同点：

- 它会先通过 `getUserBriefInfo.action` 取 `sessionKey`
- 上传时会额外请求 `generateRsaKey.action`
- 上传参数自己做 AES 加密
- 请求头还包含：
  - `EncryptionText`
  - `PkId`
  - `X-Request-Date`

它更像一套旧版上传协议。

如果你不是为了兼容老逻辑，迁移时优先参考 `189pc`。

## 17. `189_tv` 驱动的接口特点

`drivers/189_tv` 主要特点：

1. 先用 `AppKey/AppSignature` 调：
   - `getQrCodeUUID.action`
   - `qrcodeLoginResult.action`
2. 拿到 `e189AccessToken`
3. 再调：
   - `loginFamilyMerge.action`
4. 最终获得：
   - `sessionKey`
   - `sessionSecret`
   - 家庭云 session 信息

它更偏“电视端 / 家庭云登录入口”。

## 18. 迁移建议

如果你要把这套能力迁到另一个项目，建议按下面粒度拆：

### 必搬

- `internal/casmeta/casmeta.go`
- `189pc` 的签名、加密、刷新、请求封装
- `restoreCASDirect(...)`
- `uploadCAS(...)`
- `resolveCASRestoreName(...)`

### 建议一起搬

- `cas_preview.go`
- `cas_autorestore.go`
- `cas_cleanup.go`
- `FamilyTransfer` 整套逻辑

### 可以按需裁剪

- OCR 验证码识别
- 二维码登录
- 自动恢复 cron
- 临时播放

## 19. 迁移时最容易踩的坑

### 19.1 `.cas` 不是备份

恢复成功的前提是：

- 天翼后端仍然保留该文件数据
- `initMultiUpload` 返回 `FileDataExists == 1`

否则 `.cas` 只是一份无效元数据。

### 19.2 恢复名不是直接取 payload.name

恢复名默认按当前 `.cas` 文件名推导，只借用原始扩展名。

### 19.3 扩展名白名单会影响恢复和播放

如果 `CASExtAllowlist` 不包含目标扩展名：

- 不会恢复
- 不会走视频播放

### 19.4 临时播放依赖清理顺序

预览时会恢复 `TEMP_...` 文件，再异步删除。

如果迁移后你把清理逻辑改成“立即删”，播放会直接失效。

### 19.5 FamilyTransfer 不是单纯的复制开关

它和这些能力耦合：

- 个人云上传
- 家庭云中转
- `.cas` 生成后删源
- 临时预览恢复
- 清理 `FamilyTransferFolder`

迁移时建议整体迁，不要拆一半。

## 20. 建议的最小迁移方案

如果你只想在另一个项目里先跑通：

1. 先实现 `189pc` 的密码登录 + RefreshToken
2. 实现统一请求封装：
   - AES-ECB 参数加密
   - HMAC-SHA1 签名
3. 搬 `casmeta`
4. 实现：
   - 上传后生成 `.cas`
   - `.cas` 触发恢复
5. 最后再加：
   - 自动恢复
   - 临时播放
   - FamilyTransfer

这样成本最低，风险也最可控。
