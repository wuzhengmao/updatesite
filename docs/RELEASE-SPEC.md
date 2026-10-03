# 应用发布规范 v1

本规范定义了更新站点能识别的归档目录结构。**目录结构本身就是数据库**：
把文件放进去就发布完成，站点在下一个扫描周期（默认 15 秒）自动识别、归档、
计算校验和并对外提供浏览与 API 查询。

设计目标有两个，取舍时以它们为准：

1. **发布方写起来最简单** —— 一个 `cp` 或一次 `scp` 就能发版，不需要登录后台、
   不需要调用上传接口、不需要额外的发布客户端。
2. **机器读起来最确定** —— 目录名即版本号，文件名即平台，可选 JSON 覆盖一切；
   没有隐式状态，没有需要按顺序执行的步骤，重复执行结果一致。

---

## 1. 目录结构

```
<DATA_DIR>/                      # 容器内挂载点，默认 /data
└── apps/
    └── <app-id>/                # 应用 ID，同时是站点 URL 中的标识
        ├── app.json             # 可选：应用级信息
        ├── icon.png             # 可选：应用图标（也支持 icon.svg / logo.png）
        ├── 1.0.0/               # 版本目录，目录名就是版本号
        │   ├── release.json     # 可选：本版本的信息
        │   ├── CHANGELOG.md     # 可选：更新说明
        │   ├── MyApp-1.0.0-windows-x64.exe
        │   ├── MyApp-1.0.0-linux-amd64.tar.gz
        │   └── SHA256SUMS       # 可选：校验和清单
        └── 1.1.0/
            └── ...
```

**最小可用发布**：建一个 `apps/<app-id>/<版本号>/` 目录，把安装包丢进去。
就这些。其余都是可选的增强。

### 规则

| 项 | 规则 |
| --- | --- |
| `<app-id>` | 目录名即应用 ID。允许 `a-z A-Z 0-9 . _ -`，不能以 `.` 开头 |
| 版本目录 | 必须匹配 `^v?\d+(\.\d+)*([-_+][0-9A-Za-z.\-+]*)?$`，例如 `1.2.3`、`v1.2`、`2026.10.03`、`2.0.0-rc.1` |
| 版本排序 | 按语义化版本比较，**数字逐段比较**（`1.10.0` > `1.9.0`），预发布版低于同号正式版（`1.0.0-rc.1` < `1.0.0`） |
| 隐藏项 | 以 `.` 开头的文件和目录一律忽略 |
| 版本目录例外 | 目录名不是合法版本号但含有 `release.json` 时，仍会被收录 |
| 保留路径 | `/a/<app-id>/icon` 被图标占用，版本目录不要命名为 `icon` |

---

## 2. `app.json`（可选）

应用级信息，放在 `<app-id>/` 目录下。所有字段可省略。

```json
{
  "name": "MyApp 客户端",
  "summary": "一句话描述，显示在列表页",
  "description": "详细描述，支持 Markdown，显示在应用详情页",
  "homepage": "https://example.com/myapp",
  "vendor": "MTI",
  "license": "Proprietary",
  "tags": ["desktop", "tool"],
  "channel": "stable",
  "icon": "icon.png",
  "order": 10,
  "hidden": false
}
```

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 目录名 | 显示名称 |
| `summary` | string | — | 简短描述 |
| `description` | string | — | 详细描述，Markdown |
| `homepage` | string | — | 官网地址 |
| `vendor` | string | — | 厂商 / 团队 |
| `license` | string | — | 许可证 |
| `tags` | string[] | — | 标签 |
| `channel` | string | `stable` | 默认发布通道 |
| `icon` | string | 自动探测 | 图标文件名，位于应用目录下 |
| `order` | number | `100` | 列表排序，**小的排前面** |
| `hidden` | bool | `false` | 为 `true` 时不对外展示 |

`id` 字段可写但会被忽略 —— 目录名始终优先，避免出现两处不一致。

图标未指定时，按 `icon.png`、`icon.svg`、`logo.png`、`logo.svg`、`icon.jpg`
的顺序探测。

---

## 3. `release.json`（可选）

版本级信息，放在 `<版本号>/` 目录下。所有字段可省略。

```json
{
  "channel": "stable",
  "title": "体验优化版",
  "publishedAt": "2026-09-20T09:30:00+08:00",
  "prerelease": false,
  "mandatory": true,
  "minVersion": "1.0.0",
  "notes": "Markdown 更新说明（会被同目录的 CHANGELOG.md 覆盖）",
  "artifacts": [
    {
      "file": "MyApp-1.2.0-windows-x64.exe",
      "name": "Windows 64 位安装程序",
      "kind": "installer",
      "os": "windows",
      "arch": "x64",
      "sha256": "e0e306249cc37258636bd6793125bb5fb9b549f4dcfcf53535a566cda9e8bb7f",
      "size": 12345678,
      "url": "https://cdn.example.com/MyApp-1.2.0-windows-x64.exe"
    }
  ]
}
```

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `channel` | string | 应用的 `channel` | 发布通道，如 `stable` / `beta` |
| `title` | string | — | 版本标题 |
| `publishedAt` | string | 目录 mtime | RFC3339；也支持 `2006-01-02` 与 `2006-01-02 15:04:05` |
| `prerelease` | bool | 自动判断 | 版本号含 `-` 或 `_` 时默认为 `true` |
| `mandatory` | bool | `false` | 强制更新标记，API 会返回给客户端自行决策 |
| `minVersion` | string | — | 低于此版本不支持增量升级，客户端应引导全量安装 |
| `notes` | string | — | Markdown 更新说明 |
| `artifacts` | object[] | 自动识别 | **未列出的文件仍会自动收录** |

`version` 字段可写但会被忽略 —— 目录名始终优先。

### `artifacts[]` 条目

| 字段 | 说明 |
| --- | --- |
| `file` | **必填**，相对于版本目录的路径，也允许用 `子目录/文件` |
| `name` | 显示名称（默认用文件名）。**不影响下载时的实际文件名** |
| `os` | 平台，见第 5 节别名表 |
| `arch` | 架构，见第 5 节别名表 |
| `kind` | `installer` / `portable` / `archive` / `package` / `image` / `other` |
| `sha256` | 校验和，缺省时按第 6 节顺序推导 |
| `size` | 字节数，缺省时读取文件实际大小 |
| `url` | 外部下载地址（`http(s)://`）。填写后文件可以不存在于本地，站点会 302 跳转 |

`artifacts` 数组的顺序就是网站上安装包的展示顺序；未在数组中声明、
由站点自动发现的文件排在其后。

---

## 4. 文件名自动识别

站点会从文件名中推断平台、架构和类型，**识别词可以出现在文件名的任意位置**。

```
MyApp-1.2.0-windows-x64.exe          → windows / x64    / installer
MyApp-1.2.0-win64-setup.exe          → windows / x64    / installer
app_1.2.0_linux_amd64.tar.gz         → linux   / x64    / archive
app-1.2.0-linux-arm64.deb            → linux   / arm64  / package
app-2.0.0-linux-armv7l.rpm           → linux   / arm32  / package
App-3.0.0-macos-universal.dmg        → macos   / 通用   / installer
App-3.0.0-darwin-arm64.pkg           → macos   / arm64  / installer
MobileApp-3.1.0-android-universal.apk→ android / 通用   / package
MobileApp-3.1.0.ipa                  → ios     / 通用   / package
tool-1.0.0-linux-x86_64.AppImage     → linux   / x64    / portable
```

无法识别时不会报错：平台/架构留空，类型记为 `other`，文件照常列出和下载。

### 推断回退

按顺序尝试，前一步成功就不再看后面的：

1. 文件名中的显式平台词和架构词
2. `win64` / `win32` / `linux64` 这类"平台+位宽"合并词
3. 单独出现的 `64` / `32` 词元
4. 由扩展名推断平台：`.exe .msi`→windows，`.dmg .pkg`→macos，`.apk .aab`→android，
   `.ipa`→ios，`.deb .rpm .AppImage`→linux
5. 由平台推断架构：windows/macos 默认 `x64`，android/ios 默认 `universal`

---

## 5. 平台与架构别名表

规范输出始终是**规范化后的 id**，输入则接受下列所有写法（大小写不敏感，
`-` `_` `.` 空格可互换）。

### 平台（os）

| 规范 id | 接受的写法 |
| --- | --- |
| `windows` | `windows` `win` `winnt` `win32` `win64` `mswindows` `mingw` `cygwin` |
| `linux` | `linux` `linuxgnu` `linuxmusl` `gnu` |
| `macos` | `macos` `mac` `macosx` `osx` `darwin` `mac os x` |
| `android` | `android` `apk` |
| `ios` | `ios` `iphoneos` `ipados` |
| `web` | `web` `wasm` `webassembly` |
| — | 也支持 `freebsd` `openbsd` `netbsd` |

### 架构（arch）

| 规范 id | 接受的写法 |
| --- | --- |
| `x64` | `x64` `amd64` `x86_64` `x86-64` `x8664` `64` `64bit` |
| `x86` | `x86` `i386` `i486` `i586` `i686` `386` `ia32` `win32` `32bit` |
| `arm64` | `arm64` `aarch64` `arm64e` `armv8` `armv8l` `arm64v8` |
| `arm32` | `arm` `arm32` `armv6` `armv7` `armv7l` `armhf` `armeabi-v7a` |
| `universal` | `universal` `universal2` `noarch` `all` `any` `fat` |
| — | 也支持 `riscv64` `s390x` `ppc64le` `mips64` |

API 查询参数同样接受这些别名，站点会归一化后再匹配；
`arch=universal` 的产物能匹配任何架构请求。

---

## 6. 校验和（SHA-256）来源优先级

从高到低，命中即停止：

1. `release.json` 中该条目的 `sha256` 字段
2. 同名旁挂文件 `<文件名>.sha256`（也接受 `.sha256sum` `.sha1` `.md5` `.sums`）
3. `SHA256SUMS` 清单文件（也接受 `SHA256SUMS.txt` `checksums.txt` `checksums.sha256` `sums.txt`），
   格式为 `<哈希>  <文件名>`，兼容 `sha256sum` 输出的 `*` 二进制标记与 `./` 前缀
4. 站点自行计算 —— 结果按「路径 + 大小 + mtime」缓存，写入 `CACHE_DIR/sha256.json`，
   容器重启后不会重复计算

哈希计算在后台进行，不阻塞页面和 API。大文件首次出现时，该条目的
`sha256` 可能短暂为空，下一轮扫描会补上。

---

## 7. 更新说明（Changelog）来源优先级

1. 版本目录下的 `CHANGELOG.md`
2. `changelog.md`、`CHANGES.md`、`changes.md`
3. `RELEASE_NOTES.md`、`release-notes.md`、`RELEASENOTES.md`
4. `NOTES.md`、`notes.md`
5. `release.json` 的 `notes` 字段

说明文本以 Markdown 渲染在网页上，同时以原文通过 API 的 `notes` 字段返回。
支持的语法：标题、有序/无序列表、引用、代码块与行内代码、粗体、斜体、删除线、
链接、分隔线。原始 HTML 会被转义，不会被当作标签执行。

---

## 8. 下载路径与通道

站点的下载地址有两种形式：

```
/dl/<app-id>/<版本号>/<文件名>     # 固定版本，长期有效
/dl/<app-id>/latest/<文件名>       # 解析到当前最新版本
```

文件名可以用相对路径（含子目录），也可以只写文件名 —— 大小写不敏感匹配。

**通道**：一个应用可以有多个通道（如 `stable`、`beta`）。通道写在
`release.json` 的 `channel` 字段里，默认取 `app.json` 的 `channel`，
再默认 `stable`。查询时用 `?channel=beta` 指定。

`latest` 的语义是**该通道内版本号最高的版本**，预发布版按语义化版本排序
天然低于同号正式版（`1.3.0-rc.1` 高于 `1.2.0`，低于 `1.3.0`）。
`/check` 接口默认**不返回预发布版**，避免自动更新把用户带到候选版；
需要时传 `?prerelease=true`。

---

## 9. 给 AI / 脚本的发布模板

生成发布脚本时，最小可靠流程是：

```bash
APP=myapp
VERSION=1.2.0
ROOT=/path/to/archive/apps          # 对应容器的 /data/apps

mkdir -p "$ROOT/$APP/$VERSION"
cp dist/MyApp-$VERSION-windows-x64.exe   "$ROOT/$APP/$VERSION/"
cp dist/MyApp-$VERSION-linux-amd64.tar.gz "$ROOT/$APP/$VERSION/"
cp notes/$VERSION.md "$ROOT/$APP/$VERSION/CHANGELOG.md"

# 校验和（可选但推荐）
( cd "$ROOT/$APP/$VERSION" && sha256sum * > SHA256SUMS )

# 立刻生效，不必等扫描周期（可选）
curl -fsS -X POST https://update.example.com/api/v1/rescan
```

仓库自带的 `scripts/publish.sh` 封装了以上全部步骤：

```bash
./scripts/publish.sh -a myapp -v 1.2.0 \
    -r /path/to/archive/apps \
    -n notes/1.2.0.md \
    -t "体验优化版" -m \
    -u https://update.example.com \
    dist/MyApp-1.2.0-*
```

### 命名建议

为了让站点自动识别尽量少依赖 `release.json`，建议安装包文件名遵循：

```
<产品名>-<版本号>-<平台>-<架构>.<扩展名>
```

- 平台、架构用小写，与第 5 节的规范 id 一致
- 版本号在文件名和目录名中保持一致
- 平台/架构词用 `-` 或 `_` 分隔，不要和产品名粘连

即使用了别的命名，只要文件名里出现平台和架构词，站点仍能识别。
