# REST API

所有接口以 `/api/v1` 为前缀，返回 `application/json; charset=utf-8`。
默认开启 CORS（`Access-Control-Allow-Origin: *`，可通过 `CORS_ORIGIN` 收紧），
网页端可以直接跨域调用。

- 加 `?pretty=1` 会返回带缩进的 JSON
- 出错时返回统一结构，并带有对应的 HTTP 状态码：

```json
{"error": {"code": "app_not_found", "message": "no application named \"foo\""}}
```

- `os` / `arch` 查询参数接受规范文档第 5 节列出的全部别名
- 站点会从请求头 `X-Forwarded-Proto` / `X-Forwarded-Host` 还原外部地址；
  也可用 `BASE_URL` 环境变量固定

---

## `GET /api/v1/health`

存活与索引状态，容器健康检查也用它。

```json
{
  "status": "ok",
  "version": "20261003",
  "commit": "abc1234",
  "builtAt": "2026-10-03T02:00:00Z",
  "time": "2026-10-03T02:02:27Z",
  "uptimeSeconds": 6,
  "index": {
    "apps": 2,
    "releases": 3,
    "artifacts": 10,
    "scans": 1,
    "checksumsComputed": 0,
    "lastScan": "2026-10-03T10:02:20+08:00",
    "snapshotBuiltAt": "2026-10-03T10:02:20+08:00",
    "appsDir": "/data/apps",
    "warnings": []
  }
}
```

`index.warnings` 会列出扫描时跳过或忽略的条目，例如版本号不合法的目录、
JSON 写错的 `release.json`。归档目录没生效时先看这里。

---

## `GET /api/v1/apps`

应用列表。默认不含版本明细，加 `?releases=1` 一并返回。

```json
{
  "count": 1,
  "apps": [
    {
      "id": "demo-desktop",
      "name": "演示桌面客户端",
      "summary": "用来展示发布目录结构的示例应用",
      "platforms": ["linux", "windows", "macos"],
      "channel": "stable",
      "channels": ["stable"],
      "iconUrl": "https://update.example.com/a/demo-desktop/icon",
      "latest": {"stable": "1.2.0"},
      "releaseCount": 2,
      "updatedAt": "2026-09-20T09:30:00+08:00",
      "pageUrl": "https://update.example.com/a/demo-desktop",
      "apiUrl": "https://update.example.com/api/v1/apps/demo-desktop"
    }
  ]
}
```

`latest` 是「通道 → 最新版本号」的映射，客户端只需要一个字段时用它。

---

## `GET /api/v1/apps/{app}`

单个应用，含全部版本与产物。`?releases=0` 可以只要应用信息。

---

## `GET /api/v1/apps/{app}/releases`

版本列表，按版本号从新到旧。

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `channel` | 全部 | 只返回指定通道 |
| `prerelease` | `true` | `false` 时过滤掉预发布版 |

---

## `GET /api/v1/apps/{app}/releases/{version}`

单个版本。`{version}` 可以是 `latest`。

---

## `GET /api/v1/apps/{app}/latest`

最新版本，可选按平台筛选。

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `os` | — | `windows` / `linux` / `macos` / `android` / `ios` … |
| `arch` | — | `x64` / `x86` / `arm64` / `arm32` / `universal` … |
| `channel` | 应用默认通道 | 通道名 |
| `prerelease` | `true` | `false` 时跳过预发布版 |

```json
{
  "app": "demo-desktop",
  "channel": "stable",
  "release": { "...": "完整版本对象，字段见下" },
  "download": {
    "file": "DemoDesktop-1.2.0-windows-x64.exe",
    "name": "Windows 64 位安装程序",
    "os": "windows",
    "arch": "x64",
    "kind": "installer",
    "size": 39,
    "sha256": "e0e306249cc37258636bd6793125bb5fb9b549f4dcfcf53535a566cda9e8bb7f",
    "url": "https://update.example.com/dl/demo-desktop/1.2.0/DemoDesktop-1.2.0-windows-x64.exe",
    "path": "/dl/demo-desktop/1.2.0/DemoDesktop-1.2.0-windows-x64.exe",
    "available": true
  },
  "match": {
    "os": "windows",
    "arch": "x64",
    "artifacts": ["...与上面 download 同结构的数组..."]
  }
}
```

- `download` 是**为该平台挑出的最佳产物**：精确匹配优先，其次 `universal`，
  再其次是未标注平台的通用文件；本地文件优先于外链
- `match` 只在你传了 `os` 或 `arch` 时出现，列出该平台的全部候选
- 该平台没有任何产物时 `download` 为 `null`，接口本身仍然返回 `200`
- `available: false` 表示清单里声明了但文件实际不存在

### 版本对象（release）

```json
{
  "app": "demo-desktop",
  "version": "1.2.0",
  "channel": "stable",
  "title": "体验优化版",
  "notes": "## 1.2.0\n\n- 新增…",
  "publishedAt": "2026-09-20T09:30:00+08:00",
  "prerelease": false,
  "mandatory": true,
  "minVersion": "1.0.0",
  "pageUrl": "https://update.example.com/a/demo-desktop/1.2.0",
  "artifacts": [
    {
      "file": "DemoDesktop-1.2.0-windows-x64.exe",
      "name": "Windows 64 位安装程序",
      "os": "windows",
      "arch": "x64",
      "kind": "installer",
      "size": 39,
      "sha256": "e0e3…bb7f",
      "url": "https://…/dl/demo-desktop/1.2.0/DemoDesktop-1.2.0-windows-x64.exe",
      "path": "/dl/demo-desktop/1.2.0/DemoDesktop-1.2.0-windows-x64.exe",
      "available": true,
      "detected": true,
      "modified": "2026-10-03T10:01:56+08:00"
    }
  ]
}
```

- `detected: true` 表示平台/架构是从文件名推断的，`false` 表示由 `release.json` 明确声明
- `notes` 是原始 Markdown 文本

---

## `GET /api/v1/apps/{app}/check`

**自动更新客户端用这个接口。** 传入当前版本，直接得到"要不要更新、更新什么"。

| 参数 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- |
| `version` | 是 | — | 客户端当前版本号，缺省返回 `400` |
| `os` | 否 | — | 客户端平台 |
| `arch` | 否 | — | 客户端架构 |
| `channel` | 否 | 应用默认通道 | 客户端所在通道 |
| `prerelease` | 否 | **`false`** | 是否接受预发布版 |

```json
{
  "app": "demo-desktop",
  "name": "演示桌面客户端",
  "currentVersion": "1.0.0",
  "channel": "stable",
  "os": "windows",
  "arch": "x64",
  "updateAvailable": true,
  "upToDate": false,
  "mandatory": true,
  "latestVersion": "1.2.0",
  "publishedAt": "2026-09-20T09:30:00+08:00",
  "minVersion": "1.0.0",
  "notes": "## 1.2.0\n\n- 新增…",
  "download": {"...": "该平台的最佳产物，结构同 artifact"},
  "artifacts": ["...该平台的全部候选..."],
  "pageUrl": "https://update.example.com/a/demo-desktop"
}
```

判定规则：

- 客户端版本 **不低于** 通道内最新版本 → `upToDate: true`，`updateAvailable: false`，
  不返回 `download`
- 否则 `updateAvailable: true`，并带上 `mandatory` / `minVersion` / `notes`，
  由客户端自行决定是否强更、是否提示全量安装
- 默认 `prerelease=false`：只发预发布版的通道会返回 `upToDate: true`，
  避免自动更新把正式用户带到候选版
- 应用不存在返回 `404`，缺 `version` 参数返回 `400`

### 客户端调用示例

```bash
curl -s "https://update.example.com/api/v1/apps/demo-desktop/check?version=1.0.0&os=windows&arch=x64"
```

```go
// 只取需要的字段，忽略其余
var r struct {
    UpdateAvailable bool   `json:"updateAvailable"`
    LatestVersion   string `json:"latestVersion"`
    Mandatory       bool   `json:"mandatory"`
    Download        *struct {
        URL    string `json:"url"`
        SHA256 string `json:"sha256"`
        Size   int64  `json:"size"`
    } `json:"download"`
}
```

---

## `POST /api/v1/rescan`

立即触发一次扫描，不必等扫描周期。返回 `202`。

设置了 `RESCAN_TOKEN` 环境变量后，必须带令牌：

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" https://update.example.com/api/v1/rescan
```

---

## `POST /api/v1/apps/{app}/upload`

上传一个发布压缩包，解包后归档到 `apps/{app}/{版本号}/`，同名版本整体替换。
详细说明见[上传发布](UPLOAD.md)。

鉴权用**由应用 ID 推导的上传令牌**，
`Authorization: Bearer <令牌>` 或 `X-Upload-Token: <令牌>`：

```bash
curl -fS -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@release.zip" \
  -F "version=1.2.0" \
  https://update.example.com/api/v1/apps/myapp/upload
```

请求体两种形式：

- `multipart/form-data`：含归档的 file 部分，加一个可选的 `version` 文本字段
- 其它 Content-Type：请求体本身就是归档，版本号用 `?version=` 传

支持的格式为 zip、tar、tar.gz、tar.bz2（按文件头魔数识别，不看扩展名）。

成功返回 `201`：

```json
{
  "ok": true,
  "app": "myapp",
  "version": "1.2.0",
  "replaced": false,
  "bytes": 12345678,
  "files": [{"file": "MyApp-1.2.0-windows-x64.exe", "size": 9000000}],
  "pageUrl": "https://update.example.com/a/myapp/1.2.0",
  "apiUrl": "https://update.example.com/api/v1/apps/myapp/releases/1.2.0",
  "message": "release installed, the site is rescanning"
}
```

| 状态码 | 含义 |
| --- | --- |
| `201` | 已发布 |
| `400` | 应用 ID 非法、版本号无法确定、归档损坏或不含可安装文件 |
| `401` | 令牌缺失或错误 |
| `403` | 站点关闭了上传（`UPLOAD_ENABLED=false`） |
| `413` | 超过 `MAX_UPLOAD` |
| `503` | 归档目录不可写 |

---

## `GET /dl/{app}/{version}/{file}`

下载产物。`{version}` 可以是 `latest`。`{file}` 支持相对路径，
也支持只给文件名（大小写不敏感匹配）。

响应头：

| 头 | 说明 |
| --- | --- |
| `Content-Disposition` | `attachment`，文件名同时给出 ASCII 回退与 UTF-8 形式 |
| `Content-Type` | 按扩展名给出准确的安装包类型 |
| `ETag` | `"sha256-<hex>"`，支持 `If-None-Match` 返回 `304` |
| `X-Checksum-Sha256` | 完整校验和，方便下载后直接比对 |
| `Accept-Ranges` | 支持断点续传与分块下载 |

产物配置了外部 `url` 且本地无文件时，返回 `302` 跳转到该地址。
