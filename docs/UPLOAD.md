# 上传发布

除了直接往归档目录里放文件，也可以通过站点上传：把符合[发布规范](RELEASE-SPEC.md)的
文件打成一个压缩包，POST 到接口，站点自动解包并归档到
`apps/<应用ID>/<版本号>/`，同名版本整体替换。

适合放在 CI 的发布步骤里，或者由开发人员手动执行。

---

## 1. 配置部署密钥并拿令牌

上传**默认关闭**，需要先给站点配一个部署密钥 `UPLOAD_SECRET`：

```bash
updatesite token -gen-secret        # 生成一个随机密钥
```

把输出写进 `.env`（该文件已被 `.gitignore` 排除，不会进版本库）：

```ini
UPLOAD_SECRET=<把 -gen-secret 的输出粘贴到这里>
```

重启站点后，上传接口和上传页面才会生效。密钥是唯一的副本，请自行保存；
**换掉它会让已发出的令牌全部失效**。

然后就可以算某个应用的令牌了。站点用 Docker 跑的话直接在容器里算
（镜像里没有 shell，但二进制在 `PATH` 上，命令和本机一致；`docker exec`
会继承容器的环境变量，所以密钥不用重复传）：

```bash
docker exec updatesite updatesite token myapp
```

本地装了 Go 或已编译出二进制时，要显式带上密钥：

```bash
UPLOAD_SECRET=<密钥> updatesite token myapp
```

应用 ID 不需要事先存在，`token` 是 `(密钥, 应用ID)` 的纯函数，
随便什么 ID 都能算出来。

输出里会带上一条可以直接粘贴的 curl 命令：

```
应用 ID    myapp
上传令牌   bzzsor4qg76nhiylqqi2hm2u3ntaa52z

  curl -fS -X POST \
    -H "Authorization: Bearer bzzsor4qg76nhiylqqi2hm2u3ntaa52z" \
    -F "file=@release.zip" \
    -F "version=1.2.0" \
    https://update.example.com/api/v1/apps/myapp/upload
```

| 命令 | 用途 |
| --- | --- |
| `updatesite token -gen-secret` | 生成一个新的 `UPLOAD_SECRET` |
| `updatesite token <应用ID>` | 打印某个应用的令牌和示例命令 |
| `updatesite token <应用ID> -q` | 只输出令牌，便于脚本使用 |
| `updatesite token --list` | 列出归档目录里所有应用的令牌 |

选项写在应用 ID 前面或后面都可以。本机运行时数据目录用 `DATA_DIR` 指定，
`--list` 会去读它；在容器里跑时 `DATA_DIR` 已经是 `/data`，读的就是挂载进来的归档目录。

### 跨环境一致性

令牌是 `HMAC-SHA256(UPLOAD_SECRET, 应用ID)`。**只要各环境配置同一个
`UPLOAD_SECRET`，同一个应用在任何站点上算出来的令牌就完全相同**，
管理员仍然只需要算一次、告知开发人员一次。

密钥由部署方提供而不是编译在程序里，所以源码公开不会让任何人获得
给别人的站点签发令牌的能力——这正是它和工作密钥的区别。

---

## 2. 打包

压缩包的内容应该是**一个版本目录里的东西**，不要带应用目录那一层：

```
release.zip
├── MyApp-1.2.0-windows-x64.exe
├── MyApp-1.2.0-linux-arm64.tar.gz
├── MyApp-1.2.0-macos-universal.dmg
├── release.json          # 可选
├── CHANGELOG.md          # 可选
└── SHA256SUMS            # 可选
```

支持的格式：

| 格式 | 说明 |
| --- | --- |
| `.zip` | 推荐，Windows 上最方便 |
| `.tar.gz` / `.tgz` | Linux 上最常用 |
| `.tar` | |
| `.tar.bz2` | |

**不支持 `.tar.xz`、`.tar.zst`、`.7z`、`.rar`** —— 标准库没有对应的解码器，
而这个项目不引入第三方依赖。请转成 zip 或 tar.gz。传了这些格式会直接报错
并提示转换。

格式按**文件头魔数**识别，不看扩展名，所以改名不影响。

打包时以下几点会被自动处理，不用手工清理：

- `__MACOSX/`、`.DS_Store`、`._*` 等 macOS 附加文件会被丢弃
- 以 `.` 开头的文件和目录会被丢弃
- **符号链接、硬链接、设备文件会被丢弃**（安全考虑）

Windows 上打包：

```powershell
Compress-Archive -Path .\dist\* -DestinationPath release.zip -Force
```

Linux / macOS：

```bash
tar -czf release.tar.gz -C dist .
```

---

## 3. 版本号怎么确定

按顺序尝试，前一步成功就不看后面的：

1. **接口参数** —— `-F "version=1.2.0"` 或 `?version=1.2.0`
2. **压缩包里唯一的一级目录**，且目录名是合法版本号。也支持 GitHub 那种
   `myapp-1.2.3/` 的包装目录，会取末尾的版本号
3. **根目录 `release.json` 的 `version` 字段**

三步都拿不到版本号会返回 `400` 并说明原因。

> 注意：在磁盘上，`release.json` 的 `version` 字段是被忽略的（目录名优先）。
> 只有在上传时、且前两种方式都没给出结果，它才会被用上。

---

## 4. 上传

### 表单上传

```bash
curl -fS -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@release.zip" \
  -F "version=1.2.0" \
  https://update.example.com/api/v1/apps/myapp/upload
```

### 直接发原始归档

```bash
curl -fS -X POST \
  -H "Authorization: Bearer $TOKEN" \
  --data-binary @release.tar.gz \
  -H "Content-Type: application/gzip" \
  "https://update.example.com/api/v1/apps/myapp/upload?version=1.2.0"
```

### 网页上传

浏览器打开 `/upload`，填应用 ID 和令牌，选文件即可。适合手动补发一个版本。

### 成功响应

```json
{
  "ok": true,
  "app": "myapp",
  "version": "1.2.0",
  "replaced": false,
  "bytes": 12345678,
  "files": [
    {"file": "MyApp-1.2.0-windows-x64.exe", "size": 9000000},
    {"file": "CHANGELOG.md", "size": 1200}
  ],
  "pageUrl": "https://update.example.com/a/myapp/1.2.0",
  "apiUrl": "https://update.example.com/api/v1/apps/myapp/releases/1.2.0",
  "message": "release installed, the site is rescanning"
}
```

站点随后会自动重新扫描，通常一两秒内新版本就能在网页和 API 上看到。
`replaced: true` 表示同名版本已存在并被整体替换。

### 错误码

| 状态码 | 场景 |
| --- | --- |
| `400` | 应用 ID 非法、版本号无法确定、压缩包损坏或不含可安装文件 |
| `401` | 令牌缺失或错误 |
| `403` | 站点关闭了上传功能 |
| `413` | 超过 `MAX_UPLOAD` 限制 |
| `503` | 归档目录不可写（挂载成了只读） |

---

## 5. 站点做了什么校验

上传是往磁盘写文件，所以校验比较严：

- 解压到的每个路径都会规范化，**拒绝绝对路径和 `../` 逃逸**
- 丢弃符号链接、硬链接、设备文件
- 限制单个压缩包展开后的总大小（默认 `MAX_UPLOAD` 的 4 倍）和文件数量（20000）
- 要求压缩包里**至少有一个可安装文件**，只带 `release.json` 和 `CHANGELOG.md`
  的包会被拒绝（否则会生成一个空版本）
- 解压先落在应用目录下的 `.staging-*` 隐藏目录，校验通过后才整体换入目标目录；
  替换时旧目录先挪到 `.trash-*`，失败会还原
- 扫描器会跳过 `.` 开头的目录，所以**解压到一半的版本不会被外部看到**

---

## 6. 部署要求

上传需要满足两个条件，缺任何一个都会关掉这个功能：

**1. 配置 `UPLOAD_SECRET`。** 没配置时站点启动会打印一行说明，接口返回
`403`，页面返回 `404`，顶部导航也不会出现「上传」入口。

**2. 归档目录可写。** `docker-compose.yml` 里默认就是可写的：

```yaml
volumes:
  - ./release/apps:/data/apps
```

如果挂载成只读，上传会返回 `503` 并提示
`is the archive mounted read-write?`，其他功能不受影响。

想主动关掉上传，设 `UPLOAD_ENABLED=false` 即可，效果和没配密钥一样。

### 密钥怎么保管

- 只在 `.env` 或部署平台的 secret 里维护，**不要提交进版本库**
  （`.env` 已默认被 `.gitignore` 排除）
- 各环境保持一致，管理员算出的令牌才能通用
- 泄露了就换一个：生成新密钥 → 更新各环境 → 把新令牌重新发给开发人员，
  旧令牌立即失效
- 想给不同应用组用不同密钥也可以，代价是令牌不再跨组通用
