package main

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type XElem struct {
	Name     string
	Attr     [][2]string
	Text     string
	Children []*XElem
}

func E(name string, attrs ...string) *XElem {
	x := &XElem{Name: name}
	for i := 0; i+1 < len(attrs); i += 2 {
		x.Attr = append(x.Attr, [2]string{attrs[i], attrs[i+1]})
	}
	return x
}
func T(name, text string) *XElem     { return &XElem{Name: name, Text: text} }
func (e *XElem) Add(c *XElem) *XElem { e.Children = append(e.Children, c); return c }

func writeXElem(enc *xml.Encoder, e *XElem) error {
	start := xml.StartElement{Name: xml.Name{Local: e.Name}}
	for _, a := range e.Attr {
		start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: a[0]}, Value: a[1]})
	}
	if err := enc.EncodeToken(start); err != nil {
		return err
	}
	if e.Text != "" {
		if err := enc.EncodeToken(xml.CharData([]byte(e.Text))); err != nil {
			return err
		}
	}
	for _, c := range e.Children {
		if err := writeXElem(enc, c); err != nil {
			return err
		}
	}
	return enc.EncodeToken(start.End())
}

func appendMarkers(parent *XElem, markers []Marker, offset Fraction) {
	for _, m := range markers {
		attrs := []string{"start", secondsToFCPXML(m.Start.Add(offset)), "value", m.Name}
		if m.Name == "" {
			attrs[3] = "Marker"
		}
		if m.Duration.Positive() {
			attrs = append(attrs, "duration", secondsToFCPXML(m.Duration))
		}
		if m.Note != "" {
			attrs = append(attrs, "note", m.Note)
		}
		parent.Add(E("marker", attrs...))
	}
}

func assetDurationFallback(p *Project, key string) (Fraction, bool) {
	var max Fraction
	found := false
	for _, t := range p.Timelines {
		for _, c := range t.Clips {
			if c.AssetKey == key {
				end := c.SourceStart.Add(c.Duration)
				if !found || end.Cmp(max) > 0 {
					max = end
					found = true
				}
			}
		}
	}
	return max, found
}

func formatKey(t *Timeline) string {
	return fmt.Sprintf("%d/%d:%d:%d", t.Rate.FrameDuration().N, t.Rate.FrameDuration().D, t.Width, t.Height)
}

type storyAnchor struct {
	elem       *XElem
	timelineAt Fraction
	duration   Fraction
	localStart Fraction
}

func sameEdit(a, b TimelineClip) bool {
	return a.AssetKey == b.AssetKey &&
		a.TimelineStart.Cmp(b.TimelineStart) == 0 &&
		a.Duration.Cmp(b.Duration) == 0 &&
		a.SourceStart.Cmp(b.SourceStart) == 0
}

func closeDB(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 0.01
}

func matchingAudio(clips []TimelineClip, video TimelineClip, consumed map[int]bool) ([]int, *float64, bool) {
	indices := []int{}
	var volume *float64
	volumeConflict := false
	for i, c := range clips {
		if consumed[i] || c.Kind != "audio" || !sameEdit(video, c) || c.Enabled != video.Enabled {
			continue
		}
		indices = append(indices, i)
		if c.VolumeDB != nil {
			if volume == nil {
				v := *c.VolumeDB
				volume = &v
			} else if !closeDB(*volume, *c.VolumeDB) {
				volumeConflict = true
			}
		}
	}
	return indices, volume, volumeConflict
}

func primaryTrack(clips []TimelineClip, kind string) int {
	track := 0
	for _, c := range clips {
		if c.Kind != kind {
			continue
		}
		if track == 0 || c.TrackIndex < track {
			track = c.TrackIndex
		}
	}
	return track
}

func anchorFor(anchors []storyAnchor, at Fraction) *storyAnchor {
	for i := range anchors {
		a := &anchors[i]
		end := a.timelineAt.Add(a.duration)
		if at.Cmp(a.timelineAt) >= 0 && at.Cmp(end) < 0 {
			return a
		}
	}
	if len(anchors) > 0 && at.Cmp(anchors[len(anchors)-1].timelineAt.Add(anchors[len(anchors)-1].duration)) == 0 {
		return &anchors[len(anchors)-1]
	}
	return nil
}

func localOffset(a *storyAnchor, timelineAt Fraction) Fraction {
	return a.localStart.Add(timelineAt.Sub(a.timelineAt))
}

func appendOneMarker(parent *XElem, m Marker, start Fraction) {
	attrs := []string{"start", secondsToFCPXML(start), "value", m.Name}
	if m.Name == "" {
		attrs[3] = "Marker"
	}
	if m.Duration.Positive() {
		attrs = append(attrs, "duration", secondsToFCPXML(m.Duration))
	}
	if m.Note != "" {
		attrs = append(attrs, "note", m.Note)
	}
	parent.Add(E("marker", attrs...))
}

func addGap(spine *XElem, t *Timeline, at, duration Fraction) storyAnchor {
	gap := spine.Add(E("gap", "name", "Gap", "offset", secondsToFCPXML(t.TCStart.Add(at)), "start", "0s", "duration", secondsToFCPXML(duration)))
	return storyAnchor{elem: gap, timelineAt: at, duration: duration, localStart: Zero}
}

func buildFCPXML(p *Project, eventName string) *XElem {
	root := E("fcpxml", "version", "1.9")
	resources := root.Add(E("resources"))
	formatIDs := map[string]string{}
	timelineFormats := []string{}
	nextID := 1
	for _, t := range p.Timelines {
		key := formatKey(t)
		id := formatIDs[key]
		if id == "" {
			id = fmt.Sprintf("r%d", nextID)
			nextID++
			formatIDs[key] = id
			resources.Add(E("format", "id", id, "name", fmt.Sprintf("CutBridge %dx%dp%.3f", t.Width, t.Height, t.Rate.FPS.Float64()), "frameDuration", secondsToFCPXML(t.Rate.FrameDuration()), "width", fmt.Sprint(t.Width), "height", fmt.Sprint(t.Height), "colorSpace", "1-1-1 (Rec. 709)"))
		}
		timelineFormats = append(timelineFormats, id)
	}
	assetIDs := map[string]string{}
	keys := make([]string, 0, len(p.Assets))
	for k := range p.Assets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		a := p.Assets[key]
		assetFormatID := ""
		if a.HasVideo && a.Width > 0 && a.Height > 0 {
			formatKey := fmt.Sprintf("%d/%d:%d:%d", a.Rate.FrameDuration().N, a.Rate.FrameDuration().D, a.Width, a.Height)
			assetFormatID = formatIDs[formatKey]
			if assetFormatID == "" {
				assetFormatID = fmt.Sprintf("r%d", nextID)
				nextID++
				formatIDs[formatKey] = assetFormatID
				resources.Add(E("format", "id", assetFormatID, "name", fmt.Sprintf("CutBridge Source %dx%dp%.3f", a.Width, a.Height, a.Rate.FPS.Float64()), "frameDuration", secondsToFCPXML(a.Rate.FrameDuration()), "width", fmt.Sprint(a.Width), "height", fmt.Sprint(a.Height), "colorSpace", "1-1-1 (Rec. 709)"))
			}
		}
		id := fmt.Sprintf("r%d", nextID)
		nextID++
		assetIDs[key] = id
		dur := a.Duration
		if !dur.Positive() {
			if fb, ok := assetDurationFallback(p, key); ok {
				dur = fb.Sub(a.Start)
			} else {
				dur = a.Rate.FrameDuration()
			}
		}
		dur = dur.Max(a.Rate.FrameDuration())
		attrs := []string{"id", id, "name", a.Name, "start", secondsToFCPXML(a.Start), "hasVideo", bool01(a.HasVideo), "hasAudio", bool01(a.HasAudio), "duration", secondsToFCPXML(dur)}
		if assetFormatID != "" {
			attrs = append(attrs, "format", assetFormatID)
		}
		if a.HasVideo {
			attrs = append(attrs, "videoSources", "1")
		}
		if a.HasAudio {
			ch := a.AudioChannels
			if ch == 0 {
				ch = 2
			}
			ar := a.AudioRate
			if ar == 0 {
				ar = 48000
			}
			attrs = append(attrs, "audioSources", "1", "audioChannels", fmt.Sprint(ch), "audioRate", fmt.Sprint(ar))
		}
		ae := resources.Add(E("asset", attrs...))
		ae.Add(E("media-rep", "kind", "original-media", "src", a.Src, "suggestedFilename", filepath.Base(a.Name)))
	}
	library := root.Add(E("library"))
	event := library.Add(E("event", "name", eventName))
	for i, t := range p.Timelines {
		project := event.Add(E("project", "name", t.Name))
		seq := project.Add(E("sequence", "format", timelineFormats[i], "duration", secondsToFCPXML(t.Duration), "tcStart", secondsToFCPXML(t.TCStart), "tcFormat", t.TCFormat, "audioLayout", "stereo", "audioRate", "48k"))
		spine := seq.Add(E("spine"))
		clips := append([]TimelineClip(nil), t.Clips...)
		consumed := map[int]bool{}
		anchors := []storyAnchor{}

		primaryKind := "video"
		primary := primaryTrack(clips, "video")
		if primary == 0 {
			primaryKind = "audio"
			primary = primaryTrack(clips, "audio")
		}
		primaryIndices := []int{}
		for idx, c := range clips {
			if c.Kind == primaryKind && c.TrackIndex == primary {
				primaryIndices = append(primaryIndices, idx)
			}
		}
		sort.Slice(primaryIndices, func(a, b int) bool {
			ca, cb := clips[primaryIndices[a]], clips[primaryIndices[b]]
			if cmp := ca.TimelineStart.Cmp(cb.TimelineStart); cmp != 0 {
				return cmp < 0
			}
			return ca.Name < cb.Name
		})

		cursor := Zero
		for _, idx := range primaryIndices {
			c := clips[idx]
			if c.TimelineStart.Cmp(cursor) < 0 {
				// Overlapping edits cannot both occupy the primary storyline. Preserve this one as a connected clip.
				continue
			}
			if c.TimelineStart.Cmp(cursor) > 0 {
				gapDur := c.TimelineStart.Sub(cursor)
				anchors = append(anchors, addGap(spine, t, cursor, gapDur))
				cursor = c.TimelineStart
			}
			ref := assetIDs[c.AssetKey]
			if ref == "" {
				continue
			}
			attrs := []string{"ref", ref, "name", c.Name, "offset", secondsToFCPXML(t.TCStart.Add(c.TimelineStart)), "start", secondsToFCPXML(c.SourceStart), "duration", secondsToFCPXML(c.Duration), "enabled", bool01(c.Enabled)}
			var volume *float64
			embeddedAudio := false
			if c.Kind == "video" {
				audioMatches, embeddedVolume, volumeConflict := matchingAudio(clips, c, consumed)
				if len(audioMatches) > 0 && !volumeConflict {
					for _, ai := range audioMatches {
						consumed[ai] = true
					}
					volume = embeddedVolume
					embeddedAudio = true
					attrs = append(attrs, "audioRole", "dialogue", "videoRole", "video")
				} else {
					attrs = append(attrs, "srcEnable", "video", "videoRole", "video")
					if volumeConflict {
						p.addIssue("warning", "AUDIO_VOLUME_SPLIT", "同一视音频片段的声道音量设置不一致，音频保持为连接片段。", t.Name, c.Name)
					}
				}
			} else {
				attrs = append(attrs, "srcEnable", "audio", "audioRole", fmt.Sprintf("dialogue.track-%d", c.TrackIndex))
				volume = c.VolumeDB
			}
			ce := spine.Add(E("asset-clip", attrs...))
			asset := p.Assets[c.AssetKey]
			if volume != nil && !(embeddedAudio && asset != nil && asset.AudioChannels == 2) {
				ce.Add(E("adjust-volume", "amount", fmt.Sprintf("%.2fdB", *volume)))
			}
			appendMarkers(ce, c.Markers, Zero)
			if embeddedAudio && asset != nil && asset.AudioChannels == 2 {
				// Premiere exports a stereo source as two channel clipitems. After
				// collapsing those clipitems, describe the primary audio as one
				// explicit stereo component. This prevents Final Cut Pro from
				// presenting/routing the channels as two duplicated components.
				component := ce.Add(E("audio-channel-source", "srcCh", "1,2", "outCh", "L,R", "role", "dialogue"))
				if volume != nil {
					component.Add(E("adjust-volume", "amount", fmt.Sprintf("%.2fdB", *volume)))
				}
			}
			consumed[idx] = true
			anchors = append(anchors, storyAnchor{elem: ce, timelineAt: c.TimelineStart, duration: c.Duration, localStart: c.SourceStart})
			cursor = c.TimelineStart.Add(c.Duration)
		}
		if cursor.Cmp(t.Duration) < 0 {
			anchors = append(anchors, addGap(spine, t, cursor, t.Duration.Sub(cursor)))
		}
		if len(anchors) == 0 {
			anchors = append(anchors, addGap(spine, t, Zero, t.Duration))
		}

		secondary := []int{}
		for idx := range clips {
			if !consumed[idx] {
				secondary = append(secondary, idx)
			}
		}
		sort.Slice(secondary, func(a, b int) bool {
			ca, cb := clips[secondary[a]], clips[secondary[b]]
			if cmp := ca.TimelineStart.Cmp(cb.TimelineStart); cmp != 0 {
				return cmp < 0
			}
			if ca.Kind != cb.Kind {
				return ca.Kind == "video"
			}
			return ca.TrackIndex < cb.TrackIndex
		})
		for _, idx := range secondary {
			c := clips[idx]
			ref := assetIDs[c.AssetKey]
			if ref == "" {
				continue
			}
			a := anchorFor(anchors, c.TimelineStart)
			if a == nil {
				p.addIssue("warning", "UNANCHORED_EDIT", "无法为片段找到主故事线连接点，已跳过。", t.Name, c.Name)
				continue
			}
			lane := c.TrackIndex
			if c.Kind == "audio" {
				lane = -lane
			}
			attrs := []string{"ref", ref, "name", c.Name, "lane", fmt.Sprint(lane), "offset", secondsToFCPXML(localOffset(a, c.TimelineStart)), "start", secondsToFCPXML(c.SourceStart), "duration", secondsToFCPXML(c.Duration), "enabled", bool01(c.Enabled)}
			var ce *XElem
			if c.Kind == "video" {
				attrs = append(attrs, "role", "video")
				ce = a.elem.Add(E("video", attrs...))
			} else {
				attrs = append(attrs, "role", fmt.Sprintf("dialogue.track-%d", c.TrackIndex))
				// If channel-per-track audio could not be merged (for example,
				// different per-channel gain), restrict this edit to its declared
				// source channel. Without srcCh, FCP plays the whole stereo asset for
				// every Premiere channel clip, which doubles the audio.
				asset := p.Assets[c.AssetKey]
				if c.SourceTrackIndex > 0 {
					attrs = append(attrs, "srcCh", fmt.Sprint(c.SourceTrackIndex))
					if asset != nil && asset.AudioChannels == 2 {
						if c.SourceTrackIndex == 1 {
							attrs = append(attrs, "outCh", "L")
						} else if c.SourceTrackIndex == 2 {
							attrs = append(attrs, "outCh", "R")
						}
					}
				} else if asset != nil && asset.AudioChannels == 2 {
					// A collapsed Premiere stereo pair must be emitted as one stereo
					// component, not as two implicit full-source components.
					attrs = append(attrs, "srcCh", "1,2", "outCh", "L,R")
				}
				ce = a.elem.Add(E("audio", attrs...))
				if c.VolumeDB != nil {
					ce.Add(E("adjust-volume", "amount", fmt.Sprintf("%.2fdB", *c.VolumeDB)))
				}
			}
			appendMarkers(ce, c.Markers, Zero)
		}
		for _, m := range t.Markers {
			a := anchorFor(anchors, m.Start)
			if a != nil {
				appendOneMarker(a.elem, m, localOffset(a, m.Start))
			}
		}
		meta := seq.Add(E("metadata"))
		meta.Add(E("md", "key", "com.cutbridge.source", "value", p.sourceName(), "type", "string"))
	}
	normalizeAssetClipChildOrder(root)
	return root
}

func normalizeAssetClipChildOrder(e *XElem) {
	for _, child := range e.Children {
		normalizeAssetClipChildOrder(child)
	}
	if e.Name != "asset-clip" {
		return
	}
	order := func(name string) int {
		switch name {
		case "note":
			return 0
		case "conform-rate", "timeMap":
			return 1
		case "adjust-crop", "adjust-corners", "adjust-conform", "adjust-transform", "adjust-blend", "adjust-stabilization", "adjust-rollingShutter", "adjust-loudness", "adjust-noiseReduction", "adjust-humReduction", "adjust-EQ", "adjust-matchEQ", "adjust-volume", "adjust-panner":
			return 2
		case "audio", "video", "clip", "title", "mc-clip", "ref-clip", "sync-clip", "asset-clip", "audition", "spine":
			return 3
		case "marker", "chapter-marker", "rating", "keyword", "analysis-marker":
			return 4
		case "audio-channel-source":
			return 5
		case "filter-video", "filter-video-mask", "filter-audio":
			return 6
		case "metadata":
			return 7
		default:
			return 8
		}
	}
	sort.SliceStable(e.Children, func(i, j int) bool {
		return order(e.Children[i].Name) < order(e.Children[j].Name)
	})
}

func bool01(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func writeFCPXML(root *XElem, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	if _, err = w.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE fcpxml>\n"); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := writeXElem(enc, root); err != nil {
		return err
	}
	if err := enc.Flush(); err != nil {
		return err
	}
	_, err = w.WriteString("\n")
	return err
}

type reportSummary struct {
	GeneratedAt   string  `json:"generated_at"`
	Source        string  `json:"source"`
	FCPXML        string  `json:"fcpxml"`
	Version       string  `json:"fcpxml_version"`
	SequenceCount int     `json:"sequence_count"`
	AssetCount    int     `json:"asset_count"`
	ClipCount     int     `json:"clip_count"`
	Issues        []Issue `json:"issues"`
}

func reportPathFor(out string) string {
	ext := filepath.Ext(out)
	return strings.TrimSuffix(out, ext) + ".report.json"
}
func writeReport(p *Project, out, fcpxml string) error {
	clips := 0
	for _, t := range p.Timelines {
		clips += len(t.Clips)
	}
	r := reportSummary{time.Now().Format(time.RFC3339), p.SourceFile, fcpxml, "1.9", len(p.Timelines), len(p.Assets), clips, p.Issues}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(out, b, 0644)
}

func validateGenerated(root *XElem) []string {
	errs := []string{}
	if root.Name != "fcpxml" {
		errs = append(errs, "根节点必须是 fcpxml")
	}
	projects, sequences := 0, 0
	var walk func(*XElem)
	walk = func(e *XElem) {
		if e.Name == "project" {
			projects++
		}
		if e.Name == "sequence" {
			sequences++
		}
		for _, c := range e.Children {
			walk(c)
		}
	}
	walk(root)
	if projects == 0 {
		errs = append(errs, "没有 project")
	}
	if sequences == 0 {
		errs = append(errs, "没有 sequence")
	}
	return errs
}
