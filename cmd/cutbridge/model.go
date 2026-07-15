package main

import "path/filepath"

type Marker struct {
	Name     string
	Start    Fraction
	Duration Fraction
	Note     string
}

type MediaAsset struct {
	Key           string
	Name          string
	Src           string
	Rate          Rate
	Duration      Fraction
	Start         Fraction
	HasVideo      bool
	HasAudio      bool
	Width         int64
	Height        int64
	AudioChannels int64
	AudioRate     int64
}

type TimelineClip struct {
	Name       string
	AssetKey   string
	Kind       string
	TrackIndex int
	// SourceTrackIndex is the 1-based audio channel/track index declared by
	// Premiere in <sourcetrack>. Premiere commonly exports one stereo edit as
	// two otherwise-identical clipitems (source tracks 1 and 2). Keeping this
	// value lets the converter merge those exploded channel pairs safely.
	SourceTrackIndex int
	TimelineStart    Fraction
	Duration         Fraction
	SourceStart      Fraction
	Enabled          bool
	Markers          []Marker
	VolumeDB         *float64
}

type Timeline struct {
	Name     string
	Rate     Rate
	Duration Fraction
	Width    int64
	Height   int64
	TCStart  Fraction
	TCFormat string
	Clips    []TimelineClip
	Markers  []Marker
}

type Issue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Sequence string `json:"sequence,omitempty"`
	Clip     string `json:"clip,omitempty"`
}

type Project struct {
	SourceFile string
	Assets     map[string]*MediaAsset
	Timelines  []*Timeline
	Issues     []Issue
}

func (p *Project) addIssue(severity, code, message, sequence, clip string) {
	p.Issues = append(p.Issues, Issue{Severity: severity, Code: code, Message: message, Sequence: sequence, Clip: clip})
}

func (p *Project) sourceName() string { return filepath.Base(p.SourceFile) }
