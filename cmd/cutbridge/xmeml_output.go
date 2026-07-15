package main

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type writtenClip struct {
	Clip      TimelineClip
	ID        string
	Track     int
	ClipIndex int
	Pair      *writtenClip
}

func framesAtRate(seconds Fraction, rate Rate) int64 {
	v := seconds.Mul(rate.FPS)
	if v.D == 1 {
		return v.N
	}
	return int64(math.Round(v.Float64()))
}

func normalizePremierePathURL(raw, name string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "file://localhost/MISSING_MEDIA/" + url.PathEscape(name)
	}
	if strings.HasPrefix(strings.ToLower(raw), "file://localhost/") {
		return raw
	}
	if strings.HasPrefix(strings.ToLower(raw), "file:///") {
		return "file://localhost/" + raw[len("file:///"):]
	}
	return raw
}

func buildXmeml(p *Project) *XElem {
	root := E("xmeml", "version", "4")
	assetKeys := make([]string, 0, len(p.Assets))
	for key := range p.Assets {
		assetKeys = append(assetKeys, key)
	}
	sort.Strings(assetKeys)
	fileIDs := map[string]string{}
	masterIDs := map[string]string{}
	for i, key := range assetKeys {
		fileIDs[key] = fmt.Sprintf("file-%d", i+1)
		masterIDs[key] = fmt.Sprintf("masterclip-%d", i+1)
	}
	clipSerial := 1

	for seqIndex, timeline := range p.Timelines {
		definedFiles := map[string]bool{}
		seq := root.Add(E("sequence", "id", fmt.Sprintf("sequence-%d", seqIndex+1)))
		seq.Add(T("uuid", fmt.Sprintf("%08X-%04X-%04X-%04X-%012X", time.Now().UnixNano()&0xffffffff, seqIndex+1, 0x4c42, 0x8a00, time.Now().UnixNano()&0xffffffffffff)))
		seq.Add(T("duration", fmt.Sprint(framesAtRate(timeline.Duration, timeline.Rate))))
		appendXmemlRate(seq, timeline.Rate)
		seq.Add(T("name", timeline.Name))

		media := seq.Add(E("media"))
		video := media.Add(E("video"))
		format := video.Add(E("format"))
		sc := format.Add(E("samplecharacteristics"))
		appendXmemlRate(sc, timeline.Rate)
		sc.Add(T("width", fmt.Sprint(timeline.Width)))
		sc.Add(T("height", fmt.Sprint(timeline.Height)))
		sc.Add(T("anamorphic", "FALSE"))
		sc.Add(T("pixelaspectratio", "square"))
		sc.Add(T("fielddominance", "none"))
		sc.Add(T("colordepth", "24"))

		videoWritten := groupWrittenClips(timeline.Clips, "video", &clipSerial)
		audioWritten := groupWrittenClips(timeline.Clips, "audio", &clipSerial)
		pairWrittenClips(videoWritten, audioWritten)

		for _, trackNumber := range sortedTrackNumbers(videoWritten) {
			track := video.Add(E("track"))
			for _, item := range videoWritten[trackNumber] {
				emitXmemlClipitem(track, item, false, timeline, p, fileIDs, masterIDs, definedFiles)
			}
			track.Add(T("enabled", "TRUE"))
			track.Add(T("locked", "FALSE"))
		}

		audio := media.Add(E("audio"))
		audio.Add(T("numOutputChannels", "2"))
		audioFormat := audio.Add(E("format"))
		audioSC := audioFormat.Add(E("samplecharacteristics"))
		audioSC.Add(T("depth", "16"))
		audioSC.Add(T("samplerate", "48000"))
		outputs := audio.Add(E("outputs"))
		for channelIndex := 1; channelIndex <= 2; channelIndex++ {
			group := outputs.Add(E("group"))
			group.Add(T("index", fmt.Sprint(channelIndex)))
			group.Add(T("numchannels", "1"))
			group.Add(T("downmix", "0"))
			channel := group.Add(E("channel"))
			channel.Add(T("index", fmt.Sprint(channelIndex)))
		}
		for _, trackNumber := range sortedTrackNumbers(audioWritten) {
			track := audio.Add(E("track"))
			for _, item := range audioWritten[trackNumber] {
				emitXmemlClipitem(track, item, true, timeline, p, fileIDs, masterIDs, definedFiles)
			}
			track.Add(T("enabled", "TRUE"))
			track.Add(T("locked", "FALSE"))
			track.Add(T("outputchannelindex", fmt.Sprint(1+(trackNumber-1)%2)))
		}

		timecode := seq.Add(E("timecode"))
		appendXmemlRate(timecode, timeline.Rate)
		timecode.Add(T("string", formatTimecode(timeline.TCStart, timeline.Rate, timeline.TCFormat)))
		timecode.Add(T("frame", fmt.Sprint(framesAtRate(timeline.TCStart, timeline.Rate))))
		timecode.Add(T("displayformat", timeline.TCFormat))
	}
	return root
}

func appendXmemlRate(parent *XElem, rate Rate) {
	r := parent.Add(E("rate"))
	r.Add(T("timebase", fmt.Sprint(rate.Timebase)))
	if rate.NTSC {
		r.Add(T("ntsc", "TRUE"))
	} else {
		r.Add(T("ntsc", "FALSE"))
	}
}

func groupWrittenClips(clips []TimelineClip, kind string, serial *int) map[int][]*writtenClip {
	out := map[int][]*writtenClip{}
	for _, clip := range clips {
		if clip.Kind != kind {
			continue
		}
		track := clip.TrackIndex
		if track < 1 {
			track = 1
		}
		item := &writtenClip{Clip: clip, ID: fmt.Sprintf("clipitem-%d", *serial), Track: track}
		*serial++
		out[track] = append(out[track], item)
	}
	for _, items := range out {
		sort.SliceStable(items, func(i, j int) bool {
			if cmp := items[i].Clip.TimelineStart.Cmp(items[j].Clip.TimelineStart); cmp != 0 {
				return cmp < 0
			}
			return items[i].Clip.Name < items[j].Clip.Name
		})
		for i, item := range items {
			item.ClipIndex = i + 1
		}
	}
	return out
}

func pairWrittenClips(video, audio map[int][]*writtenClip) {
	used := map[*writtenClip]bool{}
	for _, videoTrack := range video {
		for _, v := range videoTrack {
			for _, audioTrack := range audio {
				for _, a := range audioTrack {
					if used[a] || !sameEdit(v.Clip, a.Clip) || v.Clip.Enabled != a.Clip.Enabled {
						continue
					}
					v.Pair, a.Pair = a, v
					used[a] = true
					goto paired
				}
			}
		paired:
		}
	}
}

func sortedTrackNumbers(groups map[int][]*writtenClip) []int {
	keys := make([]int, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func emitXmemlClipitem(parent *XElem, item *writtenClip, isAudio bool, timeline *Timeline, p *Project, fileIDs, masterIDs map[string]string, definedFiles map[string]bool) {
	asset := p.Assets[item.Clip.AssetKey]
	if asset == nil {
		return
	}
	attrs := []string{"id", item.ID}
	if isAudio {
		if asset.AudioChannels >= 2 && item.Clip.SourceTrackIndex == 0 {
			attrs = append(attrs, "premiereChannelType", "stereo")
		} else {
			attrs = append(attrs, "premiereChannelType", "mono")
		}
	}
	clipitem := parent.Add(E("clipitem", attrs...))
	clipitem.Add(T("masterclipid", masterIDs[item.Clip.AssetKey]))
	clipitem.Add(T("name", item.Clip.Name))
	if item.Clip.Enabled {
		clipitem.Add(T("enabled", "TRUE"))
	} else {
		clipitem.Add(T("enabled", "FALSE"))
	}
	clipitem.Add(T("duration", fmt.Sprint(framesAtRate(asset.Duration, asset.Rate))))
	appendXmemlRate(clipitem, asset.Rate)
	clipitem.Add(T("start", fmt.Sprint(framesAtRate(item.Clip.TimelineStart, timeline.Rate))))
	clipitem.Add(T("end", fmt.Sprint(framesAtRate(item.Clip.TimelineStart.Add(item.Clip.Duration), timeline.Rate))))
	relativeIn := item.Clip.SourceStart.Sub(asset.Start)
	if relativeIn.Cmp(Zero) < 0 {
		relativeIn = Zero
	}
	clipitem.Add(T("in", fmt.Sprint(framesAtRate(relativeIn, asset.Rate))))
	clipitem.Add(T("out", fmt.Sprint(framesAtRate(relativeIn.Add(item.Clip.Duration), asset.Rate))))
	if !isAudio {
		clipitem.Add(T("alphatype", "none"))
		clipitem.Add(T("pixelaspectratio", "square"))
		clipitem.Add(T("anamorphic", "FALSE"))
	}
	emitXmemlFile(clipitem, asset, fileIDs[item.Clip.AssetKey], definedFiles)

	if isAudio {
		sourceTrack := clipitem.Add(E("sourcetrack"))
		sourceTrack.Add(T("mediatype", "audio"))
		trackIndex := item.Clip.SourceTrackIndex
		if trackIndex < 1 {
			trackIndex = 1
		}
		sourceTrack.Add(T("trackindex", fmt.Sprint(trackIndex)))
		if item.Clip.VolumeDB != nil {
			filter := clipitem.Add(E("filter"))
			effect := filter.Add(E("effect"))
			effect.Add(T("name", "Audio Levels"))
			effect.Add(T("effectid", "audiolevels"))
			effect.Add(T("effecttype", "audiolevels"))
			effect.Add(T("mediatype", "audio"))
			param := effect.Add(E("parameter"))
			param.Add(T("parameterid", "level"))
			param.Add(T("name", "Level"))
			param.Add(T("value", fmt.Sprintf("%.8f", math.Pow(10, *item.Clip.VolumeDB/20))))
		}
	}

	if item.Pair != nil {
		for _, linked := range []*writtenClip{item, item.Pair} {
			link := clipitem.Add(E("link"))
			link.Add(T("linkclipref", linked.ID))
			link.Add(T("mediatype", linked.Clip.Kind))
			link.Add(T("trackindex", fmt.Sprint(linked.Track)))
			link.Add(T("clipindex", fmt.Sprint(linked.ClipIndex)))
		}
	}
}

func emitXmemlFile(parent *XElem, asset *MediaAsset, fileID string, definedFiles map[string]bool) {
	if definedFiles[fileID] {
		parent.Add(E("file", "id", fileID))
		return
	}
	file := parent.Add(E("file", "id", fileID))
	file.Add(T("name", asset.Name))
	file.Add(T("pathurl", normalizePremierePathURL(asset.Src, asset.Name)))
	appendXmemlRate(file, asset.Rate)
	file.Add(T("duration", fmt.Sprint(framesAtRate(asset.Duration, asset.Rate))))
	timecode := file.Add(E("timecode"))
	appendXmemlRate(timecode, asset.Rate)
	timecode.Add(T("string", formatTimecode(asset.Start, asset.Rate, "NDF")))
	timecode.Add(T("frame", fmt.Sprint(framesAtRate(asset.Start, asset.Rate))))
	timecode.Add(T("displayformat", "NDF"))
	media := file.Add(E("media"))
	if asset.HasVideo {
		video := media.Add(E("video"))
		sc := video.Add(E("samplecharacteristics"))
		appendXmemlRate(sc, asset.Rate)
		sc.Add(T("width", fmt.Sprint(asset.Width)))
		sc.Add(T("height", fmt.Sprint(asset.Height)))
		sc.Add(T("anamorphic", "FALSE"))
		sc.Add(T("pixelaspectratio", "square"))
		sc.Add(T("fielddominance", "none"))
	}
	if asset.HasAudio {
		audio := media.Add(E("audio"))
		sc := audio.Add(E("samplecharacteristics"))
		sc.Add(T("depth", "16"))
		sampleRate := asset.AudioRate
		if sampleRate == 0 {
			sampleRate = 48000
		}
		sc.Add(T("samplerate", fmt.Sprint(sampleRate)))
		channels := asset.AudioChannels
		if channels == 0 {
			channels = 2
		}
		audio.Add(T("channelcount", fmt.Sprint(channels)))
	}
	definedFiles[fileID] = true
}

func formatTimecode(seconds Fraction, rate Rate, format string) string {
	frames := framesAtRate(seconds, rate)
	if frames < 0 {
		frames = 0
	}
	base := rate.Timebase
	if base < 1 {
		base = 25
	}
	ff := frames % base
	totalSeconds := frames / base
	ss := totalSeconds % 60
	mm := (totalSeconds / 60) % 60
	hh := totalSeconds / 3600
	sep := ":"
	if format == "DF" {
		sep = ";"
	}
	return fmt.Sprintf("%02d:%02d:%02d%s%02d", hh, mm, ss, sep, ff)
}

func writeXmeml(root *XElem, out string) error {
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
	if _, err = w.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE xmeml>\n"); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "\t")
	if err := writeXElem(enc, root); err != nil {
		return err
	}
	if err := enc.Flush(); err != nil {
		return err
	}
	_, err = w.WriteString("\n")
	return err
}

type reverseReport struct {
	GeneratedAt         string  `json:"generated_at"`
	Source              string  `json:"source"`
	SourceFCPXMLVersion string  `json:"source_fcpxml_version"`
	PremiereXML         string  `json:"premiere_xml"`
	OutputFormat        string  `json:"output_format"`
	SequenceCount       int     `json:"sequence_count"`
	AssetCount          int     `json:"asset_count"`
	VideoClipCount      int     `json:"video_clip_count"`
	AudioClipCount      int     `json:"audio_clip_count"`
	Issues              []Issue `json:"issues"`
}

func writeReverseReport(p *Project, sourceVersion, out, premiereXML string) error {
	videoClips, audioClips := 0, 0
	for _, t := range p.Timelines {
		for _, c := range t.Clips {
			if c.Kind == "video" {
				videoClips++
			} else if c.Kind == "audio" {
				audioClips++
			}
		}
	}
	r := reverseReport{
		GeneratedAt:         time.Now().Format(time.RFC3339),
		Source:              p.SourceFile,
		SourceFCPXMLVersion: sourceVersion,
		PremiereXML:         premiereXML,
		OutputFormat:        "Final Cut Pro 7 XML / xmeml version 4",
		SequenceCount:       len(p.Timelines),
		AssetCount:          len(p.Assets),
		VideoClipCount:      videoClips,
		AudioClipCount:      audioClips,
		Issues:              p.Issues,
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(out, b, 0644)
}

func validateGeneratedXmeml(root *XElem) []string {
	errs := []string{}
	if root.Name != "xmeml" {
		errs = append(errs, "根节点必须是 xmeml")
	}
	sequences, clips := 0, 0
	var walk func(*XElem)
	walk = func(e *XElem) {
		if e.Name == "sequence" {
			sequences++
		}
		if e.Name == "clipitem" {
			clips++
		}
		for _, child := range e.Children {
			walk(child)
		}
	}
	walk(root)
	if sequences == 0 {
		errs = append(errs, "没有 sequence")
	}
	if clips == 0 {
		errs = append(errs, "没有 clipitem")
	}
	return errs
}
