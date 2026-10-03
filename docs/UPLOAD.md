# 上传发布

除了直接往归档目录里放文件，也可以通过站点上传：把符合[发布规范](RELEASE-SPEC.md)的
文件打成一个压缩包，POST 到接口，站点自动解包并归档到
`apps/<应用ID>/<版本号>/`，同名版本整体替换。

适合放在 CI 的发布步骤里，或者由开发人员手动执行。

---

## 1. 拿令牌

令牌由**应用 ID 单独推导**，和运行环境无关：同一个应用 ID 在任何站点、
任何部署上算出来的令牌都一样。管理员算一次告诉开发人员就行。

```bash
updatesite token myapp
```

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
| `updatesite token <应用ID>` | 打印某个应用的令牌和示例命令 |
| `updatesite token <应用ID> -q` | 只输出令牌，便于脚本使用 |
| `updatesite token --list` | 列出归档目录里所有应用的令牌 |

选项写在应用 ID 前面或后面都可以。数据目录用 `DATA_DIR` 指定，
`--list` 会去读它。

> **安全边界**：令牌的推导密钥是编译在程序里的。这样做是为了让令牌
> 「任何环境都一致」，代价是**拿到这个程序（或源码）的人可以为任意应用
> 算出令牌**。它能挡住不知道令牌的人、扫描器和泄漏的 URL，挡不住已经
> 拿到程序本体的人。如果不接受这个前提，就关掉上传
> （`UPLOAD_ENABLED=false`），只用挂载目录发布。

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

上传需要归档目录**可写**。`docker-compose.yml` 里默认就是可写的：

```yaml
volumes:
  - ./release/apps:/data/apps
```

如果挂载成只读，上传会返回 `503` 并提示
`is the archive mounted read-write?`，其他功能不受影响。

不需要上传功能时：

```yaml
environment:
  UPLOAD_ENABLED: "false"
```

这时接口返回 `403`，页面返回 `404`，顶部导航也不会出现「上传」入口。
