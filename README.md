<div align="center">

<img src="docs/icon.png" width="88" alt="Sonora">

# Sonora · Windows 独立定制版

**贴顶词岛 · 双语逐字歌词 · 本地音乐 · 歌单吸顶搜索**

源于 MySpotify，已独立命名、更换全新图标并隔离配置与实例，把桌面看词、本地听歌和大歌单找歌做得更顺手。

**简体中文** · [English](README.en.md)

[![最新版本](https://img.shields.io/github/v/release/hfx123456/Sonora?style=flat-square&label=release&color=ef3b45)](https://github.com/hfx123456/Sonora/releases/latest)
![Windows x64](https://img.shields.io/badge/Windows-10%20%2F%2011%20x64-3a6ea5?style=flat-square)
[![AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-c2410c?style=flat-square)](LICENSE)

**[下载 Sonora](https://github.com/hfx123456/Sonora/releases/latest)** · [功能详情](FUSION.md) · [更新日志](CHANGELOG.md) · [上游项目](https://github.com/margbug01/MySpotify)

<img src="docs/screenshots/island.webp" width="960" alt="实际运行截图：屏幕上沿的双语词岛，可在浏览歌曲时继续看词">

</div>

本仓库由 **hfx123456** 维护，基于 [margbug01/MySpotify](https://github.com/margbug01/MySpotify) 的源码加入个人定制。下面重点介绍本分支增加和调整的功能；Spotify 浏览、在线播放、MV、播客等基础能力来自上游。本分支当前发布 **Windows 10/11 x64** 构建。

## 1. 把双语歌词放到桌面上

播放栏最右侧的 **「Fusion 双语歌词」** 按钮可以打开三种显示方式：

| 显示方式 | 使用体验 |
| --- | --- |
| **顶部词岛** | 胶囊贴在屏幕物理上沿，显示当前句、中文译文和封面；也可切成可拖动的悬浮胶囊。 |
| **透明桌面双行** | 原文在上、中文译文在下，细描边文字浮在窗口上方，可以拖到自己习惯的位置。 |
| **内部双语面板** | 在应用内看完整歌词，点击一句跳到对应位置，背景随封面颜色流动。 |

这些显示方式还做了以下定制：

- **暂停就收起**：暂停时词岛、透明桌面歌词自动隐藏，继续播放再出现，上一句不会一直留在桌面上。
- **鼠标靠近自动避让**：贴顶词岛在鼠标移入时淡出并允许点击穿透，移开后恢复；可以从菜单关闭避让。
- **可锁定、可穿透**：桌面歌词锁定后，点击会传给后面的窗口；从播放栏菜单或托盘可解锁。
- **字体分开选**：歌词字体和中文字体可独立设置；字号、词岛宽度分别调整，位置和显示偏好会保存。
- **单行、双行自适应**：中文歌只显示原词；外语歌有中文译文才显示第二行，缺译文不拿下一句凑数，也不显示「暂无中文翻译」。
- **前奏也流畅**：修复歌名、歌手、作词作曲介绍阶段刷新不足导致的卡顿；动态模式下该阶段按 30 帧刷新。

<table>
  <tr>
    <td width="50%"><img src="docs/screenshots/island.webp" alt="顶部词岛实际效果"><br><b>贴顶词岛</b>：浏览歌曲时继续看原文与中文译文。</td>
    <td width="50%"><img src="docs/screenshots/desktop-lyrics.webp" alt="透明桌面双行歌词实际效果"><br><b>透明双行</b>：没有胶囊底色，直接叠在其他窗口上方。</td>
  </tr>
</table>

## 2. 歌词不只是多一行翻译

内置 **Lyricify Lyrics Helper** 检索与解析组件，自动在 **网易云音乐、QQ 音乐、LRCLIB** 中查找匹配歌词，在线歌曲和本地歌曲都能使用。按歌名、歌手、专辑和时长核对版本，优先选择同步歌词，再比较中文译文和逐字数据。

- **逐字扫亮和换句过渡**：支持扫亮、上浮、轻微缩放和柔光，中文译文也参与动画；菜单可关闭动态效果。
- **有逐字数据就用真实时间轴**；只有逐行数据时，句内扫亮属于近似视觉效果，不代表真实音节对齐。
- **手动导入歌词**：支持 `LRC / TTML / YRC / QRC / LYS / LYL`，也能一键重新查找，绕过旧缓存。
- **微调同步时间**：菜单可每次提前或延后 0.5 秒，并可重置；这套桌面歌词的时间偏移是全局设置。
- **本地歌词优先利用**：本地歌曲的同名 LRC 或内嵌歌词含中文时会优先使用；手动导入只对当前歌曲会话有效。
- **无需另开 Lyricify**：发行版内嵌歌词助手及 .NET 运行时，无需单独安装 .NET，也不需要运行 Lyricify Fusion。

中文翻译来自歌词源，未接入机器翻译，不能保证每首外语歌都有译文。查询只发送歌名、歌手、专辑和时长，不上传本地音频。详细行为与实现见 [FUSION.md](FUSION.md)。

<details>
<summary>查看实际歌词菜单</summary>

<img src="docs/screenshots/fusion-menu.webp" width="247" alt="歌词菜单：模式、动态效果、鼠标避让、中文译文、字体、宽度、时间偏移和歌词导入">

</details>

## 3. 大歌单里的吸顶搜索框

给歌单增加独立的 **「搜索此歌单」** 输入框，放在歌单介绍下方，向下滚动后吸附在内容顶部，随时都能继续搜索。

| 细节 | 实际行为 |
| --- | --- |
| **边输入边筛选** | 同时匹配歌名、歌手、专辑，显示匹配数量，可一键清空恢复完整列表。 |
| **简繁都能找** | 歌单里写「周杰倫」，输入「周杰伦」也能匹配，反过来也一样；显示名称保持原样。 |
| **多个词一起搜** | 空格分词，各词都匹配才保留；例如「周杰伦 晴天」可同时匹配歌手和歌名，英文不区分大小写。 |
| **输入不打断播放** | 在搜索框里输入空格不会误触暂停。 |
| **播放队列保持完整** | 双击筛选结果，从它在原歌单中的位置开始播放，不把队列截成筛出来的几首。 |
| **各歌单分别记住搜索词** | 当前运行期间，切换歌单不会把一个歌单的查询套到另一个歌单上。 |

<img src="docs/screenshots/playlist-search.webp" width="860" alt="实际歌单内搜索：输入 bigbang 后筛选当前歌单，并显示找到 22 首">

**这里的简繁互搜指歌单内筛选**，不是对 Spotify 全站搜索作额外保证。

## 4. 不登录也能播放电脑里的音乐

侧栏增加 **「本地音乐」**，可以添加整个文件夹（含子目录）或单独的歌曲，直接从文件原位置播放。

- 支持扫描 `MP3 / FLAC / M4A / WAV / OGG / Opus / AAC`；实际解码取决于播放组件对文件编码的支持。
- 读取歌曲标签、内嵌封面、内嵌歌词和同名 `.lrc`，可按歌名、歌手、专辑筛选。
- 使用同一套播放队列、随机、循环、进度、音量和桌面歌词；播放本地文件无需 Spotify 登录，在线查词仍需要网络。
- Spotify 歌单中的 `spotify:local:` 曲目，会按歌名、歌手、专辑与已导入文件唯一匹配；匹配不到或有歧义时不会随意播放别的文件。
- 移除来源只移出音乐库，不删除原文件；文件增删后可点「重新扫描」。OneDrive 云端文件需先下载到本机。

本地歌曲不通过 Spotify Connect 接力，不支持 Apple Music 受保护的 `.m4p` 文件。

## 下载与开始使用

1. 打开 **[Sonora Releases](https://github.com/hfx123456/Sonora/releases/latest)**，下载对应版本的 `Sonora-<版本>-windows-amd64.zip` 或 `sonora-<版本>-windows-amd64.tar.gz`。
2. 完整解压，保留 `Sonora.exe` 旁的 `LICENSE`、`licenses/` 和其他随附文件，再运行 `Sonora.exe`。
3. 在线 Spotify 登录与播放需要 **Microsoft Edge WebView2 Runtime**。缺少时从[微软官方下载页](https://developer.microsoft.com/microsoft-edge/webview2/)安装。
4. 听本地歌：侧栏「本地音乐」→「添加文件夹」或「添加歌曲」。开桌面歌词：播放栏最右侧「Fusion 双语歌词」→ 选择模式。

## Sonora 怎样更新与隔离数据

本仓库使用 **`hfx123456/Sonora` 的发布源和自己的更新签名公钥**。使用本仓库签名构建后，可在「设置 → 关于」检查更新；更新包的签名由程序核验。

**Sonora 与上游同名程序互相独立**：拥有独立单实例锁、图标和配置目录，可与原版并行安装运行；首次从原版迁移时会自动平滑复制原有设置、播放记录、缓存和登录态（原目录保留不动）。

## 上游基础能力与使用边界

本分支继续保留上游的 Spotify 首页、搜索、歌单、专辑、艺人、喜欢与收藏、在线播放、MV、视频播客、滚动歌词、系统媒体键、全局快捷键、托盘及主题等能力。这些不是本分支新开发的功能。

Sonora 是非官方个人项目，与 Spotify 没有关联或认可。在线功能依赖 Spotify 网页播放器内部接口，可能随接口变化失效，也可能不符合其服务条款，账号存在被限制的风险。Spotify 音频仍通过系统网页组件的 DRM 模块解密；缓存的是加密分段，不等于可离线下载的音频。请只使用自己的账号。

## 配置、开发与问题定位

Windows 的配置、播放状态和日志位于 `%APPDATA%\Sonora`，登录态与缓存位于 `%LOCALAPPDATA%\Sonora` 的相应子目录（首次启动若存在旧 `%APPDATA%\Vinyl` 会自动迁移且保留原目录不动）。替换程序前可以备份这些目录，**不要把登录数据提交到公开仓库**。

- [内置歌词说明与构建](FUSION.md)：显示模式、匹配策略、缓存和组件构建。
- [开发说明](DEVELOPMENT.md)：主程序构建与调试。
- [源码包说明](README-未编译源码.md)：源码整理时的背景和构建参考；其中日期及验证记录属于当时状态。
- [更新日志](CHANGELOG.md)：本仓库版本的变更记录。

## 来源、致谢与许可证

- **MySpotify 上游**：[margbug01/MySpotify](https://github.com/margbug01/MySpotify)，本分支基于其代码定制，保留原项目署名和 **AGPL-3.0** 许可证。
- **原生界面**：[MyGo](https://github.com/egoist/mygo)；上游界面源自 [egoist/mygo-cloudmusic](https://github.com/egoist/mygo-cloudmusic)。
- **歌词检索与解析**：[Lyricify Lyrics Helper](https://github.com/WXRIW/Lyricify-Lyrics-Helper)，Apache-2.0。
- **动态背景适配**：[Lyricify Backgrounds](https://github.com/WXRIW/Lyricify-Backgrounds)，Apache-2.0；词岛外观参考与概念署名 Lyricify，相关说明见第三方声明。
- **其他依赖**：包括 OpenCC 字表、hls.js、Go/.NET 组件等，版本、协议与原文位置见 [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md) 和 [licenses/](licenses/)。

本分支自行绘制歌词窗口，没有使用未公开的 Lyricify Fusion 完整界面源码。程序许可证见 [LICENSE](LICENSE)；分发时请保留许可证和第三方声明。Spotify 商标、截图中的歌曲封面和艺人照片属于各自权利人。
