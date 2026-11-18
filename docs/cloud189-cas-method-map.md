# OpenList-CAS 方法级整理：`.cas` 与天翼云盘

本文是给“异项目 Python 复刻”准备的。

目标不是讲概念，而是讲：

- 哪个方法负责什么
- 方法内部调用了哪些下游方法
- 最终命中了哪些天翼接口
- Python 复刻时应拆成哪些函数

## 1. 建议你在 Python 项目里的模块拆分

建议直接拆成这几层：

### 1.1 `casmeta.py`

负责：

- 判断是否 `.cas`
- 生成 `.cas` 文件名
- 计算 `md5 / slice_md5`
- 编码 `.cas`
- 解码 `.cas`
- 推导恢复文件名

### 1.2 `cloud189_auth.py`

负责：

- 密码登录
- 二维码登录
- 刷新 refresh token
- 用 access token 换 session

### 1.3 `cloud189_sign.py`

负责：

- HMAC-SHA1 签名
- AES-ECB 参数加密
- 公共请求头生成

### 1.4 `cloud189_client.py`

负责：

- 统一 GET/POST/PUT 请求
- 个人云/家庭云接口封装
- 文件列表
- 创建目录
- 下载链接
- 秒传恢复
- 普通上传

### 1.5 `cloud189_cas_service.py`

负责：

- 上传后生成 `.cas`
- 从 `.cas` 恢复
- `.cas` 临时播放
- 自动恢复
- `TEMP` 清理

## 2. `.cas` 方法映射

源码主文件：

- `internal/casmeta/casmeta.go`
- `drivers/189pc/cas_payload.go`
- `drivers/189pc/cas_restore.go`
- `drivers/189pc/cas_preview.go`

---

## 3. `casmeta` 层方法

## 3.1 `IsName(name string) bool`

来源：

- `internal/casmeta/casmeta.go`

作用：

- 判断文件名是否以 `.cas` 结尾

逻辑：

```text
lower(name).endswith(".cas")
```

Python 版建议：

```python
def is_cas_name(name: str) -> bool:
    return name.lower().endswith(".cas")
```

---

## 3.2 `FileName(sourceName string) string`

作用：

- 把普通文件名转成 `.cas` 文件名

逻辑：

```text
source_name + ".cas"
```

Python 版建议：

```python
def cas_file_name(source_name: str) -> str:
    return f"{source_name}.cas"
```

---

## 3.3 `NormalizeExtAllowlist(allowlist string) string`

作用：

- 规范化扩展名白名单

逻辑：

1. 按 `, ; 换行 回车 tab 空格` 拆分
2. 去掉前导 `.`
3. 全转小写
4. 去重
5. 如果出现 `*`，直接返回 `*`

Python 复刻建议：

```python
import re

def normalize_ext_allowlist(allowlist: str) -> str:
    parts = re.split(r"[,;\n\r\t ]+", allowlist or "")
    seen = set()
    result = []
    for part in parts:
        ext = part.strip().lower().lstrip(".")
        if not ext:
            continue
        if ext == "*":
            return "*"
        if ext not in seen:
            seen.add(ext)
            result.append(ext)
    return ",".join(result)
```

---

## 3.4 `ExtAllowed(name string, allowlist string) bool`

作用：

- 判断某文件扩展名是否允许走 `.cas`

逻辑：

1. 白名单为空或 `*` 时直接允许
2. 取文件最后一个扩展名
3. 扩展名为空则拒绝
4. 与白名单比较

注意：

- 只看最后一个扩展名
- 大小写不敏感

---

## 3.5 `Encode(info *Info) ([]byte, error)`

作用：

- 把 `Info` 编成 `.cas` 文件内容

输入：

- `Info{Name, Size, MD5, SliceMD5}`

逻辑：

1. `SliceMD5` 为空时回退成 `MD5`
2. 组装 `Payload`
3. `Payload -> JSON`
4. `JSON -> Base64`
5. 输出 Base64 文本字节

Python 复刻建议：

```python
import base64
import json
import time

def encode_cas(info: dict) -> bytes:
    slice_md5 = info.get("slice_md5") or info["md5"]
    payload = {
        "name": info["name"],
        "size": int(info["size"]),
        "md5": info["md5"],
        "sliceMd5": slice_md5,
        "create_time": str(int(time.time())),
    }
    raw = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    return base64.b64encode(raw)
```

---

## 3.6 `Decode(data []byte) (*Info, error)`

作用：

- 把 `.cas` 文件内容解析回 `Info`

逻辑：

1. trim 空白
2. Base64 解码
3. JSON 解析
4. 校验：
   - `name` 不为空
   - `size >= 0`
   - `md5` 不为空
5. `sliceMd5` 为空时回退成 `md5`

Python 复刻建议：

```python
def decode_cas(data: bytes) -> dict:
    payload = json.loads(base64.b64decode(data.strip()).decode("utf-8"))
    if not payload.get("name") or payload.get("size", -1) < 0 or not payload.get("md5"):
        raise ValueError("invalid cas payload")
    return {
        "name": payload["name"],
        "size": int(payload["size"]),
        "md5": payload["md5"],
        "slice_md5": payload.get("sliceMd5") or payload["md5"],
    }
```

---

## 3.7 `DeriveRestoreName(casName, originalName string) string`

作用：

- 根据当前 `.cas` 文件名和原始文件名，推导恢复后的目标文件名

逻辑：

1. 去掉 `casName` 最后一个扩展名 `.cas`
2. 再去掉一次扩展名
3. 取 `originalName` 的扩展名
4. 拼接 `baseName + originalExt`

例子：

```text
abc.mp4.cas + movie.mkv -> abc.mkv
test.cas + movie.mkv -> test.mkv
```

---

## 3.8 `ResolveRestoreName(casName string, info *Info) (string, error)`

作用：

- 真正用于恢复前的目标名确认

逻辑：

1. 必须确认当前文件名是 `.cas`
2. 调 `DeriveRestoreName`
3. 检查结果不为空
4. 结果里不能包含路径分隔符

这一步很重要，因为项目不允许 `.cas` 把文件恢复到别的路径。

---

## 3.9 `HasherWriter`

作用：

- 边上传边计算：
  - 原文件整体 MD5
  - `sliceMd5`

### 关键方法：`Write(p []byte)`

逻辑：

1. 数据按 `slice_size` 切片
2. 每片写入：
   - 全文件 md5
   - 当前分片 md5
3. 当前分片满了就 `finishSlice()`

### 关键方法：`finishSlice()`

逻辑：

1. 取当前分片 md5
2. 转成大写十六进制
3. 放进 `sliceMD5Hexs`
4. 重置当前分片 md5

### 关键方法：`Info(name string) *Info`

逻辑：

- 小文件：
  - `slice_md5 = file_md5`
- 大文件：
  - `slice_md5 = md5("\n".join(slice_md5_hexs))`

Python 复刻建议：

- 单独写一个 `CASHasher`
- 暴露：
  - `update(bytes)`
  - `build_info(file_name)`

## 4. `189pc` 配置字段和作用

来源：

- `drivers/189pc/meta.go`

重要字段：

- `LoginType`
  - `password` / `qrcode`
- `Username`
- `Password`
- `RefreshToken`
- `Type`
  - `personal` / `family`
- `FamilyID`
- `UploadMethod`
  - `stream` / `rapid` / `old`
- `FamilyTransfer`
- `RapidUpload`
- `GenerateCAS`
- `DeleteSource`
- `RestoreSourceFromCAS`
- `DeleteCASAfterRestore`
- `AutoRestoreExistingCAS`
- `AutoRestoreExistingCASPaths`
- `CASExtAllowlist`

Python 复刻建议：

- 定义一个配置 dataclass 或 pydantic model
- 这几个字段保留原名最方便迁移

## 5. `189pc` 认证方法映射

源码主文件：

- `drivers/189pc/utils.go`
- `drivers/189pc/help.go`

---

## 5.1 `Init(ctx)` 

来源：

- `drivers/189pc/driver.go`

作用：

- 驱动初始化总入口

内部逻辑：

1. 设置 `.cas` 全局扩展名白名单
2. 处理根目录参数
3. 校验上传线程数
4. 初始化 HTTP client
5. 如果有 `RefreshToken`
   - 先 `refreshToken()`
6. 否则
   - `login()`
7. 启动定时 `keepAlive`
8. 若未设置 `FamilyID`
   - `getFamilyID()`
9. 如果开启 `FamilyTransfer`
   - 创建/确保 `FamilyTransferFolder`
10. 初始化自动恢复 cron

Python 版建议拆法：

```python
async def init_client(self):
    self.set_cas_allowlist()
    self.normalize_root()
    self.init_http_client()
    if self.refresh_token:
        await self.refresh_token_login()
    else:
        await self.login()
    await self.ensure_family_id_if_needed()
    await self.ensure_family_transfer_folder_if_needed()
    self.start_keepalive_scheduler()
    self.start_auto_restore_scheduler_if_needed()
```

---

## 5.2 `login()`

作用：

- 根据 `LoginType` 分发登录方式

逻辑：

- `qrcode` -> `loginByQRCode()`
- 其他 -> `loginByPassword()`

---

## 5.3 `initBaseParams()`

作用：

- 初始化登录公共参数

接口：

```text
GET https://cloud.189.cn/api/portal/unifyLoginForPC.action
```

请求参数：

- `appId`
- `clientType`
- `returnURL`
- `timeStamp`

从 HTML 中提取：

- `captchaToken`
- `lt`
- `paramId`
- `reqId`

Python 复刻建议：

- 请求返回是 HTML
- 用正则提取即可

---

## 5.4 `initLoginParam()`

作用：

- 准备密码登录参数

内部逻辑：

1. 调 `initBaseParams()`
2. 调 `encryptConf.do` 获取 RSA 公钥
3. RSA 加密用户名密码
4. 调 `needcaptcha.do` 判断是否需要验证码
5. 如果需要：
   - 拉取 `picCaptcha.do`
   - 可选 OCR

### 下游接口 1

```text
POST https://open.e.189.cn/api/logbox/config/encryptConf.do
```

表单参数：

- `appId`

返回关注字段：

- `data.pubKey`
- `data.pre`

### 下游接口 2

```text
POST https://open.e.189.cn/api/logbox/oauth2/needcaptcha.do
```

表单参数：

- `appKey`
- `accountType`
- `userName`

### 下游接口 3

```text
GET https://open.e.189.cn/api/logbox/oauth2/picCaptcha.do
```

查询参数：

- `token`
- `REQID`
- `rnd`

Python 复刻建议：

```python
async def init_login_param(self):
    base = await self.init_base_params()
    encrypt_conf = await self.post_encrypt_conf()
    rsa_username = encrypt_conf["pre"] + rsa_encrypt(encrypt_conf["pubKey"], self.username)
    rsa_password = encrypt_conf["pre"] + rsa_encrypt(encrypt_conf["pubKey"], self.password)
    need_captcha = await self.check_need_captcha(rsa_username, base.req_id)
    if need_captcha:
        captcha_bytes = await self.fetch_captcha(base.captcha_token, base.req_id)
        code = await self.solve_or_raise(captcha_bytes)
    return LoginParam(...)
```

---

## 5.5 `loginByPassword()`

作用：

- 走账号密码登录并拿到 token/session

内部逻辑：

1. 若 `loginParam` 为空，先 `initLoginParam()`
2. 提交 `loginSubmit.do`
3. 获取 `toUrl`
4. 再调 `getSessionForPC.action`
5. 保存：
   - `RefreshToken`
   - `tokenInfo`

### 下游接口 1

```text
POST https://open.e.189.cn/api/logbox/oauth2/loginSubmit.do
```

请求头：

- `REQID`
- `lt`

表单参数：

- `appKey`
- `accountType`
- `userName`
- `password`
- `validateCode`
- `captchaToken`
- `returnUrl`
- `dynamicCheck=FALSE`
- `clientType`
- `cb_SaveName=1`
- `isOauth2=false`
- `state`
- `paramId`

返回关键字段：

- `toUrl`

### 下游接口 2

```text
POST https://api.cloud.189.cn/getSessionForPC.action
```

请求参数：

- 客户端公共参数 `clientSuffix`
- `redirectURL=toUrl`

返回关键字段：

- `accessToken`
- `refreshToken`
- `sessionKey`
- `sessionSecret`
- `familySessionKey`
- `familySessionSecret`

Python 复刻建议：

```python
async def login_by_password(self):
    param = await self.init_login_param_if_needed()
    login_resp = await self.post_login_submit(param)
    to_url = login_resp["toUrl"]
    token_info = await self.post_get_session_for_pc(redirect_url=to_url)
    self.token_info = token_info
    self.refresh_token = token_info["refreshToken"]
```

---

## 5.6 `initQRCodeParam()`

作用：

- 初始化二维码登录参数

内部逻辑：

1. 调 `initBaseParams()`
2. 调 `getUUID.do`
3. 记录：
   - `uuid`
   - `encryuuid`
   - 以及基础登录参数

接口：

```text
POST https://open.e.189.cn/api/logbox/oauth2/getUUID.do
```

表单参数：

- `appId`

---

## 5.7 `loginByQRCode()`

作用：

- 检查二维码登录状态，并在成功后换 session

内部逻辑：

1. 如果还没有二维码参数，先 `initQRCodeParam()`
2. 调 `qrcodeLoginState.do`
3. 根据状态分支：
   - `0`: 登录成功
   - `-106`: 等待扫描
   - `-11002`: 等待手机确认
   - `-11001`: 二维码过期
4. 登录成功后再调 `getSessionForPC.action`

### 下游接口 1

```text
POST https://open.e.189.cn/api/logbox/oauth2/qrcodeLoginState.do
```

请求头：

- `Referer`
- `Reqid`
- `lt`

表单参数：

- `appId`
- `clientType`
- `returnUrl`
- `paramId`
- `uuid`
- `encryuuid`
- `date`
- `timeStamp`

### 下游接口 2

```text
POST https://api.cloud.189.cn/getSessionForPC.action
```

参数：

- `redirectURL`

Python 复刻建议：

- 二维码登录可以拆成三步：
  1. `start_qrcode_login()`
  2. `poll_qrcode_login()`
  3. `finalize_qrcode_login()`

---

## 5.8 `refreshToken() / refreshTokenWithRetry()`

作用：

- 用 `refreshToken` 刷新 `accessToken`
- 再刷新 `sessionKey`

接口：

```text
POST https://open.e.189.cn/api/oauth2/refreshToken.do
```

表单参数：

- `clientId`
- `refreshToken`
- `grantType=refresh_token`
- `format=json`

成功后：

1. 更新 `tokenInfo`
2. 更新保存的 `RefreshToken`
3. 再调 `refreshSessionWithRetry()`

失败后逻辑：

- 清空持久化 `RefreshToken`
- 如果是二维码登录：
  - 直接报需要重新扫码
- 如果是密码登录：
  - 回退到完整 `login()`

Python 复刻建议：

```python
async def refresh_token_login(self):
    token_info = await self.post_refresh_token(self.refresh_token)
    self.refresh_token = token_info["refreshToken"]
    self.token_info = token_info
    await self.refresh_session()
```

---

## 5.9 `refreshSession() / refreshSessionWithRetry()`

作用：

- 用现有 `accessToken` 获取最新 `sessionKey/sessionSecret`

接口：

```text
GET https://api.cloud.189.cn/getSessionForPC.action
```

参数：

- `appId`
- `accessToken`
- `clientSuffix`

失败处理：

- 如果返回 `UserInvalidOpenToken`
  - 转去 `refreshTokenWithRetry()`

---

## 5.10 `keepAlive()`

作用：

- 维持当前 session 存活

接口：

```text
GET https://api.cloud.189.cn/keepUserSession.action
```

失败时：

- 自动尝试 `refreshSession()`

Python 复刻建议：

- 用定时任务每 5 分钟跑一次

## 6. `189pc` 签名与请求方法映射

---

## 6.1 `SignatureHeader(url, method, params, isFamily)`

作用：

- 为业务接口构造签名头

输出头：

- `Date`
- `SessionKey`
- `X-Request-ID`
- `Signature`

### 签名规则

调用 `signatureOfHmac(...)`

签名原文：

```text
SessionKey=<sessionKey>&Operate=<METHOD>&RequestURI=<path>&Date=<date>[&params=<encrypted_params>]
```

算法：

```text
HMAC-SHA1(secret=sessionSecret, plaintext)
```

结果：

- 大写十六进制字符串

---

## 6.2 `EncryptParams(params, isFamily)`

作用：

- 加密业务参数串

逻辑：

1. 参数编码成字符串
2. 取 `sessionSecret[:16]`
3. AES-ECB 加密
4. 大写十六进制输出

注意：

- 家庭云用 `FamilySessionSecret`
- 个人云用 `SessionSecret`

---

## 6.3 `request(url, method, callback, params, resp, isFamily...)`

作用：

- `189pc` 所有签名接口的统一入口

逻辑：

1. `SetQueryParams(clientSuffix())`
2. 如果 `params != nil`
   - `EncryptParams`
   - 放到查询参数 `params`
3. 设置 `SignatureHeader`
4. 发请求
5. 如果响应里含：
   - `userSessionBO is null`
   - `InvalidSessionKey`
   则自动 `refreshSession()` 后重试
6. 如果返回业务错误对象 `RespErr`
   - 直接抛错

Python 复刻建议：

```python
async def signed_request(self, method, url, params=None, is_family=False, **kwargs):
    client_params = self.client_suffix()
    encrypted = self.encrypt_params(params, is_family) if params else None
    if encrypted:
        client_params["params"] = encrypted
    headers = self.signature_headers(url, method, encrypted or "", is_family)
    resp = await self.http.request(method, url, params=client_params, headers=headers, **kwargs)
    text = resp.text
    if "userSessionBO is null" in text or "InvalidSessionKey" in text:
        await self.refresh_session()
        return await self.signed_request(method, url, params, is_family, **kwargs)
    self.raise_if_business_error(resp)
    return resp
```

---

## 6.4 `get(...) / post(...) / put(...)`

作用：

- 对 `request(...)` 的轻封装

其中 `put(...)` 单独走标准 HTTP PUT，用于上传分片。

## 7. 文件与目录方法映射

---

## 7.1 `getFilesWithPage(...)`

作用：

- 分页拉取目录内容

接口：

- 个人云：
  - `GET https://api.cloud.189.cn/listFiles.action`
- 家庭云：
  - `GET https://api.cloud.189.cn/family/file/listFiles.action`

个人云参数：

- `folderId`
- `fileType=0`
- `mediaAttr=0`
- `iconOption=5`
- `pageNum`
- `pageSize`
- `recursive=0`
- `orderBy`
- `descending`

家庭云额外/差异参数：

- `familyId`
- `orderBy` 需要转成数字枚举
- `descending` 用 `"true"/"false"`

---

## 7.2 `getFiles(...)`

作用：

- 循环分页，把整个目录全拉出来

内部调用：

- `getFilesWithPage(...)`

Python 版建议：

- 写成 async generator 或 while 循环分页

---

## 7.3 `findFileByName(...)`

作用：

- 在目录里按文件名找文件

内部逻辑：

- 每次按 10 条分页扫
- 找到即返回

---

## 7.4 `findFolderByName(...)`

作用：

- 在目录里按名字找子目录

逻辑和 `findFileByName(...)` 类似，只是遍历 `FolderList`

---

## 7.5 `ensureTempDir(...)`

作用：

- 确保存在一个名叫 `TEMP` 的目录，用于 `.cas` 临时播放恢复

接口：

- 个人云：
  - `POST https://api.cloud.189.cn/createFolder.action`
- 家庭云：
  - `POST https://api.cloud.189.cn/family/file/createFolder.action`

个人云参数：

- `parentFolderId`

家庭云参数：

- `familyId`
- `parentId`

---

## 7.6 `ensureFamilyTransferFolder(...)`

作用：

- 确保家庭云中存在 `FamilyTransferFolder`

内部调用：

- `findFamilyTransferFolder(...)`
- `createFamilyTransferFolder()`

## 8. `.cas` 上传与恢复方法映射

---

## 8.1 `shouldUploadCAS(name)`

作用：

- 判断当前文件上传后是否要生成 `.cas`

条件：

1. `GenerateCAS == true`
2. 当前文件名不是 `.cas`
3. 扩展名在 `CASExtAllowlist` 内

---

## 8.2 `uploadCAS(ctx, dstDir, info)`

作用：

- 根据 `Info` 生成 `.cas` 文件并上传

内部逻辑：

1. `casmeta.Encode(info)`
2. 构造一个文本流
3. 调 `uploadFile(...)` 再上传一次

注意：

- `.cas` 文件本身也是存到云盘里的普通文件
- MIME 类型写成了 `text/plain`

Python 复刻建议：

```python
async def upload_cas(self, parent_dir_id, info):
    data = encode_cas(info)
    cas_name = cas_file_name(info["name"])
    return await self.upload_bytes(parent_dir_id, cas_name, data, mimetype="text/plain")
```

---

## 8.3 `deleteSource(ctx, dstDir, uploadedObj, info)`

作用：

- 在 `.cas` 上传成功后删除源文件

内部逻辑：

1. 如果没有传 `uploadedObj`
   - 就按文件名重新查
2. 调 `Delete(...)`

---

## 8.4 `parseCAS(data)` / `parseCASFromStreamer(...)` / `parseCASFromObj(...)`

作用：

- 分别从：
  - 字节
  - 流
  - 云盘对象
  三种来源解析 `.cas`

`parseCASFromObj(...)` 的逻辑：

1. 先 `Link(..., Type="raw_cas")`
2. 用返回 URL 下载 `.cas`
3. `casmeta.Decode(...)`

---

## 8.5 `resolveCASRestoreName(...)`

作用：

- 恢复前最终得到目标文件名

内部调用：

- `casmeta.ResolveRestoreName(...)`

---

## 8.6 `restoreCAS(ctx, dstDir, info, casName, temp)`

作用：

- `.cas` 恢复总入口

逻辑：

1. 解析恢复名
2. 检查扩展名白名单
3. `temp=True` 时改成 `TEMP_...` 文件名
4. 如果目标文件已存在且不是临时恢复
   - 直接返回现有文件
5. 根据模式分支：
   - 个人云直接恢复
   - 个人云 + `FamilyTransfer`
     - 先恢复到家庭云
     - 再从家庭云转存到个人云
   - 临时预览且开启 `FamilyTransfer`
     - 走家庭云 `TEMP` 恢复

这是 `.cas` 恢复的调度方法。

---

## 8.7 `restoreCASDirect(ctx, dstDir, info, isFamily, overwrite)`

作用：

- 真正调用天翼“秒传恢复”接口的核心方法

### 下游接口 1：`initMultiUpload`

个人云：

```text
GET https://upload.cloud.189.cn/person/initMultiUpload
```

家庭云：

```text
GET https://upload.cloud.189.cn/family/initMultiUpload
```

参数：

- `parentFolderId`
- `fileName`
- `fileSize`
- `fileMd5`
- `sliceSize`
- `sliceMd5`
- 家庭云还要 `familyId`

关键返回：

- `data.uploadFileId`
- `data.fileDataExists`

### 下游接口 2：`commitMultiUploadFile`

个人云：

```text
GET https://upload.cloud.189.cn/person/commitMultiUploadFile
```

家庭云：

```text
GET https://upload.cloud.189.cn/family/commitMultiUploadFile
```

参数：

- `uploadFileId`
- `isLog=0`
- `opertype`

其中：

- `overwrite=True` -> `opertype=3`
- `overwrite=False` -> `opertype=1`

### 关键逻辑

如果：

```text
fileDataExists != 1
```

直接失败：

```text
cas restore failed: source file data does not exist in cloud
```

Python 复刻建议：

```python
async def restore_cas_direct(self, parent_id, info, is_family=False, overwrite=True):
    slice_size = self.part_size(info["size"])
    init_resp = await self.signed_get_init_multi_upload(...)
    if init_resp["data"]["fileDataExists"] != 1:
        raise RuntimeError("cloud source data does not exist")
    upload_file_id = init_resp["data"]["uploadFileId"]
    commit_resp = await self.signed_get_commit_multi_upload_file(
        upload_file_id=upload_file_id,
        opertype="3" if overwrite else "1",
        is_family=is_family,
    )
    return commit_resp
```

## 9. 上传入口方法映射

---

## 9.1 `Put(ctx, dstDir, stream, up)`

作用：

- 对外统一上传入口

逻辑分两条：

### 路径 A：上传的是 `.cas`

如果：

- `RestoreSourceFromCAS == true`
- 文件名是 `.cas`

则：

1. 读完整 `.cas`
2. 解析
3. 推导恢复名
4. 如果扩展名允许
   - 直接 `restoreCAS(...)`
   - 不把 `.cas` 本体存进去
5. 如果扩展名不允许
   - 把 `.cas` 当普通文件上传

### 路径 B：上传的是普通文件

1. `uploadFile(...)`
2. 得到 `newObj` 与 `casInfo`
3. `uploadCAS(...)`
4. 如果开启 `DeleteSource`
   - 删除原文件
   - 返回 `.cas` 对象
5. 否则返回原文件对象

---

## 9.2 `uploadFile(ctx, dstDir, stream, up)`

作用：

- 真正决定怎么上传源文件

逻辑分支：

1. 如果开启 `RapidUpload`
   - 优先 `RapidUpload(...)`
2. 如果 `stream.IsForceStreamUpload()`
   - 强制 `stream`
3. 如果 `UploadMethod == "old"`
   - 走 `OldUpload(...)`
4. 如果是个人云并开启 `FamilyTransfer`
   - 先传家庭云中转
   - 再转存回个人云
5. 否则：
   - `rapid` -> `FastUpload(...)`
   - `stream` -> `StreamUpload(...)`

这个方法是整个上传体系的调度中心。

## 10. `.cas` 播放方法映射

---

## 10.1 `shouldPlayCAS(file, args)`

作用：

- 判断当前请求是否要把 `.cas` 当视频入口处理

条件：

1. 文件是 `.cas`
2. `args.Type == "cas_video"`

---

## 10.2 `CASPreviewName(ctx, file)`

作用：

- 给前端展示一个“恢复后的真实视频名”

逻辑：

1. 解析 `.cas`
2. 推导恢复名
3. 检查扩展名白名单
4. 检查是否视频扩展名

---

## 10.3 `linkCASVideo(ctx, file, args)`

作用：

- `.cas` 临时视频播放总入口

逻辑：

1. 解析 `.cas`
2. 推导恢复名
3. 如果不是允许的视频扩展名
   - 回退成 `raw_cas`
4. `ensureTempDir(...)`
5. `restoreCAS(..., temp=True)`
6. `linkObj(..., Type="raw_video")`
7. 异步删除临时文件

Python 复刻建议：

- 返回视频播放链接前，不要立即删临时文件
- 应该延迟或异步删

---

## 10.4 `linkObj(ctx, file, args, isFamily)`

作用：

- 获取真实文件下载地址

下游接口：

个人云：

```text
GET https://api.cloud.189.cn/getFileDownloadUrl.action
```

家庭云：

```text
GET https://api.cloud.189.cn/family/file/getFileDownloadUrl.action
```

个人云参数：

- `fileId`
- `dt=3`
- `flag=1`

家庭云参数：

- `fileId`
- `familyId`

后处理：

1. 拿到 `fileDownloadUrl`
2. 把 `&amp;` 转回 `&`
3. `http://` 替换成 `https://`
4. 再主动请求一次
5. 如果返回 302
   - 取 `location`

这一步是“取最终可下载链接”。

## 11. 自动恢复方法映射

---

## 11.1 `autoRestoreCAS(ctx)`

作用：

- 周期扫描配置目录，自动恢复 `.cas`

逻辑：

1. 按换行拆 `AutoRestoreExistingCASPaths`
2. 每个路径调用 `getDirByPath(...)`
3. 再调 `restoreCASInDir(...)`

---

## 11.2 `restoreCASInDir(ctx, dir, dirPath)`

作用：

- 递归扫描目录中的 `.cas`

逻辑：

1. `getFiles(...)`
2. 子目录递归
3. 文件若不是 `.cas` 跳过
4. 做 in-flight 去重
5. 解析 `.cas`
6. 推导恢复名
7. 如果目标文件已存在
   - 可选删 `.cas`
8. 如果目标文件不存在
   - 调 `restoreCAS(...)`
   - 可选删 `.cas`

## 12. 清理方法映射

---

## 12.1 `beginCleanupTask()` / `endCleanupTask()`

作用：

- 用计数器保护异步清理时机

意义：

- 防止任务还在运行，临时文件就被清掉

---

## 12.2 `queueFamilyTransferCleanupObj(obj)`

作用：

- 把家庭云中转文件放进待清理队列

---

## 12.3 `cleanTempFolder(ctx)`

作用：

- 删除 `TEMP` 目录中所有 `TEMP_` 开头文件

逻辑：

1. `findFolderByName("TEMP")`
2. `getFiles(...)`
3. 删除非目录且前缀为 `TEMP_` 的对象

---

## 12.4 `deleteFamilyTransferObj(ctx, obj)`

作用：

- 删除家庭云中转文件

逻辑：

1. 如果对象没有 ID，但有名字
   - 重新到 `FamilyTransferFolder` 按名字找
2. 调 `Delete(...)`

## 13. 其他天翼驱动的复刻建议

### 13.1 `drivers/189`

适合参考的点：

- 老上传协议
- `generateRsaKey.action`
- `sessionKey` 获取方式

不建议作为主迁移版本，因为：

- 没有 `.cas` 体系
- 逻辑较旧

### 13.2 `drivers/189_tv`

适合参考的点：

- 电视端二维码登录
- `AppKey + AppSignature` 方案

主要方法：

- `AppKeySignatureHeader(...)`
- `login()`
- `refreshSession()`
- `requestWithRetry(...)`

如果你的 Python 项目只做 PC/网页云盘，不建议优先迁它。

## 14. Python 复刻最小方法清单

如果让我在另一个 Python 项目复刻，最少我会先实现这些方法：

### 认证层

- `init_base_params()`
- `init_login_param()`
- `login_by_password()`
- `start_qrcode_login()`
- `poll_qrcode_login()`
- `refresh_token_login()`
- `refresh_session()`
- `keep_alive()`

### 签名层

- `signature_of_hmac()`
- `aes_ecb_encrypt()`
- `encrypt_params()`
- `signature_headers()`
- `signed_request()`

### 云盘层

- `list_files()`
- `find_file_by_name()`
- `find_folder_by_name()`
- `create_folder()`
- `get_download_url()`
- `init_multi_upload()`
- `commit_multi_upload_file()`
- `stream_upload()`
- `rapid_upload()`

### CAS 层

- `is_cas_name()`
- `encode_cas()`
- `decode_cas()`
- `resolve_restore_name()`
- `build_cas_info_from_stream()`
- `upload_cas()`
- `restore_cas_direct()`
- `restore_cas()`
- `preview_cas_video()`
- `auto_restore_cas()`

## 15. 复刻优先顺序建议

最稳的顺序是：

1. 先复刻 `189pc` 的认证和签名
2. 跑通 `listFiles` / `createFolder` / `getFileDownloadUrl`
3. 跑通 `initMultiUpload + commitMultiUploadFile`
4. 复刻 `casmeta`
5. 跑通“上传后生成 `.cas`”
6. 跑通“上传 `.cas` 后恢复”
7. 最后再加：
   - 自动恢复
   - 临时播放
   - FamilyTransfer

如果你后面要我直接在 Python 项目复刻，我就会按这个方法表一层层落代码。
