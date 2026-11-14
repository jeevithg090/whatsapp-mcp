package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
)

// opusGranuleRate is the rate of Ogg Opus granule positions. Per RFC 7845 it
// is always 48 kHz, regardless of the input sample rate in the OpusHead.
const opusGranuleRate = 48000

const (
	maxVoiceNoteSeconds = 300
	waveformLength      = 64
)

var errNotOgg = errors.New("not a valid Ogg file (missing OggS signature)")

// analyzeOggOpus extracts the duration (in whole seconds, clamped to
// 1..300) of an Ogg Opus file and returns a synthetic waveform for it, as
// required by WhatsApp voice notes.
func analyzeOggOpus(data []byte) (uint32, []byte, error) {
	if len(data) < 4 || string(data[:4]) != "OggS" {
		return 0, nil, errNotOgg
	}

	var (
		lastGranule uint64
		preSkip     uint16
	)

	for i := 0; i+27 <= len(data); {
		if string(data[i:i+4]) != "OggS" {
			i++
			continue
		}

		granulePos := binary.LittleEndian.Uint64(data[i+6 : i+14])
		numSegments := int(data[i+26])
		headerEnd := i + 27 + numSegments
		if headerEnd > len(data) {
			break
		}

		pageSize := 27 + numSegments
		for _, segLen := range data[i+27 : headerEnd] {
			pageSize += int(segLen)
		}
		pageEnd := min(i+pageSize, len(data))

		// The OpusHead packet: "OpusHead"(8) version(1) channels(1) preSkip(2) ...
		if head := bytes.Index(data[headerEnd:pageEnd], []byte("OpusHead")); head >= 0 {
			off := headerEnd + head + 10
			if off+2 <= pageEnd {
				preSkip = binary.LittleEndian.Uint16(data[off : off+2])
			}
		}

		// A granule position of -1 (all ones) means "no packet ends on this page".
		if granulePos != 0 && granulePos != math.MaxUint64 {
			lastGranule = granulePos
		}

		i += pageSize
	}

	var duration uint32
	if lastGranule > uint64(preSkip) {
		seconds := float64(lastGranule-uint64(preSkip)) / opusGranuleRate
		duration = uint32(math.Ceil(seconds))
	} else {
		// Very rough estimate (~16 kbit/s) when no granule position is present.
		duration = uint32(len(data) / 2000)
	}

	duration = max(1, min(duration, maxVoiceNoteSeconds))
	return duration, placeholderWaveform(duration), nil
}

// placeholderWaveform generates a deterministic, natural-looking 64-sample
// waveform (values 0..100) for a voice note of the given duration.
func placeholderWaveform(duration uint32) []byte {
	waveform := make([]byte, waveformLength)
	rng := rand.New(rand.NewSource(int64(duration))) //nolint:gosec // cosmetic only

	const baseAmplitude = 35.0
	frequencyFactor := float64(min(int(duration), 120)) / 30.0

	for i := range waveform {
		pos := float64(i) / float64(waveformLength)

		val := baseAmplitude * math.Sin(pos*math.Pi*frequencyFactor*8)
		val += (baseAmplitude / 2) * math.Sin(pos*math.Pi*frequencyFactor*16)
		val += (rng.Float64() - 0.5) * 15
		val *= 0.7 + 0.3*math.Sin(pos*math.Pi)
		val += 50

		waveform[i] = byte(math.Max(0, math.Min(100, val)))
	}
	return waveform
}
