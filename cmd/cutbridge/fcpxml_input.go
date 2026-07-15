package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type fcpxFormat struct {
	ID     string
	Rate   Rate
	Width  int64
	Height int64
}

type fcpxParsedClip struct {
	Clip TimelineClip
	Lane int
}

func parseFCPTime(value string, def Fraction) Fraction {
	value = strings.TrimSpace(value)
	if value == "" {
		return def
	}
	value = strings.TrimSuffix(value, "s")
	if strings.Contains(value, "/") {
		parts := strings.SplitN(value, "/", 2)
		n := parseInt(parts[0], 0)
		d := parseInt(parts[1], 1)
		if d == 0 {
			return def
		}
		return F(n, d)
	}
	if i, err := strconv.ParseInt(value, 10, 64); err == nil {
		return F(i, 1)
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		const scale = int64(1000000)
		return F(int64(math.Round(f*float64(scale))), scale)
	}
	return def
}

func rateFromFrameDuration(frameDuration string) Rate {
	fd := parseFCPTime(frameDuration, F(1, 25))
	if !fd.Positive() {
		fd = F(1, 25)
	}
	fps := F(1, 1).Div(fd)
	r := Rate{FPS: fps, Timebase: int64(math.Round(fps.Float64())), NTSC: false}
	switch {
	case fps.Cmp(F(24000, 1001)) == 0:
		r.Timebase, r.NTSC = 24, true
	case fps.Cmp(F(30000, 1001)) == 0:
		r.Timebase, r.NTSC = 30, true
	case fps.Cmp(F(60000, 1001)) == 0:
		r.Timebase, r.NTSC = 60, true
	case fps.Cmp(F(120000, 1001)) == 0:
		r.Timebase, r.NTSC = 120, true
	case fps.D == 1:
		r.Timebase = fps.N
	}
	if r.Timebase < 1 {
		r.Timebase = 25
	}
	return r
}

func resolveFCPXMLInput(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		candidate := filepath.Join(abs, "Info.fcpxml")
		if _, err := os.Stat(candidate); err != nil {
			return "", fmt.Errorf("资源包中没有找到 Info.fcpxml：%s", abs)
		}
		return candidate, nil
	}
	return abs, nil
}

func parseFCPXML(path string) (*Project, string, error) {
	resolved, err := resolveFCPXMLInput(path)
	if err != nil {
		return nil, "", err
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return nil, "", err
	}
	if st.Size() > maxXMLBytes {
		return nil, "", fmt.Errorf("XML 文件过大，已超过 150 MB 安全限制")
	}
	root, err := parseDOM(resolved)
	if err != nil {
		return nil, "", err
	}
	if root.Name != "fcpxml" {
		return nil, "", fmt.Errorf("输入不是 FCPXML：根节点为 <%s>", root.Name)
	}
	version := root.Attr["version"]
	resources := root.Direct("resources")
	if resources == nil {
		return nil, version, fmt.Errorf("FCPXML 缺少 resources")
	}

	formats := map[string]fcpxFormat{}
	for _, n := range resources.DirectAll("format") {
		id := n.Attr["id"]
		if id == "" {
			continue
		}
		formats[id] = fcpxFormat{
			ID:     id,
			Rate:   rateFromFrameDuration(n.Attr["frameDuration"]),
			Width:  parseInt(n.Attr["width"], 0),
			Height: parseInt(n.Attr["height"], 0),
		}
	}

	p := &Project{SourceFile: resolved, Assets: map[string]*MediaAsset{}}
	for _, n := range resources.DirectAll("asset") {
		id := n.Attr["id"]
		if id == "" {
			continue
		}
		format := formats[n.Attr["format"]]
		rate := format.Rate
		if rate.Timebase == 0 {
			rate = parseRate("25", "FALSE")
		}
		src := ""
		for _, rep := range n.DirectAll("media-rep") {
			candidate := strings.TrimSpace(rep.Attr["src"])
			if candidate == "" {
				continue
			}
			if src == "" || rep.Attr["kind"] == "original-media" {
				src = candidate
			}
			if rep.Attr["kind"] == "original-media" {
				break
			}
		}
		name := n.Attr["name"]
		if name == "" && src != "" {
			name = filepath.Base(strings.TrimSuffix(src, "/"))
		}
		a := &MediaAsset{
			Key:           id,
			Name:          name,
			Src:           src,
			Rate:          rate,
			Duration:      parseFCPTime(n.Attr["duration"], Zero),
			Start:         parseFCPTime(n.Attr["start"], Zero),
			HasVideo:      n.Attr["hasVideo"] == "1",
			HasAudio:      n.Attr["hasAudio"] == "1",
			Width:         format.Width,
			Height:        format.Height,
			AudioChannels: parseInt(n.Attr["audioChannels"], 0),
			AudioRate:     parseInt(n.Attr["audioRate"], 0),
		}
		if a.AudioChannels == 0 && a.HasAudio {
			a.AudioChannels = 2
		}
		if a.AudioRate == 0 && a.HasAudio {
			a.AudioRate = 48000
		}
		if a.Src == "" {
			p.addIssue("warning", "FCPXML_MISSING_MEDIA_REP", "素材没有 original-media 路径，Premiere 导入后需要手动重新链接。", "", name)
		}
		p.Assets[id] = a
	}

	for _, projectNode := range root.Descendants("project") {
		seq := projectNode.Direct("sequence")
		if seq == nil {
			continue
		}
		format, ok := formats[seq.Attr["format"]]
		if !ok {
			p.addIssue("warning", "FCPXML_SEQUENCE_FORMAT_MISSING", fmt.Sprintf("序列引用的 format=%s 不存在，使用 25 fps 1920×1080 兜底。", seq.Attr["format"]), projectNode.Attr["name"], "")
			format = fcpxFormat{Rate: parseRate("25", "FALSE"), Width: 1920, Height: 1080}
		}
		t := &Timeline{
			Name:     projectNode.Attr["name"],
			Rate:     format.Rate,
			Duration: parseFCPTime(seq.Attr["duration"], format.Rate.FrameDuration()),
			Width:    format.Width,
			Height:   format.Height,
			TCStart:  parseFCPTime(seq.Attr["tcStart"], Zero),
			TCFormat: seq.Attr["tcFormat"],
		}
		if t.Name == "" {
			t.Name = "FCP Sequence"
		}
		if t.Width == 0 {
			t.Width = 1920
		}
		if t.Height == 0 {
			t.Height = 1080
		}
		if t.TCFormat == "" {
			t.TCFormat = "NDF"
		}
		spine := seq.Direct("spine")
		if spine == nil {
			p.addIssue("warning", "FCPXML_NO_SPINE", "项目没有主故事线 spine。", t.Name, "")
			p.Timelines = append(p.Timelines, t)
			continue
		}
		parsed := []fcpxParsedClip{}
		cursor := Zero
		for _, child := range spine.Children {
			pos := cursor
			if value := child.Attr["offset"]; value != "" {
				pos = parseFCPTime(value, t.TCStart).Sub(t.TCStart)
			}
			dur := parseFCPTime(child.Attr["duration"], Zero)
			parseFCPStoryElement(p, t, child, pos, parseFCPTime(child.Attr["start"], Zero), 0, &parsed)
			end := pos.Add(dur)
			if end.Cmp(cursor) > 0 {
				cursor = end
			}
		}
		remapFCPXLanes(parsed)
		for _, item := range parsed {
			t.Clips = append(t.Clips, item.Clip)
			t.Duration = t.Duration.Max(item.Clip.TimelineStart.Add(item.Clip.Duration))
		}
		p.Timelines = append(p.Timelines, t)
	}

	if len(p.Timelines) == 0 {
		return nil, version, fmt.Errorf("FCPXML 中没有可转换的 project/sequence")
	}
	if len(p.Assets) == 0 {
		p.addIssue("error", "NO_MEDIA", "FCPXML 中没有 asset 媒体资源。", "", "")
	}
	return p, version, nil
}

func parseFCPStoryElement(p *Project, t *Timeline, n *Node, globalStart, parentLocalStart Fraction, inheritedLane int, out *[]fcpxParsedClip) {
	duration := parseFCPTime(n.Attr["duration"], Zero)
	localStart := parseFCPTime(n.Attr["start"], Zero)
	if n.Attr["start"] == "" {
		if asset := p.Assets[n.Attr["ref"]]; asset != nil {
			localStart = asset.Start
		}
	}
	lane := inheritedLane
	if value := n.Attr["lane"]; value != "" {
		lane = int(parseInt(value, int64(inheritedLane)))
	}

	if n.Name == "gap" {
		for _, child := range n.Children {
			childOffset := parseFCPTime(child.Attr["offset"], localStart)
			childGlobal := globalStart.Add(childOffset.Sub(localStart))
			parseFCPStoryElement(p, t, child, childGlobal, localStart, lane, out)
		}
		return
	}

	ref := n.Attr["ref"]
	asset := p.Assets[ref]
	if n.Name == "asset-clip" || n.Name == "video" || n.Name == "audio" {
		if asset == nil {
			p.addIssue("warning", "FCPXML_UNRESOLVED_ASSET", fmt.Sprintf("片段引用的素材 %s 不存在，已跳过。", ref), t.Name, n.Attr["name"])
		} else if duration.Positive() {
			sourceStart := parseFCPTime(n.Attr["start"], asset.Start)
			enabled := n.Attr["enabled"] != "0"
			srcEnable := n.Attr["srcEnable"]
			includeVideo := asset.HasVideo
			includeAudio := asset.HasAudio
			switch n.Name {
			case "video":
				includeAudio = false
			case "audio":
				includeVideo = false
			}
			if srcEnable == "video" {
				includeAudio = false
			}
			if srcEnable == "audio" {
				includeVideo = false
			}
			name := n.Attr["name"]
			if name == "" {
				name = asset.Name
			}
			volume := parseFCPVolume(n)
			if includeVideo {
				*out = append(*out, fcpxParsedClip{Lane: lane, Clip: TimelineClip{Name: name, AssetKey: ref, Kind: "video", TimelineStart: globalStart, Duration: duration, SourceStart: sourceStart, Enabled: enabled, Markers: parseFCPMarkers(n, sourceStart)}})
			}
			if includeAudio {
				srcTrack := 0
				if srcCh := n.Attr["srcCh"]; srcCh != "" {
					parts := strings.FieldsFunc(srcCh, func(r rune) bool { return r == ',' || r == ' ' })
					if len(parts) > 0 {
						srcTrack = int(parseInt(parts[0], 0))
					}
				}
				*out = append(*out, fcpxParsedClip{Lane: lane, Clip: TimelineClip{Name: name, AssetKey: ref, Kind: "audio", SourceTrackIndex: srcTrack, TimelineStart: globalStart, Duration: duration, SourceStart: sourceStart, Enabled: enabled, Markers: parseFCPMarkers(n, sourceStart), VolumeDB: volume}})
			}
		}
	} else if n.Name == "transition" {
		p.addIssue("warning", "FCPXML_TRANSITION_SKIPPED", "FCP 转场不能可靠映射为 Premiere XML，已保留相邻剪辑位置但未转换转场。", t.Name, n.Attr["name"])
	} else if n.Name != "spine" {
		switch n.Name {
		case "adjust-colorConform", "metadata", "marker", "keyword", "rating", "chapter-marker", "analysis-marker", "sync-source":
			// Metadata-only elements are intentionally ignored.
		default:
			if n.Attr["ref"] != "" || duration.Positive() {
				p.addIssue("warning", "FCPXML_ELEMENT_SKIPPED", fmt.Sprintf("暂不支持 FCPXML 元素 <%s>，已跳过。", n.Name), t.Name, n.Attr["name"])
			}
		}
	}

	if n.Name == "asset-clip" || n.Name == "video" || n.Name == "audio" {
		for _, child := range n.Children {
			switch child.Name {
			case "asset-clip", "video", "audio", "gap", "transition", "ref-clip", "sync-clip", "mc-clip", "clip", "title":
				childOffset := parseFCPTime(child.Attr["offset"], localStart)
				childGlobal := globalStart.Add(childOffset.Sub(localStart))
				parseFCPStoryElement(p, t, child, childGlobal, localStart, lane, out)
			}
		}
	}
}

func parseFCPMarkers(n *Node, sourceStart Fraction) []Marker {
	out := []Marker{}
	for _, child := range n.Children {
		if child.Name != "marker" && child.Name != "chapter-marker" {
			continue
		}
		start := parseFCPTime(child.Attr["start"], sourceStart)
		out = append(out, Marker{Name: child.Attr["value"], Start: start, Duration: parseFCPTime(child.Attr["duration"], Zero), Note: child.Attr["note"]})
	}
	return out
}

func parseFCPVolume(n *Node) *float64 {
	for _, child := range n.Children {
		if child.Name != "adjust-volume" {
			continue
		}
		value := strings.TrimSpace(strings.TrimSuffix(child.Attr["amount"], "dB"))
		if value == "" {
			continue
		}
		if v, err := strconv.ParseFloat(value, 64); err == nil {
			return &v
		}
	}
	return nil
}

func remapFCPXLanes(items []fcpxParsedClip) {
	for _, kind := range []string{"video", "audio"} {
		lanes := []int{}
		seen := map[int]bool{}
		for _, item := range items {
			if item.Clip.Kind != kind || item.Lane == 0 || seen[item.Lane] {
				continue
			}
			seen[item.Lane] = true
			lanes = append(lanes, item.Lane)
		}
		sort.Slice(lanes, func(i, j int) bool {
			ai, aj := lanes[i], lanes[j]
			if kind == "audio" {
				ai, aj = absInt(ai), absInt(aj)
			}
			if ai == aj {
				return lanes[i] < lanes[j]
			}
			return ai < aj
		})
		trackFor := map[int]int{}
		for i, lane := range lanes {
			trackFor[lane] = i + 2
		}
		for i := range items {
			if items[i].Clip.Kind != kind {
				continue
			}
			if items[i].Lane == 0 {
				items[i].Clip.TrackIndex = 1
			} else {
				items[i].Clip.TrackIndex = trackFor[items[i].Lane]
			}
		}
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
