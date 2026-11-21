<div align="center">

<img src="docs/icon.png" width="88" alt="Sonora">

# Sonora v1.0 · Windows & macOS 独立定制版

**流光液态玻璃 · 逐字双语律动 · 可拖跨屏灵动岛 · 本地音乐 · 歌单吸顶搜索**

源于 MySpotify，已独立命名、更换全新图标并隔离配置与实例，支持 **Windows 10/11 x64** 与 **macOS（Apple Silicon / Intel 通用）**，把流光播放页、双语逐字歌词、跨屏灵动岛、本地听歌与大歌单找歌打磨到开箱即用。

**简体中文** · [English](README.en.md)

[![最新版本](https://img.shields.io/badge/version-v1.0.0-ef3b45?style=flat-square)](https://github.com/hfx123456/Sonora/releases/latest)
![Windows x64](https://img.shields.io/badge/Windows-10%20%2F%2011%20x64-3a6ea5?style=flat-square)
![macOS Universal](https://img.shields.io/badge/macOS-Universal%20(arm64%20%2F%20x64)-111111?style=flat-square)
[![AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-c2410c?style=flat-square)](LICENSE)

**[下载 Sonora](https://github.com/hfx123456/Sonora/releases/latest)** · [功能详情](FUSION.md) · [更新日志](CHANGELOG.md) · [上游项目](https://github.com/margbug01/MySpotify)

<img src="docs/screenshots/stage-bilingual.webp" width="960" alt="Sonora v1.0 律动模式：Apple Music 风格 GPU 流光背景、液态玻璃控制栏与双语逐字弹跳发光">

</div>

本仓库由 **hfx123456** 维护，基于 [margbug01/MySpotify](https://github.com/margbug01/MySpotify) 的源码加入深度定制。下面重点介绍 **Sonora v1.0** 的核心特性；Spotify 浏览、在线播放、MV、播客等基础能力来自上游。本分支发布 **Windows 10/11 x64** 与 **macOS Universal（Apple Silicon / Intel）** 构建。

---

## 1. 流光背景、液态玻璃与双语逐字「律动」

正在播放页全面升级视觉与歌词动效：

- **Apple Music 风格 GPU 流光动态背景（Flow）**：根据封面调色板实时生成流动网格渐变，随音乐节奏呼吸漂移。
- **iOS 风格液态玻璃（Liquid Glass）**：统一顶部切换器、律动页播控栏与悬浮控件质感，可在「设置」中随时开关并连续调节模糊强度。
- **「律动」模式双语逐字同步动效**：不仅英文/日韩原词逐字散落弹跳、亮起封面主色微光并缓慢漂移，**中文译文也会按原词演唱时间轴同步逐字飞入、发光与漂移**；顶部标签与底部播控条在指针移入时才浮现，移开自动隐去。
- **「歌词」滚动模式句内扫光**：当前句支持词内连续卡拉 OK 扫光、轻微抬升与柔光，中文译文同步逐字扫亮；非当前行适度柔化，暂停及唱词间隙停止连续重绘以降低功耗。

<table>
  <tr>
    <td width="50%"><img src="docs/screenshots/stage-bilingual.webp" alt="律动模式双语逐字动效"><br><b>律动模式</b>：流光背景 + 液态玻璃控件 + 原文与译文逐字弹跳发光。</td>
    <td width="50%"><img src="docs/screenshots/nowplaying-glass.webp" alt="普通歌词滚动模式逐字扫光"><br><b>歌词模式</b>：句内连续扫光与译文同步扫亮。</td>
  </tr>
</table>

---

## 2. 可自由拖动、跨屏移动的灵动岛与桌面歌词

播放栏最右侧的 **「Fusion 双语歌词」** 按钮提供三种形态：

| 显示方式 | 使用体验 |
| --- | --- |
| **灵动岛（顶部胶囊）** | 默认贴在屏幕物理上沿，显示当前句、译文（或中文歌下一句预览）与封面，随句长以弹簧动画平滑伸缩。**支持按住自由拖动并跨越多块显示器**：拖离顶部自动变为圆角悬浮胶囊，拖回任意屏幕顶部边缘自动贴顶吸附；多屏环境下右键菜单还提供「词岛移至下一屏幕」。 |
| **透明桌面双行** | 细描边文字直接浮在其他窗口上方，上排高亮当前句，下排预览下一句中文歌词或显示外语中文译文；未锁定时悬停显示半透明黑底控制栏，便于拖放与切歌。 |
| **内部双语面板** | 在应用内查看完整双语歌词，点击任意一句即可跳转进度，背景随封面色彩流动。 |

### 锁定点击穿透与快捷解锁

- **锁定后点一下直接浮出「解锁」**：开启「锁定桌面歌词（点击穿透）」后，桌面歌词与灵动岛保持全区域点击穿透，不会误挡背后窗口操作；把鼠标移入或点按歌词，上方会浮出一个精致的「**解锁**」胶囊按钮，且不显示多余黑框，直接点一下即可恢复拖动与右键菜单。
- **暂停自动收起**：暂停时灵动岛与透明桌面歌词自动隐去，继续播放时恢复。
- **字体与尺寸独立调节**：歌词字体与中文字体可分开选择，字号、词岛宽度、位置与偏好自动保存。

<table>
  <tr>
    <td width="50%"><img src="docs/screenshots/island-floating.webp" alt="可自由拖动跨屏的悬浮灵动岛"><br><b>悬浮灵动岛</b>：拖离屏幕顶部自动切换为圆角胶囊，支持跨屏拖动。</td>
    <td width="50%"><img src="docs/screenshots/fusion-locked-unlock.webp" alt="锁定点击穿透后浮出解锁按钮"><br><b>锁定快捷解锁</b>：锁定后仅浮出「解锁」胶囊，无多余黑框遮挡。</td>
  </tr>
  <tr>
    <td width="50%"><img src="docs/screenshots/island.webp" alt="贴顶双语词岛"><br><b>贴顶词岛</b>：贴合显示器物理上沿，随句长弹性伸缩。</td>
    <td width="50%"><img src="docs/screenshots/desktop-lyrics.webp" alt="透明桌面双行歌词"><br><b>透明桌面双行</b>：无底色描边直叠在其他窗口上方。</td>
  </tr>
</table>

---

## 3. 内置多源双语检索与导入

内置 **Lyricify Lyrics Helper** 检索与解析组件，自动在 **网易云音乐、QQ 音乐、LRCLIB** 中查找匹配歌词，在线歌曲与本地歌曲均支持：

- **跨文字体系与别名匹配**：支持罗马音及跨文字体系歌曲（如带括号跨语种译名/别名），缺失译文时自动尝试网易云补查翻译，绝不拿下一句原词充当翻译。
- **逐字 / 逐行自适应**：优先采用真实逐字时间轴；仅有逐行时间轴时在句内分配近似视觉进度。
- **手动导入与时间微调**：支持导入 `LRC / TTML / YRC / QRC / LYS / LYL`，支持一键重新查找绕过缓存，以及全局 ±0.5 秒同步微调。
- **开箱即用**：发行版内嵌歌词助手与 .NET 运行时，无需额外安装 .NET 或运行 Lyricify Fusion。

详细机制见 [FUSION.md](FUSION.md)。

<details>
<summary>查看歌词菜单</summary>

<img src="docs/screenshots/fusion-menu.webp" width="247" alt="歌词菜单：模式、动态效果、鼠标避让、中文译文、字体、宽度、跨屏移动、时间偏移与导入">

</details>

---

## 4. 大歌单里的吸顶搜索框与歌单排序

给歌单与本地音乐统一了胶囊搜索体验，向下滚动后自动吸附在顶部：

- **边输边筛 & 简繁互搜**：同时匹配歌名、歌手、专辑，支持简体与繁体双向匹配、空格多词联合筛选（英文忽略大小写），输入空格不误触播放暂停。
- **保留完整队列**：筛选后双击任意结果，仍按原歌单/本地库完整队列顺序播放。
- **歌单排序一致**：保持显示顺序、搜索结果与播放队列严格同步。

<img src="docs/screenshots/playlist-search.webp" width="860" alt="歌单内吸顶搜索框">

---

## 5. 本地音乐与「音乐云盘」（跨设备同步）

侧栏提供 **「本地音乐」**，同时支持本机曲库与网易云风格的 **个人音乐云盘**：

- **本机曲库**：递归扫描 `MP3 / FLAC / M4A / WAV / OGG / Opus / AAC`，读取内嵌封面、标签、内嵌歌词及同名 `.lrc`；无需登录 Spotify 即可播放。
- **6 大云盘直连**：原生支持 **OneDrive / Microsoft 365**、**Google Drive**、**百度网盘**、**夸克网盘**、**115网盘** 与 **WebDAV**（内置坚果云 / 123云盘 / Alist 快捷预设，支持群晖 / 飞牛 NAS）。
- **极速扫盘 + 边播边缓存**：启动秒读本地索引缓存；点播云盘歌曲时边播边自动缓存到本机，再次播放与拖进度条零延迟；支持自动点亮 Spotify 歌单中的 `spotify:local:` 灰歌，以及本机歌曲右键一键上传到云盘。
- **隐私安全**：所有云盘凭据与刷新令牌均使用系统钥匙串（Windows DPAPI / macOS Keychain）加密保存在本机，不经过任何第三方服务器。

### 云盘快速配置说明（全部免费，不绑卡）

- **夸克网盘 / 115网盘**：无需申请任何 API，点「添加云盘」→ 选对应网盘 → 点击 **「扫码登录」**，在弹出的官网窗口扫码即可自动完成连接。
- **WebDAV（坚果云 / 123云盘 / Alist / NAS）**：点「添加云盘 → WebDAV」，选用快捷预设或填入 WebDAV 地址与账号密码即可。
- **OneDrive / Microsoft 365**（每人免费申请自己的 Client ID，1 分钟搞定）：
  1. 打开 [Azure 应用注册](https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade) → 点 **「新注册」**（无需开通 Azure 付费订阅或绑卡）。
  2. 名称填 `Sonora`，受支持的账户类型选 **「任何组织目录中的账户和个人 Microsoft 账户」**，重定向 URI 平台选 **「公共客户端/本机（移动和桌面）」** 并填 `http://127.0.0.1`。
  3. 复制生成的 **「应用程序(客户端) ID」** 填入 Sonora 即可一键浏览器授权。
- **Google Drive**：在 [Google Cloud Console](https://console.cloud.google.com/apis/dashboard) 启用 Google Drive API → 在「OAuth 同意屏幕」添加自己的 Gmail 为测试用户 → 创建 **「桌面应用」** OAuth 客户端 ID，将 Client ID 与 Secret 填入 Sonora 即可。
- **百度网盘**：在百度网盘开放平台免费创建应用（回调地址填 `http://127.0.0.1`），将 `AppKey` 与 `SecretKey` 填入 Sonora 即可。

---

## 下载与开始使用

1. 打开 **[Sonora Releases](https://github.com/hfx123456/Sonora/releases/latest)**：
   - **Windows**：推荐下载一键安装包 `sonora-setup-1.0.1-amd64.exe`（自动创建快捷方式并按需安装 WebView2）；或下载便携版 `Sonora-1.0.1-windows-amd64.zip` 解压运行。
   - **macOS**：下载通用镜像 `sonora-1.0.1-universal.dmg`（支持 Apple Silicon 与 Intel），拖入「应用程序」即可使用；因未进行 Apple 公证，首次打开请右键「打开」或在「系统设置 → 隐私与安全性」中允许运行。
2. 两个平台均已内嵌 Lyricify 歌词助手与 .NET 运行时，**无需额外配置任何环境**。

## 数据隔离与更新

- **独立双开不冲突**：Sonora 使用独立单实例锁、自定义图标、独立应用标识（`dev.hfx123456.sonora`）及独立数据目录（`%APPDATA%\Sonora` / `%LOCALAPPDATA%\Sonora`），可与原版 MySpotify 并行安装运行；首次启动自动平滑迁移原配置与缓存且保留原目录不动。
- **独立更新源**：使用 `hfx123456/Sonora` 发布源与自有更新签名公钥，可在「设置 → 关于」检查更新。

## 上游基础能力与声明

Spotify 首页浏览、搜索、歌单、专辑、艺人、喜欢与收藏、在线播放、MV、视频播客、系统媒体键、全局快捷键、托盘及主题等基础能力来自上游 [margbug01/MySpotify](https://github.com/margbug01/MySpotify)，保留 **AGPL-3.0** 协议。

Sonora 为非官方个人项目，与 Spotify 无关；在线功能依赖网页播放器内部接口，请仅使用自己的账号。

## 开发与源码构建

```powershell
$env:CGO_ENABLED = '0'
go test ./internal/...
go build -trimpath -ldflags='-H windowsgui -s -w' -o Sonora.exe .
```

更多说明见 [DEVELOPMENT.md](DEVELOPMENT.md)、[FUSION.md](FUSION.md) 与 [CHANGELOG.md](CHANGELOG.md)。
