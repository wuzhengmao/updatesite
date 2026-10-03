# updatesite · 应用更新站点

一个用于多产品、多平台、多架构的**在线更新站点**。把安装包按约定丢进归档目录
就完成发布，站点自动归档、计算校验和、提供网页浏览与 REST API 查询。

- **发布即拷贝** —— 目录结构本身就是数据库，`cp` 进去就生效，不用登录后台
- **也能上传发布** —— 打包成 zip/tar.gz 通过接口或网页上传，站点自动解包归档
- **应用信息可在线维护** —— 名称、描述、标签、图标都能在网页上改，不必手工编辑 JSON
- **多产品多平台** —— 每个应用独立 ID，每个版本按 `os` / `arch` 归档安装包
- **REST API** —— 客户端传当前版本号即可查询是否有新版本、下载哪个包
- **内置文档** —— 发布规范、上传说明、API 文档随二进制一起分发，浏览 `/docs` 即可查看
- **单二进制** —— Go 标准库实现，零第三方依赖，静态编译
- **多架构镜像** —— `linux/amd64` 与 `linux/arm64`，基于 `scratch`，镜像约 13 MB

代码在 <https://github.com/wuzhengmao/updatesite>。

---

## 快速开始

```bash
git clone https://github.com/wuzhengmao/updatesite.git
cd updatesite
docker compose up -d --build
```

容器内监听 **80（HTTP）** 与 **443（HTTPS）**，映射到宿主的 `8080` 与 `8443`。

开箱即用的状态取决于你给了什么：

| 缺少的东西 | 后果 |
| --- | --- |
| `certs/server.crt`、`certs/server.key` | 443 不启动，只提供 HTTP，日志里会说明原因 |
| `.env` 里的 `UPLOAD_SECRET` | 上传接口与上传页面整体关闭 |
| `release/apps` 里有内容 | 站点是空的 |

所以想立刻看到完整效果：

```bash
cp .env.example .env
./scripts/self-signed-cert.sh ./certs
mkdir -p release/apps && cp -r examples/apps/* release/apps/
docker compose up -d --build
```

然后打开 <https://localhost:8443>（自签证书，浏览器会警告，选「继续访问」）。
只看 HTTP 的话是 <http://localhost:8080>，它会 301 跳到 HTTPS。

版本号取自仓库根目录的 **`VERSION` 文件**，已入库，所以服务器上 `git pull`
之后直接构建就能拿到正确的版本，不需要改任何配置：

```bash
echo 0.3.0 > VERSION && git commit -am "版本 0.3.0"
```

要临时构建一个别的版本号（比如候选版），命令行覆盖即可：

```bash
VERSION=0.3.0-rc1 docker compose up -d --build
```

提交号与构建时间都能自动处理，不用管：

- **构建时间**：没传 `BUILD_DATE` 时回落到二进制自身的生成时间，也就是它被链接
  进镜像的时刻。走缓存复用出来的镜像报的是当初那次构建的时间，这是诚实的答案
- **提交号**：`COMMIT=$(git rev-parse --short HEAD) docker compose up -d --build`
  可以让页脚显示它；不传就不显示。compose 读不到 shell 变量，所以要像这样内联

### 只拉镜像、不要仓库

镜像里的二进制是自包含的：模板、样式、脚本、文档全部嵌在里面，运行时不需要
任何仓库文件。以下全程只用 `docker`，不需要 `git clone`。

下面用 `latest` 是为了让示例不会过期；生产环境建议换成固定版本号
（如 `wuzm219/updatesite:0.2.1`），或者干脆用 digest，见文末「推送到 Docker Hub」。

**1. 建目录，从镜像里生成部署密钥**

```bash
mkdir -p updatesite/{apps,certs} && cd updatesite

cat > .env <<EOF
UPLOAD_SECRET=$(docker run --rm wuzm219/updatesite:latest token -gen-secret -q)
EOF
```

> 镜像的 `ENTRYPOINT` 已经是这个程序，所以**不要再写一遍程序名**。
> `docker run --rm <镜像> token -gen-secret` 是对的；
> 写成 `docker run --rm <镜像> updatesite token ...` 会多传一个参数，
> 程序会报「未知命令」并退出——这是刻意设计，免得打错了却静默启动一个服务器。

**2. 写 `docker-compose.yml`**

```yaml
services:
  updatesite:
    image: wuzm219/updatesite:latest
    container_name: updatesite
    restart: unless-stopped
    ports:
      - "8080:80"
      - "8443:443"
    environment:
      SITE_TITLE: "软件更新中心"
      TZ: "Asia/Shanghai"
      UPLOAD_SECRET: "${UPLOAD_SECRET:?请先设置 UPLOAD_SECRET}"
      TLS_CERT: "/certs/server.crt"
      TLS_KEY: "/certs/server.key"
      TLS_REDIRECT: "true"
    volumes:
      - ./apps:/data/apps
      - ./certs:/certs:ro
      - updatesite-cache:/var/cache/updatesite
volumes:
  updatesite-cache:
```

`apps` 就是归档目录，发布的东西都在里面，换机器拷走即可。
`updatesite-cache` 只是个命名卷，存 sha256 缓存，删了会重算。

**3. 起服务**

```bash
docker compose up -d
```

`certs/` 还是空的话，443 不会启动，站点以纯 HTTP 提供服务并在日志里说明原因 ——
这是刻意的，证书配错不该让站点整个打不开。日志会打印：

```
TLS is configured but unusable, serving plain HTTP only: open /certs/server.crt: no such file or directory
```

**4. 配证书**

用你自己的正式证书（推荐），或者临时生成一张自签的：

```bash
openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 825 \
  -keyout certs/server.key -out certs/server.crt \
  -subj "//CN=update.example.com" \
  -addext "subjectAltName=DNS:update.example.com,IP:127.0.0.1"

docker compose up -d --force-recreate
```

> `//CN=` 里的双斜杠是给 Git Bash 的：MSYS 会把单个前导斜杠当路径转换，
> 把 `/CN=host` 改写成 `C:/Program Files/Git/CN=host`，openssl 会拒绝。
> Linux/macOS 上写 `/CN=` 即可。

**5. 验证**

```bash
docker logs updatesite 2>&1 | grep -i "listen"
curl -sk https://localhost:8443/api/v1/health
docker exec updatesite updatesite token <应用ID>       # 拿上传令牌
```

**升级**：改 `image:` 的 tag 再 `docker compose pull && docker compose up -d --force-recreate`。

**不用 compose 的等价写法**：

```bash
docker run -d --name updatesite --restart unless-stopped \
  -p 8080:80 -p 8443:443 \
  -e UPLOAD_SECRET="$(docker run --rm wuzm219/updatesite:latest token -gen-secret -q)" \
  -e TZ=Asia/Shanghai \
  -e TLS_CERT=/certs/server.crt -e TLS_KEY=/certs/server.key -e TLS_REDIRECT=true \
  -v "$PWD/apps:/data/apps" -v "$PWD/certs:/certs:ro" \
  -v updatesite-cache:/var/cache/updatesite \
  wuzm219/updatesite:latest
```

（Windows 的 Git Bash 里路径要写成 `"D:/updatesite/apps:/data/apps"` 这种形式，
并加 `MSYS_NO_PATHCONV=1`，否则容器内路径会被一起转换。）

### 不使用 Docker

```bash
DATA_DIR=./examples CACHE_DIR=./cache go run ./cmd/updatesite   # 默认监听 :8080
```

Windows PowerShell：

```powershell
$env:DATA_DIR="./examples"; $env:CACHE_DIR="./cache"; go run ./cmd/updatesite
```

容器以外的场景 `ADDR` 默认是 `:8080`，只有在容器里才被 `ENV ADDR=:80` 覆盖。

---

## 归档目录

`release/apps` 是**归档目录**，也是 compose 里的挂载点。它**不入版本库**——
里面是真实安装包，体积大且多为专有产物，属于部署产物而非源码。仓库里的
`examples/apps` 才是入库的演示数据。

生产环境一般把它指到别处：

```yaml
volumes:
  - /srv/updates/apps:/data/apps
```

**必须可写**，否则上传功能会失败（返回 `503` 并提示
`is the archive mounted read-write?`）。只手工拷贝发布、不需要上传时，
可以加 `:ro` 收紧权限。

---

## 发布一个版本

目录约定（详见 [发布规范](docs/RELEASE-SPEC.md)）：

```
release/apps/<应用ID>/<版本号>/
├── release.json     # 可选：通道、标题、强制更新、平台声明
├── CHANGELOG.md     # 可选：更新说明（Markdown）
├── SHA256SUMS       # 可选：校验和清单
├── MyApp-1.2.0-windows-x64.exe
├── MyApp-1.2.0-linux-amd64.tar.gz
└── MyApp-1.2.0-macos-universal.dmg
```

**最简发布**：

```bash
mkdir -p release/apps/myapp/1.2.0
cp dist/* release/apps/myapp/1.2.0/
```

默认 15 秒内生效，或者立刻通知站点：

```bash
curl -fkX POST https://localhost:8443/api/v1/rescan
```

> 下文凡是访问 `https://localhost:8443` 的命令都带了 `-k`，用来跳过自签证书校验；
> 换成正式证书后可以去掉。用 `http://localhost:8080` 也行，但默认配置下它会
> 301 跳到 HTTPS，`curl` 需要 `-L` 才会跟随。

**用脚本发布**（自动生成 `SHA256SUMS` 和 `release.json`）：

```bash
./scripts/publish.sh -a myapp -v 1.2.0 \
    -r ./release/apps \
    -n notes/1.2.0.md -t "体验优化版" -m \
    dist/MyApp-1.2.0-*
```

`-u https://站点地址` 可以在发布后顺带通知站点立即重扫；自签证书场景下
`publish.sh` 内部用的是 `curl -fsS`，需要跟随到正式证书才能用。

安装包文件名里带上平台和架构（`windows`、`x64`、`linux`、`arm64`…），
站点会自动识别归档；识别不出来也不会出错，只是需要手动用 `release.json` 声明。

### 用上传接口发布

把同样的内容打成 zip 或 tar.gz 上传，站点自动解包归档，同名版本整体替换。

上传**默认关闭**，先配一个部署密钥：

```bash
docker exec updatesite updatesite token -gen-secret
```

写进 `.env` 后重启（`.env` 不入库，从 `.env.example` 复制一份开始）：

```bash
cp .env.example .env
# 编辑 .env，填入刚生成的 UPLOAD_SECRET
docker compose up -d
```

之后算令牌：

```bash
docker exec updatesite updatesite token myapp
```

令牌由「部署密钥 + 应用 ID」推导。**各环境配同一个密钥，同一应用的令牌就处处
相同**，管理员仍然只需要算一次、告知开发人员一次。换密钥会让已发出的令牌全部失效。
细节见[上传发布](docs/UPLOAD.md)。

开发人员拿到令牌后：

```bash
curl -fkS -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@release.zip" -F "version=1.2.0" \
  https://localhost:8443/api/v1/apps/myapp/upload
```

网页端 <https://localhost:8443/upload> 也可以拖文件上传。

### 在线维护应用信息

同一个页面下半部分可以改应用的名称、描述、厂商、标签、排序和图标，
对应 `apps/<应用ID>/app.json`，对所有版本生效。接口见 [API 文档](docs/API.md)。

---

## API

客户端查询更新只需要一个请求：

```bash
curl -k "https://localhost:8443/api/v1/apps/myapp/check?version=1.0.0&os=windows&arch=x64"
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
    "url": "https://update.example.com/dl/myapp/1.2.0/MyApp-1.2.0-windows-x64.exe"
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
| `GET /api/v1/apps/{app}/metadata` | 读取应用元数据（含图标信息），需令牌 |
| `PUT /api/v1/apps/{app}/metadata` | **修改应用元数据**（名称/描述/标签…），需令牌 |
| `PUT /api/v1/apps/{app}/icon` | **更换图标**（PNG/JPEG/SVG/WebP），需令牌 |
| `DELETE /api/v1/apps/{app}/releases/{version}` | **删除某个版本**，需令牌，不可恢复 |
| `DELETE /api/v1/apps/{app}` | **删除整个应用**，需令牌，不可恢复 |
| `POST /api/v1/rescan` | 立即重新扫描 |
| `GET /dl/{app}/{version}/{file}` | 下载；版本可写 `latest`，支持断点续传 |
| `GET /docs` | 内置文档（发布规范、上传说明、API） |

完整字段说明见 [API 文档](docs/API.md)，运行时也可以直接在站点上访问
<https://localhost:8443/docs> 查看同样的内容。

---

## 配置

全部通过环境变量，无需配置文件。下表是**程序自身的默认值**；
`docker-compose.yml` 会覆盖其中几项（`ADDR=:80`、开启上传与 TLS），
以那个文件为准。

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `TZ` | 不设则 UTC（compose 里为 `Asia/Shanghai`） | 站点显示时间用的时区，任意 IANA 名称 |
| `ADDR` | `:8080`（容器内 `:80`） | HTTP 监听地址 |
| `DATA_DIR` | `/data` | 归档根目录，其下的 `apps/` 存放应用 |
| `CACHE_DIR` | `/var/cache/updatesite` | 校验和缓存位置 |
| `SCAN_INTERVAL` | `15s` | 扫描周期 |
| `SITE_TITLE` | `软件更新中心` | 站点标题 |
| `SITE_SUBTITLE` | — | 站点副标题 |
| `BASE_URL` | 空（从请求推断） | API 返回的绝对地址前缀。**反代场景留空即可**，站点会读 `X-Forwarded-Proto` / `X-Forwarded-Host`；容器被直接访问且端口与监听端口不一致时才需要设置 |
| `CORS_ORIGIN` | `*` | API 的 `Access-Control-Allow-Origin` |
| `RESCAN_TOKEN` | — | 设置后 `POST /api/v1/rescan` 需要 Bearer 令牌 |
| `LOG_REQUESTS` | `true` | 是否打印访问日志 |
| `UPLOAD_ENABLED` | `true` | 是否开放上传接口与上传页面 |
| `UPLOAD_SECRET` | — | **未设置则上传功能完全关闭**；令牌由它与应用 ID 共同推导 |
| `MAX_UPLOAD` | `2GiB` | 单个上传包的大小上限，支持 `512MiB` 这类后缀 |
| `TLS_ADDR` | `:443` | HTTPS 监听地址，仅在证书可用时启用 |
| `TLS_CERT` | — | PEM 证书路径（可含证书链） |
| `TLS_KEY` | — | 与证书配对的 PEM 私钥路径 |
| `TLS_REDIRECT` | `false`（compose 里为 `true`） | 打开后 HTTP 全部 301 到 HTTPS |

---

## HTTPS

容器同时监听 80 和 443。证书可用时 443 自动启用 TLS，不可用时**只记一条日志并
继续提供 HTTP**，不会让站点打不开。

`docker-compose.yml` 默认已经打开，只需要生成证书：

```bash
./scripts/self-signed-cert.sh ./certs
docker compose up -d --build
```

几点说明：

- 证书和私钥**必须同时配置**，只给一个会记一条警告并保持 HTTP-only
- 证书路径写错或私钥不匹配时，会打印具体原因并退回 HTTP-only
- TLS 最低版本锁在 1.2
- 打开 `TLS_REDIRECT` 后 HTTP 请求 301 到 HTTPS，**但 `/api/v1/health` 例外**——
  否则容器健康检查会被自己重定向走
- 重定向的目标地址优先取 `BASE_URL`。容器看不到宿主的端口映射，
  所以 `8080:80 / 8443:443` 这种映射**必须设置 `BASE_URL`** 才能跳对
- 通过 HTTPS 访问时，API 返回的 `pageUrl` / `url` 自动是 `https://`；
  走 HTTP 时是 `http://`
- 自签证书浏览器会报警告。生产请用内部 CA 或 Let's Encrypt

不需要内置 TLS、想交给网关终止的话，去掉 443 的端口映射，
并把 `TLS_CERT` / `TLS_KEY` / `TLS_REDIRECT` 注释掉。

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

反代场景**不需要设置 `BASE_URL`**：站点会读 `X-Forwarded-Proto` 与
`X-Forwarded-Host` 拼出外部地址，换域名时无需改动。上面那段 nginx 配置已经带了
这两个头。

**注意**：compose 默认 `TLS_REDIRECT=true`，反代如果直接打 HTTP 端口会收到 301
死循环。交给网关终止 TLS 时，请先把 `TLS_REDIRECT` 改回 `"false"` 并去掉
443 的映射。

### 排查绝对地址不对

API 返回的 `url` / `pageUrl` 指向了错误的主机（比如 `localhost`），按这个顺序查：

1. 看容器启动日志，会打印一行 `absolute URLs are built from BASE_URL=...`
   或 `absolute URLs follow X-Forwarded-Proto/Host`
2. 如果打印的是 `BASE_URL=...`，说明它被设上了。反代场景应当留空 ——
   检查 `.env` 里有没有残留的 `BASE_URL`
3. 如果走的是请求头推断，确认代理确实传了
   `X-Forwarded-Proto` 与 `X-Forwarded-Host`

---

## 构建多架构镜像

```bash
./scripts/build.sh                          # 本地构建 amd64 + arm64
PUSH=1 ./scripts/build.sh                   # 构建并推送
PLATFORMS=linux/arm64 ./scripts/build.sh    # 只构建 arm64 并载入本地 docker
```

或直接用 docker：

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t wuzm219/updatesite:$(cat VERSION) --push .   # 版本号取自 VERSION 文件
```

### 推送到 Docker Hub

镜像名是 `wuzm219/updatesite`。`buildx` 构建多架构后直接推送：

```bash
docker login                    # 只需要做一次
PUSH=1 ./scripts/build.sh      # tag 取自 VERSION 文件，无需手动指定
```

会推送 `wuzm219/updatesite:<VERSION 文件的值>` 和 `wuzm219/updatesite:latest` 两个 tag，
都带 amd64 与 arm64 两个平台：

```bash
docker manifest inspect wuzm219/updatesite:<版本号>   # 确认两个架构都在
```

换了镜像名或命名空间的话，改 `scripts/build.sh` 里的 `IMAGE` 默认值，
或临时覆盖：`IMAGE=someone/updatesite PUSH=1 ./scripts/build.sh`。

---

`scripts/build.sh` 会把版本号、commit、构建时间通过 `-ldflags` 注入，
显示在站点页脚和 `/api/v1/health` 里：

```
0.2.1+a1b2c3d · 2026-10-03 13:32 +08:00
```

构建时间来自 `BUILD_DATE`（UTC 存储，便于比较）。没传时回落到二进制自身的
生成时间 —— 在镜像里那就是它被链接的时刻，走缓存复用出来的镜像报的也是当初
那次构建的时间。

显示时按 `TZ` 转成当地时间，偏移用数字形式而非 `CST` 这类缩写 —— 单看 `CST`
无法区分中国标准时间、美国中部时间和古巴标准时间。时区库已嵌入二进制，
所以 `scratch` 镜像里不装 tzdata 也能用。

### 本地改完代码后重建

```bash
./scripts/dev-rebuild.sh
```

它会用 `.env` 里的版本戳构建镜像、强制重建容器、等健康检查通过。
脚本内部刻意用 `docker build` 而不是 `docker compose build`：Docker Desktop 有时
会把默认 buildx builder 换成 `docker-container` 驱动的（名字是随机两个单词），
那种 builder 构建的镜像不进本地镜像库，compose 会认为无变化而**继续跑旧镜像**，
站点静默地提供过期版本。

---

## 技术选型

| 决策 | 理由 |
| --- | --- |
| **目录约定 + 可选 JSON 覆盖**，不用数据库 | 归档目录可直接挂载、用文件管理器维护、进备份系统。发布方只需拷贝文件；需要精确控制时再用 `release.json` 覆盖。没有迁移、没有状态、重复执行结果一致 |
| **Go 标准库，零第三方依赖** | 编译出一个静态二进制，离线可构建，没有依赖漂移与供应链风险。`net/http`、`html/template`、`embed` 足以支撑这个规模的服务 |
| **`scratch` 基础镜像** | 镜像约 13 MB，容器里没有 shell、包管理器和 libc，攻击面最小。健康检查由二进制自身的 `-healthcheck` 参数完成 |
| **单次交叉编译代替 QEMU** | `--platform=$BUILDPLATFORM` + `GOARCH=$TARGETARCH`，一次构建同时产出 amd64 和 arm64，不需要模拟 |
| **后台扫描 + 原子快照** | 文件遍历和哈希计算在后台进行，HTTP 处理函数只读当前快照，永远不会被慢磁盘阻塞。扫描完成后整体替换，读到的数据始终自洽 |
| **校验和按 mtime 缓存** | 大文件只在首次出现时计算一次，结果持久化到 `CACHE_DIR`，容器重启后不重算 |
| **上传密钥由部署方提供** | 令牌是 `(密钥, 应用ID)` 的纯函数，各环境配同一密钥时令牌通用；密钥不写进源码，公开仓库不等于公开签发能力 |
| **Markdown 子集自研渲染** | 避免引入依赖。渲染前先整体 HTML 转义，链接做协议白名单校验，代码块用占位符隔离，从构造上杜绝 XSS |

### 项目结构

```
VERSION                版本号，唯一来源；构建时读入，也用于镜像 tag
cmd/updatesite/        程序入口；含 token 子命令与 -healthcheck 自检模式
internal/config/       环境变量配置
internal/semver/       宽松语义化版本解析与比较
internal/index/        归档扫描、平台识别、校验和缓存、快照发布
internal/appmeta/      app.json 与图标的读写
internal/token/        由部署密钥与应用 ID 推导上传令牌
internal/upload/       压缩包解包、版本推断、原子替换
internal/server/       HTTP 路由、JSON API、上传、下载、网页与模板
internal/buildinfo/    构建期注入的版本信息
scripts/publish.sh     发布脚本
scripts/build.sh       多架构镜像构建
scripts/self-signed-cert.sh  测试用自签证书
docs/                  文档，同时被嵌入二进制并在 /docs 提供浏览
├── docs.go            把本目录的 .md 嵌入二进制
├── RELEASE-SPEC.md    发布规范
├── UPLOAD.md          上传发布说明
└── API.md             API 文档
examples/apps/         演示归档（占位文件，入库）
release/apps/          真实归档目录（挂载点，不入库）
.env.example           环境变量模板；复制成 .env 使用，.env 不入库
```

> `release/apps` 被 `.gitignore` 排除：里面是真实安装包，体积大且多为专有产物，
> 属于部署产物而非源码。`.env` 同样不入库，因为它可能存着 `UPLOAD_SECRET`。
> 往 UI 和文档里写示例时请用 `examples/apps` 里的应用，不要用真实产品。

---

## 开发

```bash
go test ./...      # 测试
go vet ./...       # 静态检查
make run           # 用 examples/apps 目录本地跑起来（监听 :8080）
```

平台识别、版本比较、发布目录解析、上传解包、元数据读写、HTTP 接口都有测试覆盖，
改动这些逻辑时请先跑一遍测试。
