# 许可证与第三方署名

MySpotify 本身以 **AGPL-3.0** 发布，完整条文见根目录的 `LICENSE`。
下面列出编进程序的第三方代码、版本、许可证，以及对应原文所在位置；原文都在 `licenses/` 目录。分发或再发布本程序（含修改版）时，请一并保留 `LICENSE`、`licenses/` 和本文件。

## 第三方组件

| 组件 | 版本 / 提交 | 许可证 | 原文 |
| --- | --- | --- | --- |
| Go 标准库 | go1.27.3 | BSD-3-Clause | `licenses/go-LICENSE`、`licenses/go-PATENTS` |
| github.com/coder/websocket | v1.8.15 | ISC | `licenses/github.com_coder_websocket-LICENSE.txt` |
| github.com/dhowden/tag | 3d75831295e8 | BSD-3-Clause | `licenses/github.com_dhowden_tag-LICENSE` |
| github.com/ebitengine/purego | v0.11.1 | Apache-2.0 | `licenses/github.com_ebitengine_purego-LICENSE` |
| github.com/egoist/mygo | v0.6.5 预发布 | MIT | `licenses/github.com_egoist_mygo-LICENSE` |
| ├ internal/shader、internal/shaderir（源自 Ebitengine Kage） | — | Apache-2.0 | `licenses/github.com_egoist_mygo_internal_shader-LICENSE` |
| github.com/go-text/typesetting | v0.3.5 | Unlicense OR BSD-3-Clause | `licenses/github.com_go-text_typesetting-LICENSE` |
| golang.org/x/image | v0.46.0 | BSD-3-Clause | `licenses/golang.org_x_image-LICENSE`、`-PATENTS` |
| golang.org/x/net | v0.59.0 | BSD-3-Clause | `licenses/golang.org_x_net-LICENSE`、`-PATENTS` |
| golang.org/x/sys | v0.49.0 | BSD-3-Clause | `licenses/golang.org_x_sys-LICENSE`、`-PATENTS` |
| golang.org/x/text | v0.43.0 | BSD-3-Clause | `licenses/golang.org_x_text-LICENSE`、`-PATENTS` |
| hls.js | `internal/player/web/hls.min.js` | Apache-2.0 | `licenses/hls.js-LICENSE` |
| OpenCC 繁简字表 | `internal/lyrics/TSCharacters.txt` | Apache-2.0 | `licenses/OpenCC-LICENSE` |
| Lyricify Lyrics Helper（QRC DES 解密算法参考） | `3fc579b029c24a1f54ebb2fd002ee3624c85c7e7` | Apache-2.0 | `licenses/Lyricify-Lyrics-Helper-LICENSE` |
| Lyricify Backgrounds | `cbef8a667c02a01707c78c7e76dc67d65cf161ed` | Apache-2.0 | `licenses/Lyricify-Backgrounds-LICENSE.txt`、`-NOTICE.txt` |
| Dynamic Lyrics Island 概念 | — | CC BY-SA 4.0 | `licenses/Lyricify-Backgrounds-NOTICE.txt` |

## 外观与歌词组件的署名

- **Lyricify Lyrics Helper**（WXRIW / Lyricify 贡献者）：纯 Go 歌词引擎中的 QQ 音乐 QRC DES 解密表与匹配思路参考自其 `3fc579b029c24a1f54ebb2fd002ee3624c85c7e7` 实现，以 Apache-2.0 署名；已不再嵌入 `.NET` 运行时或 C# 子进程。
- **Lyricify Backgrounds**（WXRIW / Lyricify 贡献者）：动态背景改编自 `cbef8a667c02a01707c78c7e76dc67d65cf161ed` 的 iOS Classic 效果，以 Apache-2.0 使用。
- **动态歌词岛概念**：词岛与桌面歌词外观参考 Lyricify 官方截图，概念署名 Lyricify，相关视觉改编以 CC BY-SA 4.0 共享。这不改变程序其他源码的许可证。

## 关于 Spotify

本程序是个人项目，与 Spotify 没有任何关系，也未获其认可。它通过 Spotify 网页播放器自身的内部接口工作，而不是公开 API；Spotify 可能随时改动接口，且这种用法很可能不符合 Spotify 的用户条款，账号存在被限制的风险。本程序不包含 Spotify 的代码、音频或内容，请只用自己的账号并自行承担后果。

## 保持完整

`go.mod` 或内嵌组件版本有变动时，请同步更新本文件与 `licenses/`。第三方许可证原文以其各自文件为准。
