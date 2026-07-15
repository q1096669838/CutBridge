package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type Fraction struct {
	N int64
	D int64
}

func gcd(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

func F(n, d int64) Fraction {
	if d == 0 {
		panic("zero denominator")
	}
	if d < 0 {
		n, d = -n, -d
	}
	g := gcd(n, d)
	return Fraction{N: n / g, D: d / g}
}

func (a Fraction) Add(b Fraction) Fraction { return F(a.N*b.D+b.N*a.D, a.D*b.D) }
func (a Fraction) Sub(b Fraction) Fraction { return F(a.N*b.D-b.N*a.D, a.D*b.D) }
func (a Fraction) Mul(b Fraction) Fraction { return F(a.N*b.N, a.D*b.D) }
func (a Fraction) Div(b Fraction) Fraction { return F(a.N*b.D, a.D*b.N) }
func (a Fraction) Cmp(b Fraction) int {
	left, right := a.N*b.D, b.N*a.D
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
func (a Fraction) Max(b Fraction) Fraction {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}
func (a Fraction) Float64() float64 { return float64(a.N) / float64(a.D) }
func (a Fraction) IsZero() bool     { return a.N == 0 }
func (a Fraction) Positive() bool   { return a.N > 0 }

var Zero = F(0, 1)

type Rate struct {
	FPS      Fraction
	Timebase int64
	NTSC     bool
}

func (r Rate) FrameDuration() Fraction { return F(1, 1).Div(r.FPS) }

func parseBool(value string, def bool) bool {
	if strings.TrimSpace(value) == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y":
		return true
	default:
		return false
	}
}

func parseInt(value string, def int64) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return def
	}
	if i, err := strconv.ParseInt(value, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return int64(f)
	}
	return def
}

func parseFloat(value string, def float64) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return def
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return f
	}
	return def
}

func parseRate(timebaseText, ntscText string) Rate {
	tb := parseInt(timebaseText, 25)
	if tb < 1 {
		tb = 1
	}
	ntsc := parseBool(ntscText, false)
	fps := F(tb, 1)
	if ntsc && (tb == 24 || tb == 30 || tb == 60 || tb == 120) {
		fps = F(tb*1000, 1001)
	}
	return Rate{FPS: fps, Timebase: tb, NTSC: ntsc}
}

func framesToSeconds(frames int64, rate Rate) Fraction {
	return F(frames, 1).Div(rate.FPS)
}

func secondsToFCPXML(v Fraction) string {
	if v.D == 1 {
		return fmt.Sprintf("%ds", v.N)
	}
	return fmt.Sprintf("%d/%ds", v.N, v.D)
}

var timecodeRE = regexp.MustCompile(`^\s*(\d+):(\d+):(\d+)([:;])(\d+)\s*$`)

func parseTimecode(value string, rate Rate) (Fraction, string) {
	m := timecodeRE.FindStringSubmatch(value)
	if m == nil {
		return Zero, "NDF"
	}
	h, _ := strconv.ParseInt(m[1], 10, 64)
	min, _ := strconv.ParseInt(m[2], 10, 64)
	sec, _ := strconv.ParseInt(m[3], 10, 64)
	fr, _ := strconv.ParseInt(m[5], 10, 64)
	base := (h*3600+min*60+sec)*rate.Timebase + fr
	if m[4] == ";" && rate.NTSC && (rate.Timebase == 30 || rate.Timebase == 60) {
		drop := int64(2)
		if rate.Timebase == 60 {
			drop = 4
		}
		totalMinutes := h*60 + min
		base -= drop * (totalMinutes - totalMinutes/10)
		return framesToSeconds(base, rate), "DF"
	}
	return framesToSeconds(base, rate), "NDF"
}

func dbFromLinear(v float64) float64 {
	if v <= 0 {
		return -96.0
	}
	return 20.0 * math.Log10(v)
}
