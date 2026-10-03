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

## 应用元数据

应用级信息（名称、描述、图标等）存在 `apps/{app}/app.json`，对所有版本生效。
这三个接口让管理员不必手工编辑文件，网页端 <http://localhost:8080/upload>
的「应用信息」面板用的就是它们。

**鉴权与上传接口相同**：由 `UPLOAD_SECRET` 推导的应用令牌，
放在 `Authorization: Bearer <令牌>` 或 `X-Upload-Token: <令牌>` 里。
站点未配置 `UPLOAD_SECRET` 时，这三个接口统一返回 `403`。

### `GET /api/v1/apps/{app}/metadata`

返回**原样的 `app.json`**，而不是 `GET /api/v1/apps/{app}` 那种合并视图 ——
后者会把从安装包推断出的 `platforms` 一并返回，存回去等于把推断结果固化。

文件不存在时返回 `200` 和一个空的 `metadata` 对象，便于表单直接渲染。

```json
{
  "app": "demo-desktop",
  "metadata": {
    "id": "demo-desktop",
    "name": "演示桌面客户端",
    "summary": "用来展示发布目录结构的示例应用",
    "description": "这是 **示例应用**，用于演示更新站点的目录结构与元数据格式。",
    "vendor": "MTI",
    "license": "Proprietary",
    "tags": ["desktop", "demo"],
    "channel": "stable",
    "icon": "icon.svg",
    "order": 10,
    "hidden": false
  },
  "icon": {"file": "icon.svg", "url": "https://…/a/demo-desktop/icon"},
  "iconUrl": "https://…/a/demo-desktop/icon"
}
```

### `PUT /api/v1/apps/{app}/metadata`

请求体是 `application/json`，字段与 `app.json` 一致。**整体替换**，不是增量合并：
没写的字段会被清空。`id` 会被忽略，目录名始终优先。

```bash
curl -fS -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
        "name": "演示桌面客户端",
        "summary": "用来展示发布目录结构的示例应用",
        "description": "这是 **示例应用**，用于演示目录结构与元数据格式。",
        "vendor": "MTI",
        "license": "Proprietary",
        "homepage": "https://example.com/demo",
        "tags": ["desktop", "demo"],
        "channel": "stable",
        "order": 10
      }' \
  https://update.example.com/api/v1/apps/demo-desktop/metadata
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `name` | string | 显示名称，最长 300 字符 |
| `summary` | string | 一句话描述，最长 300 |
| `description` | string | 详细描述，Markdown，最长 20000 |
| `homepage` | string | 必须 http(s)，最长 300 |
| `vendor` | string | 厂商，最长 300 |
| `license` | string | 许可证，最长 300 |
| `channel` | string | 默认通道，最长 300 |
| `tags` | string[] | 最多 30 个，单个最长 60 字符 |
| `platforms` | string[] | 最多 20 个。留空则站点从安装包自动推断 |
| `icon` | string | 图标文件名。由图标接口维护，手工设置需谨慎 |
| `order` | number \| null | 列表排序，小的靠前，默认 100 |
| `hidden` | bool | 为 `true` 时不在应用列表中展示，但下载地址仍然有效 |

成功返回 `200` 和保存后的内容（结构与 `GET` 相同），并触发一次重新扫描。
未知字段会被拒绝（`400 invalid_json`），避免拼错字段静默丢数据。

| 状态码 | 含义 |
| --- | --- |
| `200` | 已保存 |
| `400` | JSON 非法、含未知字段，或字段超限（`save_failed`） |
| `401` | 令牌缺失或错误 |
| `403` | 站点未开启上传功能 |
| `503` | 归档目录不可写 |

### `PUT /api/v1/apps/{app}/icon`

请求体是图标二进制本身，`Content-Type` 可省略（站点按内容判断）。

```bash
curl -fS -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  --data-binary @icon.png \
  https://update.example.com/api/v1/apps/demo-desktop/icon
```

- 接受 PNG、JPEG、SVG、WebP，最大 2 MB
- **类型由文件内容判定，不看扩展名**；保存的文件名也由内容决定
  （`icon.png` / `icon.jpg` / `icon.svg` / `icon.webp`），
  调用方无法指定路径或扩展名
- 写入后同目录下其他已知图标名（`icon.*`、`logo.*`）会被删除，保证只有一个
- 同时把 `app.json` 的 `icon` 字段指向新文件，避免之前配置过的自定义图标名继续生效
- 返回体与 `GET metadata` 相同，`message` 里带上新的文件名

`400` 表示不是可识别的图片（或伪装成 SVG 的 HTML），`413` 表示超过大小上限。

> 图标以 `image/svg+xml` 提供时会带上 `Content-Security-Policy` 与
> `X-Content-Type-Options: nosniff`，SVG 里的脚本不会在本站源下执行。

---

## 删除

两个接口都是**不可恢复**的：删除会直接抹掉归档目录里的文件，没有回收站。
鉴权与上传接口相同，未配置 `UPLOAD_SECRET` 时返回 `403`。

> 网页端 <https://localhost:8443/upload> 的「版本列表」与「危险操作」用的是这两个接口，
> 并且要求**手工输入版本号 / 应用 ID** 才能点确认，避免误点。

### `DELETE /api/v1/apps/{app}/releases/{version}`

删除一个已发布的版本，该版本的安装包与元数据一并移除。同一应用的其他版本、
`app.json` 与图标不受影响。

```bash
curl -fS -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  https://update.example.com/api/v1/apps/demo-desktop/releases/1.0.0
```

`{version}` 必须是归档目录里真实存在的目录名。`latest` 在这里**没有特殊含义** ——
它不是一个目录名，会被拒绝，以免有人以为它指向最新版却删掉了别的东西。

```json
{"ok": true, "app": "demo-desktop", "version": "1.0.0", "message": "版本 1.0.0 已删除"}
```

### `DELETE /api/v1/apps/{app}`

删除整个应用：全部版本、`app.json`、图标。归档目录下的 `apps/{app}/` 会被整目录移除。

```bash
curl -fS -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  https://update.example.com/api/v1/apps/demo-desktop
```

```json
{"ok": true, "app": "demo-desktop", "message": "应用 demo-desktop 及其全部版本已删除"}
```

### 实现方式

删除**先改名再抹除**：目标目录先被移到同级的 `.trash-<随机>` 隐藏目录，
然后再删除。扫描器跳过以 `.` 开头的目录，所以版本对站点是瞬间消失的；
即使 `RemoveAll` 中途失败（Windows 上文件被占用时会发生），也不会留下一个
内容残缺、看起来正常的版本目录。

| 状态码 | 含义 |
| --- | --- |
| `200` | 已删除 |
| `400` | 应用 ID 或版本名非法（`delete_failed`） |
| `401` | 令牌缺失或错误 |
| `403` | 站点未开启上传功能 |
| `404` | 应用或版本不存在 |
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
