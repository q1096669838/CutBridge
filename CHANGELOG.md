# Changelog

## 0.3.1 - 2026-07-14

- 修复 Premiere XML 转 FCPXML 时立体声音频重复的问题。
- 为合并后的立体声显式写入 `srcCh="1,2"` 与 `outCh="L,R"`。
- 保留 0.3.0 的 FCPXML → Premiere XML 双向转换功能。

## 0.3.0 - 2026-07-14

- 新增 FCPXML / FCPXMLD → Premiere Final Cut Pro 7 XML。
- 自动识别输入格式和转换方向。

## 0.2.4 - 2026-07-14

- 合并 Premiere 导出的左右声道片段，避免逻辑音频重复。

## 0.2.3 - 2026-07-14

- 修复简写 `<file id="..."/>` 覆盖完整媒体定义的问题。
- 补充素材格式资源并跳过无法转换的 Premiere 生成器。

## 0.2.2 - 2026-07-14

- 将最低视频轨放入 FCP 主故事线。
- 合并同源视音频剪辑。

## 0.2.1 - 2026-07-14

- 改为 Universal 2 单一主程序，修复 App Translocation 路径问题。
