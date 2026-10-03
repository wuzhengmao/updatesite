# updatesite · 应用更新站点

一个用于多产品、多平台、多架构的**在线更新站点**。把安装包按约定丢进挂载目录
就完成发布，站点自动归档、计算校验和、提供网页浏览与 REST API 查询。

- **发布即拷贝** —— 目录结构本身就是数据库，`cp` 进去就生效，不用登录后台
- **也能上传发布** —— 打包成 zip/tar.gz 通过接口或网页上传，站点自动解包归档，
  令牌由应用 ID 推导，管理员算一次告知开发人员即可
- **多产品多平台** —— 每个应用独立 ID，每个版本按 `os` / `arch` 归档安装包
- **REST API** —— 客户端传当前版本号即可查询是否有新版本、下载哪个包
- **内置文档** —— 发布规范与 API 说明随二进制一起分发，浏览 `/docs` 即可查看
- **单二进制** —— Go 标准库实现，零第三方依赖，静态编译
- **多架构镜像** —— `linux/amd64` 与 `linux/arm64`，基于 `scratch`，镜像约 10 MB

---

## 快速开始

```bash
git clone <repo> && cd mti-update-site
docker compose up -d --build
```

给构建打个版本戳（会显示在页脚和 `/api/v1/health` 里，便于确认线上跑的是哪个镜像）：

```bash
VERSION=1.2.0 COMMIT=$(git rev-parse --short HEAD) docker compose up -d --build
```

打开 <http://localhost:8080>，会看到 `release/apps` 里的两个示例应用。

`release/apps` 就是**归档目录**，也是默认的挂载点。往里面丢
`<应用ID>/<版本号>/` 目录即可发布。里面的示例文件可以直接删掉，
换成自己的内容；生产环境一般把它指到别处，编辑 `docker-compose.yml`：

```yaml
volumes:
  - /srv/updates/apps:/data/apps:ro
```

### 不使用 Docker

```bash
go run ./cmd/updatesite        # 默认读取 /data，Windows 上请设置 DATA_DIR
```

或者直接用仓库里的归档目录：

```bash
DATA_DIR=./release CACHE_DIR=./cache go run ./cmd/updatesite
```

Windows PowerShell：

```powershell
$env:DATA_DIR="./release"; $env:CACHE_DIR="./cache"; go run ./cmd/updatesite
```

---

## 发布一个版本

目录约定（详见 [发布规范](docs/RELEASE-SPEC.md)）：

```
<归档目录>/apps/<应用ID>/<版本号>/
├── release.json     # 可选：通道、标题、强制更新、平台声明
├── CHANGELOG.md     # 可选：更新说明（Markdown）
├── SHA256SUMS       # 可选：校验和清单
├── MyApp-1.2.0-windows-x64.exe
├── MyApp-1.2.0-linux-amd64.tar.gz
└── MyApp-1.2.0-macos-universal.dmg
```

**最简发布**：

```bash
mkdir -p /srv/updates/apps/myapp/1.2.0
cp dist/* /srv/updates/apps/myapp/1.2.0/
```

默认 15 秒内生效，或者立刻通知站点：

```bash
curl -X POST http://localhost:8080/api/v1/rescan
```

**用脚本发布**（自动生成 `SHA256SUMS` 和 `release.json`）：

```bash
./scripts/publish.sh -a myapp -v 1.2.0 \
    -r /srv/updates/apps \
    -n notes/1.2.0.md -t "体验优化版" -m \
    -u https://update.example.com \
    dist/MyApp-1.2.0-*
```

安装包文件名里带上平台和架构（`windows`、`x64`、`linux`、`arm64`…），
站点会自动识别归档；识别不出来也不会出错，只是需要手动用 `release.json` 声明。

**用上传接口发布**：把同样的内容打成 zip 或 tar.gz 上传，站点自动解包归档，
同名版本整体替换。先让管理员生成令牌：

```bash
docker exec updatesite updatesite token myapp   # 令牌只由应用 ID 推导，任何环境都相同
```

开发人员拿到令牌后：

```bash
curl -fS -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@release.zip" -F "version=1.2.0" \
  https://update.example.com/api/v1/apps/myapp/upload
```

网页端 <http://localhost:8080/upload> 也可以直接拖文件上传。
细节见[上传发布](docs/UPLOAD.md)。上传需要归档目录可写，详见 `docker-compose.yml`。

---

## API

客户端查询更新只需要一个请求：

```bash
curl "http://localhost:8080/api/v1/apps/myapp/check?version=1.0.0&os=windows&arch=x64"
```

```json
{
  "app": "myapp",
  "currentVersion": "1.0.0",
  "updateAvailable": true,
  "upToDate": false,
  "latestVersion": "1.2.0",
  "mandatory": true,
  "download": {
    "file": "MyApp-1.2.0-windows-x64.exe",
    "size": 12345678,
    "sha256": "e0e306249cc37258636bd6793125bb5fb9b549f4dcfcf53535a566cda9e8bb7f",
    "url": "http://localhost:8080/dl/myapp/1.2.0/MyApp-1.2.0-windows-x64.exe"
  }
}
```

其余接口：

| 接口 | 说明 |
| --- | --- |
| `GET /api/v1/health` | 健康检查与索引状态 |
| `GET /api/v1/apps` | 应用列表 |
| `GET /api/v1/apps/{app}` | 应用详情（含全部版本） |
| `GET /api/v1/apps/{app}/releases` | 版本列表 |
| `GET /api/v1/apps/{app}/releases/{version}` | 指定版本 |
| `GET /api/v1/apps/{app}/latest` | 最新版本，可按 `os` / `arch` 筛选 |
| `GET /api/v1/apps/{app}/check` | **查更新** |
| `POST /api/v1/apps/{app}/upload` | **上传发布**压缩包，需令牌 |
| `POST /api/v1/rescan` | 立即重新扫描 |
| `GET /dl/{app}/{version}/{file}` | 下载；版本可写 `latest`，支持断点续传 |
| `GET /docs` | 内置文档（发布规范、上传说明、API） |

完整字段说明见 [API 文档](docs/API.md)，运行时也可以直接在站点上访问
<http://localhost:8080/docs> 查看同样的内容。

---

## 配置

全部通过环境变量，无需配置文件。

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `ADDR` | 容器内 `:80`，直接运行 `:8080` | HTTP 监听地址 |
| `DATA_DIR` | `/data` | 归档根目录，其下的 `apps/` 存放应用 |
| `CACHE_DIR` | `/var/cache/updatesite` | 校验和缓存位置 |
| `SCAN_INTERVAL` | `15s` | 扫描周期 |
| `SITE_TITLE` | `软件更新中心` | 站点标题 |
| `SITE_SUBTITLE` | — | 站点副标题 |
| `BASE_URL` | 自动推断 | API 返回的绝对地址前缀，反代时建议显式设置 |
| `CORS_ORIGIN` | `*` | API 的 `Access-Control-Allow-Origin` |
| `RESCAN_TOKEN` | — | 设置后 `POST /api/v1/rescan` 需要 Bearer 令牌 |
| `LOG_REQUESTS` | `true` | 是否打印访问日志 |
| `UPLOAD_ENABLED` | `true` | 是否开放上传接口与上传页面 |
| `MAX_UPLOAD` | `2GiB` | 单个上传包的大小上限，支持 `512MiB` 这类后缀 |
| `TLS_ADDR` | `:443` | HTTPS 监听地址，仅在配置了证书时启用 |
| `TLS_CERT` | — | PEM 证书路径（可含证书链） |
| `TLS_KEY` | — | 与证书配对的 PEM 私钥路径 |
| `TLS_REDIRECT` | `false` | 打开后 HTTP 全部 301 到 HTTPS |

---

## 构建多架构镜像

```bash
./scripts/build.sh                    # 本地构建 amd64 + arm64
PUSH=1 ./scripts/build.sh             # 构建并推送
PLATFORMS=linux/arm64 ./scripts/build.sh   # 只构建 arm64 并载入本地 docker
```

或直接用 docker：

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=1.2.0 -t mti/updatesite:1.2.0 --push .
```

`scripts/build.sh` 会把版本号、commit、构建时间通过 `-ldflags` 注入，
在站点页脚和 `/api/v1/health` 里可见。

---

## 技术选型

| 决策 | 理由 |
| --- | --- |
| **目录约定 + 可选 JSON 覆盖**，不用数据库 | 归档目录可直接挂载、用文件管理器维护、进 Git LFS 或备份系统。发布方只需拷贝文件；需要精确控制时再用 `release.json` 覆盖。没有迁移、没有状态、重复执行结果一致 |
| **Go 标准库，零第三方依赖** | 编译出一个静态二进制，离线可构建，没有依赖漂移与供应链风险。`net/http`、`html/template`、`embed` 足以支撑这个规模的服务 |
| **`scratch` 基础镜像** | 镜像约 10 MB，容器里没有 shell、包管理器和 libc，攻击面最小。健康检查由二进制自身的 `-healthcheck` 参数完成 |
| **单次交叉编译代替 QEMU** | `--platform=$BUILDPLATFORM` + `GOARCH=$TARGETARCH`，一次构建同时产出 amd64 和 arm64，不需要模拟 |
| **后台扫描 + 原子快照** | 文件遍历和哈希计算在后台进行，HTTP 处理函数只读当前快照，永远不会被慢磁盘阻塞。扫描完成后整体替换，读到的数据始终自洽 |
| **校验和按 mtime 缓存** | 大文件只在首次出现时计算一次，结果持久化到 `CACHE_DIR`，容器重启后不重算 |
| **Markdown 子集自研渲染** | 避免引入依赖。渲染前先整体 HTML 转义，链接做协议白名单校验，代码块用占位符隔离，从构造上杜绝 XSS |

### 项目结构

```
cmd/updatesite/        程序入口；含 token 子命令与 -healthcheck 自检模式
internal/config/       环境变量配置
internal/semver/       宽松语义化版本解析与比较
internal/index/        归档扫描、平台识别、校验和缓存、快照发布
internal/token/        由应用 ID 推导上传令牌
internal/upload/       压缩包解包、版本推断、原子替换
internal/server/       HTTP 路由、JSON API、上传、下载、网页与模板
internal/buildinfo/    构建期注入的版本信息
scripts/publish.sh     发布脚本
docs/                  文档，同时被嵌入二进制并在 /docs 提供浏览
├── docs.go            把本目录的 .md 嵌入二进制
├── RELEASE-SPEC.md    发布规范
├── UPLOAD.md          上传发布说明
└── API.md             API 文档
```

---

## 开发

```bash
go test ./...      # 测试
go vet ./...       # 静态检查
make run           # 用 release/apps 目录本地跑起来
```

平台识别、版本比较、发布目录解析、HTTP 接口都有测试覆盖，
改动这些逻辑时请先跑一遍测试。

---

## HTTPS

容器同时监听 80 和 443，配置了证书后 443 自动启用 TLS：

```bash
./scripts/self-signed-cert.sh ./certs      # 测试用自签证书
TLS_CERT=./certs/server.crt TLS_KEY=./certs/server.key TLS_REDIRECT=true \
  docker compose up -d --build
```

在 `docker-compose.yml` 里长期生效的写法是把证书挂进来并打开变量：

```yaml
    environment:
      TLS_CERT: "/certs/server.crt"
      TLS_KEY: "/certs/server.key"
      TLS_REDIRECT: "true"
      BASE_URL: "https://update.example.com"
    volumes:
      - ./certs:/certs:ro
```

几点说明：

- 证书和私钥**必须同时配置**，只给一个会记一条警告并保持 HTTP-only
- TLS 最低版本锁在 1.2
- 打开 `TLS_REDIRECT` 后 HTTP 请求 301 到 HTTPS，**但 `/api/v1/health` 例外**——
  否则容器健康检查会被自己重定向走
- 通过 HTTPS 访问时，API 返回的 `pageUrl` / `url` 自动是 `https://`，
  走 HTTP 时是 `http://`；固定域名时设 `BASE_URL` 更省事
- 自签证书浏览器会报警告。生产请用内部 CA 或 Let's Encrypt

不需要内置 TLS、想交给网关终止的话，把 443 的端口映射去掉做纯反代即可。

---

## 反向代理

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-Host $host;
    client_max_body_size 0;    # 安装包可能很大
    proxy_request_buffering off;
}
```

`BASE_URL` 没设置时，站点会根据 `X-Forwarded-*` 还原外部地址；
固定域名时直接设置 `BASE_URL=https://update.example.com` 更省事。
