# MySpotify 未编译工程源码

打包日期：2026-11-11。这是当前开发工作树的完整源码，包含尚未提交的本地音乐、内置双语歌词、歌词动画、可调大小词岛和翻译逻辑修改。可以直接用编辑器打开并继续修改。

本包刻意排除了所有编译产物：`MySpotify.exe`、`internal/fusion/bridge.bin`、`rsrc_windows_amd64.syso`、DLL、PDB、`bin`、`obj` 和运行缓存。歌词助手的完整 C# 源码在 `tools/fusion-bridge`，它依赖的 Lyricify 源码在 `third_party`。Go 依赖以源码形式放在 `vendor`。

不含办公室电脑的账号、Cookie、浏览器数据、个人歌曲、播放记录、应用配置或日志。`internal/player/testdata` 里的小型音视频是项目原有测试素材。

## 环境

- Windows 10/11 x64。
- Go 1.27.3 或更高兼容版本；本次验证使用 Go 1.27.4 windows/amd64。
- 修改歌词助手时需要 .NET 8 或更高兼容 SDK。
- 运行程序时需要 Microsoft Edge WebView2 Runtime。

## 首次构建

`internal/fusion/bridge.go` 通过 `go:embed` 引用 `bridge.bin`，所以首次编译前必须先从 C# 源码生成歌词助手：

```powershell
$lyricifySource = (Resolve-Path '.\third_party\Lyricify-Lyrics-Helper').Path
.\tools\fusion-bridge\Build.ps1 -LyricifySource $lyricifySource
$env:CGO_ENABLED = '0'
go build -mod=vendor -trimpath -ldflags='-H windowsgui -s -w' -o MySpotify.exe .
```

`Build.ps1` 默认生成包含 .NET 运行时的 Windows x64 单文件助手，并写入 `internal/fusion/bridge.bin`。第一次恢复 NuGet 包和运行时组件需要网络；以后可复用本机缓存。`-FrameworkDependent` 会生成需要另装 .NET 的小体积版本。

如需带程序图标、版本信息和 manifest，按 `DEVELOPMENT.md` 中的 `go-winres` 命令重新生成 `rsrc_windows_amd64.syso`；普通 `go build` 不依赖这个文件。

## 开发与测试

```powershell
go test -mod=vendor ./internal/fusion -count=1
go test -mod=vendor ./internal/app -run '^TestFusion' -count=5
go test -mod=vendor ./internal/...
```

此前全量测试有两个已知失败：`TestProxySetting` 依赖本机系统代理设置，`TestLongLyricsBuildOnlyWhatShows` 存在已有布局位置差异。其他包此前通过。

`vendor` 使 Go 应用可以离线解析依赖。若修改 `go.mod`，需要联网执行 `go mod tidy` 和 `go mod vendor`。功能及限制详见 `FUSION.md`。

## 代码来源与协议

- MySpotify 基于上游提交 `726da4a58a65441411376212bb4ac14dbfa51e1d` 加当前工作树修改；协议见根目录 `LICENSE`（AGPL-3.0）。
- `third_party/Lyricify-Lyrics-Helper`：提交 `3fc579b029c24a1f54ebb2fd002ee3624c85c7e7`，Apache-2.0。
- `third_party/Lyricify-Backgrounds-reference`：提交 `cbef8a667c02a01707c78c7e76dc67d65cf161ed`，Apache-2.0。
- 第三方许可证和署名保存在 `licenses` 及对应依赖目录。这里没有未公开的 Lyricify Fusion 完整界面源码。

`SHA256SUMS.txt` 包含除自身外所有源码文件的 SHA-256，路径相对本目录。
