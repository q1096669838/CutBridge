package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSampleConversion(t *testing.T) {
	p, err := parseXmeml(filepath.Join("testdata", "sample_premiere.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Timelines) != 1 {
		t.Fatalf("timelines=%d", len(p.Timelines))
	}
	clips := 0
	for _, tl := range p.Timelines {
		clips += len(tl.Clips)
	}
	if clips != 5 {
		t.Fatalf("clips=%d", clips)
	}
	root := buildFCPXML(p, "CutBridge Import")
	if errs := validateGenerated(root); len(errs) != 0 {
		t.Fatal(errs)
	}
	out := filepath.Join(t.TempDir(), "sample.fcpxml")
	if err := writeFCPXML(root, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, expected := range []string{`<!DOCTYPE fcpxml>`, `<fcpxml version="1.9">`, `name="Sample Timeline"`, `<asset-clip ref=`, `audioRole="dialogue"`, `lane="2"`, `start="0s" hasVideo="1"`} {
		if !strings.Contains(s, expected) {
			t.Fatalf("missing %s", expected)
		}
	}

	for _, unwanted := range []string{`CutBridge Track Container`, `<audio ref=`} {
		if strings.Contains(s, unwanted) {
			t.Fatalf("unexpected %s", unwanted)
		}
	}
}

func TestDropFrameTimecode(t *testing.T) {
	rate := parseRate("30", "TRUE")
	seconds, format := parseTimecode("01:00:00;00", rate)
	if format != "DF" {
		t.Fatalf("format=%s", format)
	}
	if seconds.Cmp(F(35999964, 10000)) == 0 {
		return
	}
	// The exact rational is more useful than a rounded comparison.
	expected := framesToSeconds(107892, rate)
	if seconds.Cmp(expected) != 0 {
		t.Fatalf("got %v/%v want %v/%v", seconds.N, seconds.D, expected.N, expected.D)
	}
}

func TestRepeatedFileReferencesAndSyntheticSlug(t *testing.T) {
	xmlText := `<?xml version="1.0" encoding="UTF-8"?>
<xmeml version="4"><sequence><name>Reference Test</name><duration>100</duration>
<rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate>
<media><video><format><samplecharacteristics><rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate><width>1920</width><height>1080</height></samplecharacteristics></format>
<track>
<clipitem id="v1"><name>camera.mp4</name><start>0</start><end>50</end><in>0</in><out>50</out><file id="file-1"><name>camera.mp4</name><pathurl>file:///tmp/camera.mp4</pathurl><rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate><duration>100</duration><media><video><samplecharacteristics><rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate><width>3840</width><height>2160</height></samplecharacteristics></video><audio><samplecharacteristics><samplerate>48000</samplerate></samplecharacteristics><channelcount>2</channelcount></audio></media></file></clipitem>
<clipitem id="v2"><name>camera.mp4</name><start>50</start><end>100</end><in>50</in><out>100</out><file id="file-1"/></clipitem>
</track>
<track><clipitem id="adj"><name>调整图层</name><start>0</start><end>100</end><in>0</in><out>100</out><file id="file-2"><name>黑场视频</name><mediaSource>Slug</mediaSource><rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate><media><video><samplecharacteristics><width>1920</width><height>1080</height></samplecharacteristics></video></media></file></clipitem></track>
</video></media></sequence></xmeml>`
	in := filepath.Join(t.TempDir(), "reference.xml")
	if err := os.WriteFile(in, []byte(xmlText), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := parseXmeml(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Assets) != 1 {
		t.Fatalf("assets=%d, want 1", len(p.Assets))
	}
	a := p.Assets["file-1"]
	if a == nil || a.Name != "camera.mp4" || a.Src != "file:///tmp/camera.mp4" || !a.HasVideo || !a.HasAudio {
		t.Fatalf("bad resolved asset: %#v", a)
	}
	root := buildFCPXML(p, "Import")
	out := filepath.Join(t.TempDir(), "reference.fcpxml")
	if err := writeFCPXML(root, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "MISSING_MEDIA") || strings.Contains(s, "黑场视频") {
		t.Fatalf("synthetic slug leaked into output")
	}
	if !strings.Contains(s, `name="camera.mp4"`) || !strings.Contains(s, `format="`) {
		t.Fatalf("resolved asset or source format missing")
	}
}

func TestPremiereExplodedStereoAudioIsMerged(t *testing.T) {
	xmlText := `<?xml version="1.0" encoding="UTF-8"?>
<xmeml version="4"><sequence><name>Stereo Pair</name><duration>100</duration>
<rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate>
<media>
<video><format><samplecharacteristics><rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate><width>1920</width><height>1080</height></samplecharacteristics></format>
<track><clipitem id="v1"><name>camera.mp4</name><start>0</start><end>100</end><in>0</in><out>100</out><file id="file-1"><name>camera.mp4</name><pathurl>file:///tmp/camera.mp4</pathurl><rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate><duration>100</duration><media><video><samplecharacteristics><rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate><width>1920</width><height>1080</height></samplecharacteristics></video><audio><samplecharacteristics><samplerate>48000</samplerate></samplecharacteristics><channelcount>2</channelcount></audio></media></file></clipitem></track></video>
<audio>
<track><clipitem id="a1" premiereChannelType="stereo"><name>camera.mp4</name><start>0</start><end>100</end><in>0</in><out>100</out><file id="file-1"/><sourcetrack><mediatype>audio</mediatype><trackindex>1</trackindex></sourcetrack></clipitem></track>
<track><clipitem id="a2" premiereChannelType="stereo"><name>camera.mp4</name><start>0</start><end>100</end><in>0</in><out>100</out><file id="file-1"/><sourcetrack><mediatype>audio</mediatype><trackindex>2</trackindex></sourcetrack></clipitem></track>
</audio></media></sequence></xmeml>`
	in := filepath.Join(t.TempDir(), "stereo.xml")
	if err := os.WriteFile(in, []byte(xmlText), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := parseXmeml(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(p.Timelines[0].Clips); got != 2 { // one video + one logical stereo audio edit
		t.Fatalf("clips=%d, want 2", got)
	}
	var audio TimelineClip
	for _, c := range p.Timelines[0].Clips {
		if c.Kind == "audio" {
			audio = c
		}
	}
	if audio.SourceTrackIndex != 0 || audio.TrackIndex != 1 {
		t.Fatalf("audio not merged correctly: %#v", audio)
	}
	root := buildFCPXML(p, "Import")
	out := filepath.Join(t.TempDir(), "stereo.fcpxml")
	if err := writeFCPXML(root, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, `<audio ref=`) {
		t.Fatalf("exploded stereo channels leaked into connected audio clips")
	}
	if !strings.Contains(s, `audioRole="dialogue"`) {
		t.Fatalf("stereo audio was not embedded with the video")
	}
	if !strings.Contains(s, `<audio-channel-source srcCh="1,2" outCh="L,R" role="dialogue">`) {
		t.Fatalf("embedded stereo audio was not explicitly grouped and routed")
	}
}

func TestConnectedStereoAudioUsesSingleStereoComponent(t *testing.T) {
	xmlText := `<?xml version="1.0" encoding="UTF-8"?>
<xmeml version="4"><sequence><name>Connected Stereo</name><duration>150</duration>
<rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate><media>
<video><format><samplecharacteristics><rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate><width>1920</width><height>1080</height></samplecharacteristics></format>
<track><clipitem id="v1"><name>camera.mp4</name><start>0</start><end>100</end><in>0</in><out>100</out><file id="file-1"><name>camera.mp4</name><pathurl>file:///tmp/camera.mp4</pathurl><rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate><duration>150</duration><media><video><samplecharacteristics><rate><timebase>25</timebase><ntsc>FALSE</ntsc></rate><width>1920</width><height>1080</height></samplecharacteristics></video><audio><samplecharacteristics><samplerate>48000</samplerate></samplecharacteristics><channelcount>2</channelcount></audio></media></file></clipitem></track></video>
<audio>
<track><clipitem id="a1" premiereChannelType="stereo"><name>camera.mp4</name><start>25</start><end>125</end><in>25</in><out>125</out><file id="file-1"/><sourcetrack><mediatype>audio</mediatype><trackindex>1</trackindex></sourcetrack></clipitem></track>
<track><clipitem id="a2" premiereChannelType="stereo"><name>camera.mp4</name><start>25</start><end>125</end><in>25</in><out>125</out><file id="file-1"/><sourcetrack><mediatype>audio</mediatype><trackindex>2</trackindex></sourcetrack></clipitem></track>
</audio></media></sequence></xmeml>`
	in := filepath.Join(t.TempDir(), "connected-stereo.xml")
	if err := os.WriteFile(in, []byte(xmlText), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := parseXmeml(in)
	if err != nil {
		t.Fatal(err)
	}
	root := buildFCPXML(p, "Import")
	out := filepath.Join(t.TempDir(), "connected-stereo.fcpxml")
	if err := writeFCPXML(root, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if got := strings.Count(s, `<audio ref=`); got != 1 {
		t.Fatalf("connected audio count=%d, want 1", got)
	}
	if !strings.Contains(s, `srcCh="1,2" outCh="L,R"`) {
		t.Fatalf("connected stereo audio is not explicitly grouped/routed")
	}
	if !strings.Contains(s, `srcEnable="video"`) {
		t.Fatalf("video should not implicitly play audio when the edit is split")
	}
}

func TestFCPXMLToPremiereConversion(t *testing.T) {
	input := filepath.Join("testdata", "sample_fcpxml_1_14.fcpxml")
	p, version, err := parseFCPXML(input)
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.14" {
		t.Fatalf("version=%s", version)
	}
	if len(p.Timelines) != 1 || len(p.Assets) != 15 {
		t.Fatalf("timelines=%d assets=%d", len(p.Timelines), len(p.Assets))
	}
	videoClips, audioClips := 0, 0
	for _, c := range p.Timelines[0].Clips {
		if c.Kind == "video" {
			videoClips++
		} else if c.Kind == "audio" {
			audioClips++
		}
	}
	if videoClips != 27 || audioClips != 17 {
		t.Fatalf("video=%d audio=%d", videoClips, audioClips)
	}
	root := buildXmeml(p)
	if errs := validateGeneratedXmeml(root); len(errs) != 0 {
		t.Fatal(errs)
	}
	out := filepath.Join(t.TempDir(), "premiere.xml")
	if err := writeXmeml(root, out); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseDOM(out)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Name != "xmeml" || parsed.Attr["version"] != "4" {
		t.Fatalf("bad root: %s %#v", parsed.Name, parsed.Attr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, expected := range []string{`<!DOCTYPE xmeml>`, `<xmeml version="4">`, `<name>序列 02 (Resolve)</name>`, `<pathurl>file://localhost/Users/`} {
		if !strings.Contains(s, expected) {
			t.Fatalf("missing %s", expected)
		}
	}
}
