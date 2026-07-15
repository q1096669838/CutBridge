# CutBridge

CutBridge 是一款面向 macOS 的开源双向时间线转换工具，在本地转换剪辑工程 XML，不上传时间线或媒体路径。

## 支持的转换方向

- Premiere Pro / DaVinci Resolve 的 Final Cut Pro 7 XML（`xmeml version="4"`）→ Final Cut Pro FCPXML
- Final Cut Pro FCPXML / FCPXMLD → Premiere Pro 可导入的 Final Cut Pro 7 XML

软件会根据 XML 根节点自动判断转换方向。

## 当前版本

`v0.3.1`

## 已实现

- Apple Silicon 与 Intel Mac（Universal 2）
- 主故事线与连接片段/固定轨道映射
- 素材路径、剪辑入出点、帧率、分辨率和基础时间码
- Premiere 重复 `<file id="..."/>` 引用解析
- Premiere 立体声声道对去重及 `srcCh/outCh` 映射
- FCPXML 转 Premiere 固定视频轨和音频轨
- 转换兼容性报告
- 图形界面和命令行入口

## 已知限制

以下内容通常无法一比一迁移，需要在目标软件中复查或重建：

- Lumetri、Final Cut Pro 调色和 DaVinci Resolve 调色节点
- Fusion、MOGRT、专有标题和第三方插件
- 多机位、复合片段、复杂嵌套和调整图层
- 复杂变速、速度曲线、部分转场及音频插件

## macOS 构建

需要 macOS、Xcode Command Line Tools 和 Go 1.23 或更高版本：

```bash
chmod +x scripts/build_macos.sh
./scripts/build_macos.sh
```

输出：

```text
dist/CutBridge.app
dist/CutBridge_macOS_0.3.1.zip
```

构建结果默认不包含 Apple Developer ID 签名和公证。

## 测试

```bash
go test ./...
```

## 命令行

```bash
go run ./cmd/cutbridge --input "/path/to/timeline.xml"
```

指定输出：

```bash
go run ./cmd/cutbridge \
  --input "/path/to/timeline.fcpxml" \
  --output "/path/to/timeline_for_Premiere.xml"
```

## 隐私

CutBridge 完全在本机读取和写入 XML，不上传工程、媒体路径或素材。

## 商标声明

CutBridge 是独立开源项目，与 Apple、Adobe 或 Blackmagic Design 不存在隶属、授权或合作关系。Final Cut Pro、Premiere Pro 和 DaVinci Resolve 是各自权利人的商标。

## License

齐哥来啦！
