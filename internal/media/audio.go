package media

import (
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PlaceholderWaveform generates a deterministic 64-byte synthetic waveform
// based on duration. Used when we can't derive a real waveform.
func PlaceholderWaveform(duration uint32) []byte {
	const waveformLength = 64
	waveform := make([]byte, waveformLength)

	r := rand.New(rand.NewPCG(uint64(duration), 0))

	baseAmplitude := 35.0
	d := int(duration)
	if d > 120 {
		d = 120
	}
	frequencyFactor := float64(d) / 30.0

	for i := range waveform {
		pos := float64(i) / float64(waveformLength)

		val := baseAmplitude * math.Sin(pos*math.Pi*frequencyFactor*8)
		val += (baseAmplitude / 2) * math.Sin(pos*math.Pi*frequencyFactor*16)

		val += (r.Float64() - 0.5) * 15

		fadeInOut := math.Sin(pos * math.Pi)
		val = val * (0.7 + 0.3*fadeInOut)

		val = val + 50

		if val < 0 {
			val = 0
		} else if val > 100 {
			val = 100
		}

		waveform[i] = byte(val)
	}

	return waveform
}

// sanitizeFFmpegInputPath rewrites paths whose basename begins with `-` so
// that ffmpeg treats them as files rather than option flags. Absolute paths
// pass through unchanged (they already begin with `/`); relative paths get a
// `./` prefix when the basename would otherwise begin with `-`.
//
// Examples:
//
//	"/tmp/foo.mp3"      -> "/tmp/foo.mp3"
//	"/tmp/-weird.mp3"   -> "/tmp/-weird.mp3"  (basename-leading-dash, absolute is safe)
//	"-weird.mp3"        -> "./-weird.mp3"
//	"./-weird.mp3"      -> "./-weird.mp3"
func sanitizeFFmpegInputPath(p string) string {
	if p == "" {
		return p
	}
	// Absolute paths already start with `/` (or a drive letter on Windows,
	// but we don't run on Windows), which ffmpeg parses as a filename.
	if filepath.IsAbs(p) {
		return p
	}
	// Relative paths that already lead with `./` or `../` are fine.
	if strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") {
		return p
	}
	if strings.HasPrefix(p, "-") {
		return "./" + p
	}
	return p
}

var safeEnvKeys = []string{"PATH", "TMPDIR", "TEMP", "TMP", "HOME"}

// minimalEnv returns a restricted environment for child processes, carrying
// only the variables ffmpeg needs (path resolution, temp dirs, home for codec
// config) while excluding credentials, tokens, and other sensitive values.
func minimalEnv() []string {
	env := make([]string, 0, len(safeEnvKeys))
	for _, k := range safeEnvKeys {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// absPathRe matches absolute file paths (Unix-style).
var absPathRe = regexp.MustCompile(`/[\w/.\-]+`)

// sanitizeStderr strips absolute file paths from ffmpeg stderr output and
// truncates to a maximum of 200 characters. This prevents leaking local
// filesystem layout to MCP callers.
func sanitizeStderr(s string) string {
	s = absPathRe.ReplaceAllString(s, "<path>")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
