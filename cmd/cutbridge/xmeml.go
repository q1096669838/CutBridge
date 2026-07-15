package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxXMLBytes int64 = 150 * 1024 * 1024

var windowsPathRE = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
var windowsURLPathRE = regexp.MustCompile(`^/[A-Za-z]:/`)

func directSequences(root *Node) []*Node {
	out := []*Node{}
	for _, seq := range root.Descendants("sequence") {
		nested := false
		for p := seq.Parent; p != nil; p = p.Parent {
			if p.Name == "clipitem" || p.Name == "sequenceitem" {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, seq)
		}
	}
	return out
}

func richestFiles(root *Node) map[string]*Node {
	out := map[string]*Node{}
	scores := map[string]int{}
	for _, n := range root.Descendants("file") {
		id := n.Attr["id"]
		if id == "" {
			id = n.TextAt("name", "")
		}
		if id == "" {
			continue
		}
		score := n.Count() + len(strings.TrimSpace(n.Text.String()))
		if old, ok := scores[id]; !ok || score > old {
			out[id], scores[id] = n, score
		}
	}
	return out
}

func sanitizeFileURL(raw, baseDir string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(strings.ToLower(raw), "file://localhost/") {
		raw = "file:///" + raw[len("file://localhost/"):]
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme == "file" {
		p, _ := url.PathUnescape(u.Path)
		win := windowsURLPathRE.MatchString(p)
		return (&url.URL{Scheme: "file", Path: p}).String(), win
	}
	if windowsPathRE.MatchString(raw) {
		normalized := strings.ReplaceAll(raw, "\\", "/")
		return (&url.URL{Scheme: "file", Path: "/" + normalized}).String(), true
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		decoded = raw
	}
	p := decoded
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return (&url.URL{Scheme: "file", Path: p}).String(), false
}

func nodeRate(n *Node, fallback Rate) Rate {
	if n == nil {
		return fallback
	}
	tb := n.TextAt("rate/timebase", "")
	if tb == "" {
		return fallback
	}
	return parseRate(tb, n.TextAt("rate/ntsc", ""))
}

func parseFileAsset(fileID string, n *Node, sourceFile string, fallback Rate) (*MediaAsset, bool) {
	name := n.TextAt("name", fileID)
	raw := n.TextAt("pathurl", "")
	if raw == "" {
		raw = n.TextAt("url", "")
	}
	src, win := sanitizeFileURL(raw, filepath.Dir(sourceFile))
	rate := nodeRate(n, fallback)
	durFrames := parseInt(n.TextAt("duration", ""), 0)
	dur := Zero
	if durFrames > 0 {
		dur = framesToSeconds(durFrames, rate)
	}
	media := n.Direct("media")
	var video, audio *Node
	if media != nil {
		video, audio = media.Direct("video"), media.Direct("audio")
	}
	width, height, audioRate, channels := int64(0), int64(0), int64(0), int64(0)
	if video != nil {
		width = parseInt(video.TextAt("samplecharacteristics/width", ""), 0)
		height = parseInt(video.TextAt("samplecharacteristics/height", ""), 0)
	}
	if audio != nil {
		audioRate = parseInt(audio.TextAt("samplecharacteristics/samplerate", ""), 0)
		channels = parseInt(audio.TextAt("channelcount", ""), 0)
		if channels <= 0 {
			channels = int64(len(audio.DirectAll("channel")))
			if channels == 0 {
				channels = 2
			}
		}
	}
	return &MediaAsset{Key: fileID, Name: name, Src: src, Rate: rate, Duration: dur, Start: Zero,
		HasVideo: video != nil, HasAudio: audio != nil, Width: width, Height: height, AudioChannels: channels, AudioRate: audioRate}, win
}

func parseMarkers(n *Node, rate Rate, origin Fraction) []Marker {
	out := []Marker{}
	for _, m := range n.DirectAll("marker") {
		in := parseInt(m.TextAt("in", ""), 0)
		outF := parseInt(m.TextAt("out", ""), in)
		d := outF - in
		if d < 0 {
			d = 0
		}
		out = append(out, Marker{Name: m.TextAt("name", "Marker"), Start: origin.Add(framesToSeconds(in, rate)), Duration: framesToSeconds(d, rate), Note: m.TextAt("comment", "")})
	}
	return out
}

func extractVolumeDB(clip *Node) *float64 {
	for _, filter := range clip.DirectAll("filter") {
		effect := filter.Direct("effect")
		if effect == nil {
			continue
		}
		effectName := strings.ToLower(effect.TextAt("name", ""))
		if !strings.Contains(effectName, "audio") && !strings.Contains(effectName, "level") {
			continue
		}
		for _, param := range effect.DirectAll("parameter") {
			id := param.TextAt("parameterid", "")
			if id == "" {
				id = param.TextAt("name", "")
			}
			id = strings.ToLower(id)
			switch id {
			case "level", "volume", "audiolevels", "audio level":
				v := parseFloat(param.TextAt("value", ""), 1.0)
				if v > 4.0 {
					v /= 100.0
				}
				db := dbFromLinear(v)
				return &db
			}
		}
	}
	return nil
}

func clipFileID(clip *Node) string {
	f := clip.Direct("file")
	if f == nil {
		return ""
	}
	if id := f.Attr["id"]; id != "" {
		return id
	}
	return f.TextAt("name", "")
}

type audioEditKey struct {
	AssetKey      string
	TimelineStart Fraction
	Duration      Fraction
	SourceStart   Fraction
	Enabled       bool
}

// collapseExplodedAudio merges the channel-per-track representation Premiere
// uses for stereo media. A typical exported stereo edit appears twice with
// identical timing: one clipitem has sourcetrack/trackindex=1 and the other has
// trackindex=2. Referencing the asset twice in FCPXML would play the complete
// stereo stream twice, so it must become one logical audio edit.
func collapseExplodedAudio(p *Project, timeline *Timeline) {
	groups := map[audioEditKey][]int{}
	for i, c := range timeline.Clips {
		if c.Kind != "audio" || c.SourceTrackIndex <= 0 {
			continue
		}
		key := audioEditKey{
			AssetKey:      c.AssetKey,
			TimelineStart: c.TimelineStart,
			Duration:      c.Duration,
			SourceStart:   c.SourceStart,
			Enabled:       c.Enabled,
		}
		groups[key] = append(groups[key], i)
	}

	remove := map[int]bool{}
	merged := 0
	for key, indices := range groups {
		asset := p.Assets[key.AssetKey]
		if asset == nil || asset.AudioChannels < 2 || len(indices) != int(asset.AudioChannels) {
			continue
		}

		bySource := map[int]int{}
		minTrack := 0
		var sharedVolume *float64
		volumeConflict := false
		for _, idx := range indices {
			c := timeline.Clips[idx]
			if c.SourceTrackIndex < 1 || c.SourceTrackIndex > int(asset.AudioChannels) {
				volumeConflict = true
				break
			}
			if _, exists := bySource[c.SourceTrackIndex]; exists {
				volumeConflict = true
				break
			}
			bySource[c.SourceTrackIndex] = idx
			if minTrack == 0 || c.TrackIndex < minTrack {
				minTrack = c.TrackIndex
			}
			if c.VolumeDB != nil {
				if sharedVolume == nil {
					v := *c.VolumeDB
					sharedVolume = &v
				} else if !closeDB(*sharedVolume, *c.VolumeDB) {
					volumeConflict = true
				}
			}
		}
		if volumeConflict || len(bySource) != int(asset.AudioChannels) {
			continue
		}
		complete := true
		for ch := 1; ch <= int(asset.AudioChannels); ch++ {
			if _, ok := bySource[ch]; !ok {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}

		keep := bySource[1]
		mergedClip := timeline.Clips[keep]
		mergedClip.SourceTrackIndex = 0 // zero means use the complete interleaved source
		// Compact Premiere's paired channel tracks (1+2, 3+4, ...) into one
		// logical FCP audio lane while preserving their relative order.
		mergedClip.TrackIndex = (minTrack-1)/int(asset.AudioChannels) + 1
		mergedClip.VolumeDB = sharedVolume
		timeline.Clips[keep] = mergedClip
		for _, idx := range indices {
			if idx != keep {
				remove[idx] = true
			}
		}
		merged++
	}

	if len(remove) == 0 {
		return
	}
	out := make([]TimelineClip, 0, len(timeline.Clips)-len(remove))
	for i, c := range timeline.Clips {
		if !remove[i] {
			out = append(out, c)
		}
	}
	timeline.Clips = out
	p.addIssue("info", "EXPLODED_AUDIO_MERGED", fmt.Sprintf("已将 %d 组 Premiere 分轨立体声音频合并为单个 FCP 音频编辑，避免重复播放。", merged), timeline.Name, "")
}

func parseXmeml(path string) (*Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("找不到输入文件：%s", abs)
	}
	if st.Size() > maxXMLBytes {
		return nil, fmt.Errorf("XML 文件过大，已超过 150 MB 安全限制")
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 4096)
	n, _ := f.Read(head)
	f.Close()
	lower := strings.ToLower(string(head[:n]))
	if strings.Contains(lower, "<!entity") {
		return nil, fmt.Errorf("为避免外部实体风险，不接受包含 ENTITY 的输入 XML")
	}
	if strings.Contains(lower, "<!doctype") && (strings.Contains(lower, " system ") || strings.Contains(lower, " public ") || strings.Contains(lower, "[")) {
		return nil, fmt.Errorf("为避免外部实体风险，不接受外部或内嵌 DTD")
	}
	root, err := parseDOM(abs)
	if err != nil {
		return nil, err
	}
	if root.Name != "xmeml" {
		if root.Name == "fcpxml" {
			return nil, fmt.Errorf("输入文件已经是 FCPXML，无需转换")
		}
		return nil, fmt.Errorf("不支持的根节点：%s；需要 Premiere/Resolve 导出的 xmeml XML", root.Name)
	}
	p := &Project{SourceFile: abs, Assets: map[string]*MediaAsset{}}
	files := richestFiles(root)
	sequences := directSequences(root)
	if len(sequences) == 0 {
		return nil, fmt.Errorf("XML 中没有找到可转换的 sequence")
	}
	windowsReported := map[string]bool{}

	for si, seq := range sequences {
		seqName := seq.TextAt("name", fmt.Sprintf("Sequence %d", si+1))
		seqRate := parseRate(seq.TextAt("rate/timebase", ""), seq.TextAt("rate/ntsc", ""))
		sample := seq.Find("media/video/format/samplecharacteristics")
		width, height := int64(1920), int64(1080)
		if sample != nil {
			width = parseInt(sample.TextAt("width", ""), 1920)
			height = parseInt(sample.TextAt("height", ""), 1080)
		}
		if width < 1 {
			width = 1
		}
		if height < 1 {
			height = 1
		}
		tcStart, tcFormat := Zero, "NDF"
		if strings.ToUpper(seq.TextAt("timecode/displayformat", "")) == "DF" {
			tcFormat = "DF"
		}
		tcFrame := parseInt(seq.TextAt("timecode/frame", ""), -1)
		if tcFrame >= 0 {
			tcStart = framesToSeconds(tcFrame, seqRate)
		} else {
			parsed, format := parseTimecode(seq.TextAt("timecode/string", ""), seqRate)
			tcStart = parsed
			if !parsed.IsZero() {
				tcFormat = format
			}
		}
		timeline := &Timeline{Name: seqName, Rate: seqRate, Duration: Zero, Width: width, Height: height, TCStart: tcStart, TCFormat: tcFormat, Markers: parseMarkers(seq, seqRate, Zero)}
		kinds := []struct{ kind, path string }{{"video", "media/video/track"}, {"audio", "media/audio/track"}}
		for _, kp := range kinds {
			tracks := seq.FindAll(kp.path)
			for ti, track := range tracks {
				trackIndex := ti + 1
				trackEnabled := strings.ToUpper(track.TextAt("enabled", "")) != "FALSE"
				cursor := int64(0)
				for _, item := range track.DirectAll("clipitem") {
					clipName := item.TextAt("name", "Untitled Clip")
					clipRate := nodeRate(item, seqRate)
					sourceIn := parseInt(item.TextAt("in", ""), 0)
					sourceOut := parseInt(item.TextAt("out", ""), sourceIn)
					rawStart := parseInt(item.TextAt("start", ""), -1)
					rawEnd := parseInt(item.TextAt("end", ""), -1)
					sourceDuration := sourceOut - sourceIn
					if sourceDuration < 0 {
						sourceDuration = 0
					}
					itemDuration := parseInt(item.TextAt("duration", ""), sourceDuration)
					fallback := sourceDuration
					if fallback == 0 {
						fallback = itemDuration
					}
					if rawStart < 0 && rawEnd >= 0 {
						rawStart = rawEnd - fallback
						if rawStart < 0 {
							rawStart = 0
						}
					} else if rawEnd < 0 && rawStart >= 0 {
						rawEnd = rawStart + fallback
					} else if rawStart < 0 && rawEnd < 0 {
						rawStart = cursor
						rawEnd = rawStart + fallback
					}
					if rawEnd <= rawStart {
						if fallback < 1 {
							fallback = 1
						}
						rawEnd = rawStart + fallback
					}
					if rawEnd > cursor {
						cursor = rawEnd
					}
					fileID := clipFileID(item)
					if fileID == "" {
						p.addIssue("warning", "MISSING_FILE_REFERENCE", "片段没有 file 引用，已跳过。", seqName, clipName)
						continue
					}
					fileNode := files[fileID]
					if fileNode == nil {
						fileNode = item.Direct("file")
					}
					if fileNode == nil {
						p.addIssue("warning", "UNRESOLVED_FILE", fmt.Sprintf("无法解析素材引用 %s，已跳过。", fileID), seqName, clipName)
						continue
					}
					// Premiere 的调整图层/黑场会使用 mediaSource=Slug，但没有真实 pathurl。
					// 将它写成 asset + MISSING_MEDIA 会导致 Final Cut Pro 拒绝整个 XML。
					if strings.EqualFold(strings.TrimSpace(fileNode.TextAt("mediaSource", "")), "Slug") {
						p.addIssue("warning", "SYNTHETIC_SLUG_SKIPPED", "检测到 Premiere 调整图层或黑场生成器；由于其效果不能直接映射到 FCPXML，已跳过该片段。", seqName, clipName)
						continue
					}
					if p.Assets[fileID] == nil {
						asset, win := parseFileAsset(fileID, fileNode, abs, clipRate)
						if asset.Src == "" {
							p.addIssue("warning", "MISSING_MEDIA_PATH", "素材没有 pathurl；FCP 导入后需要手动重新链接。", seqName, clipName)
							asset.Src = "file:///MISSING_MEDIA/" + url.PathEscape(asset.Name)
						}
						p.Assets[fileID] = asset
						if win && !windowsReported[fileID] {
							p.addIssue("warning", "WINDOWS_MEDIA_PATH", fmt.Sprintf("素材路径来自 Windows：%s。在 Mac 上导入后通常需要重新链接。", asset.Src), seqName, clipName)
							windowsReported[fileID] = true
						}
					}
					asset := p.Assets[fileID]
					if kp.kind == "video" {
						asset.HasVideo = true
					} else {
						asset.HasAudio = true
					}
					timelineStart := framesToSeconds(rawStart, seqRate)
					duration := framesToSeconds(rawEnd-rawStart, seqRate)
					sourceStart := framesToSeconds(sourceIn, clipRate)
					enabled := trackEnabled && strings.ToUpper(item.TextAt("enabled", "")) != "FALSE"
					sourceTrackIndex := 0
					if kp.kind == "audio" {
						sourceTrackIndex = int(parseInt(item.TextAt("sourcetrack/trackindex", ""), 0))
					}
					clip := TimelineClip{Name: clipName, AssetKey: fileID, Kind: kp.kind, TrackIndex: trackIndex, SourceTrackIndex: sourceTrackIndex, TimelineStart: timelineStart, Duration: duration, SourceStart: sourceStart, Enabled: enabled, Markers: parseMarkers(item, clipRate, sourceStart)}
					if kp.kind == "audio" {
						clip.VolumeDB = extractVolumeDB(item)
					}
					timeline.Clips = append(timeline.Clips, clip)
					timeline.Duration = timeline.Duration.Max(timelineStart.Add(duration))
					filters := len(item.DirectAll("filter"))
					if filters > 0 {
						supported := kp.kind == "audio" && clip.VolumeDB != nil
						if !supported || filters > 1 {
							p.addIssue("warning", "UNSUPPORTED_FILTERS", fmt.Sprintf("检测到 %d 个滤镜/效果；首版仅尝试保留基础音量，其余效果未转换。", filters), seqName, clipName)
						}
					}
				}
				transitions := len(track.DirectAll("transitionitem"))
				if transitions > 0 {
					p.addIssue("warning", "TRANSITIONS_FLATTENED", fmt.Sprintf("轨道含 %d 个转场；首版保留片段位置，但不创建对应 FCP 转场。", transitions), seqName, "")
				}
			}
		}
		collapseExplodedAudio(p, timeline)
		declared := parseInt(seq.TextAt("duration", ""), 0)
		if declared > 0 {
			timeline.Duration = timeline.Duration.Max(framesToSeconds(declared, seqRate))
		}
		if !timeline.Duration.Positive() {
			timeline.Duration = framesToSeconds(1, seqRate)
		}
		p.Timelines = append(p.Timelines, timeline)
	}
	if len(p.Assets) == 0 {
		p.addIssue("error", "NO_MEDIA", "没有解析出任何素材资源。", "", "")
	}
	clips := 0
	for _, t := range p.Timelines {
		clips += len(t.Clips)
	}
	if clips == 0 {
		p.addIssue("error", "NO_CLIPS", "没有解析出任何可转换片段。", "", "")
	}
	return p, nil
}
