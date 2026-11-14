package main

import (
	"encoding/binary"
	"os"
	"testing"
)

// oggPage builds a minimal Ogg page with a single segment.
func oggPage(granule uint64, seq uint32, payload []byte) []byte {
	if len(payload) > 255 {
		panic("payload too large for single-segment test page")
	}
	page := make([]byte, 27, 27+1+len(payload))
	copy(page, "OggS")
	binary.LittleEndian.PutUint64(page[6:14], granule)
	binary.LittleEndian.PutUint32(page[18:22], seq)
	page[26] = 1
	page = append(page, byte(len(payload)))
	return append(page, payload...)
}

func opusHead(preSkip uint16, inputRate uint32) []byte {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1 // version
	head[9] = 1 // channels
	binary.LittleEndian.PutUint16(head[10:12], preSkip)
	binary.LittleEndian.PutUint32(head[12:16], inputRate)
	return head
}

func TestAnalyzeOggOpusDuration(t *testing.T) {
	// 10.2 s of audio at the fixed 48 kHz granule rate, with a 24 kHz input
	// rate in the header (as produced by `ffmpeg -ar 24000`).
	preSkip := uint16(312)
	data := oggPage(0, 0, opusHead(preSkip, 24000))
	data = append(data, oggPage(0, 1, []byte("OpusTags"))...)
	data = append(data, oggPage(48000*5, 2, make([]byte, 100))...)
	data = append(data, oggPage(uint64(preSkip)+48000*10+9600, 3, make([]byte, 100))...)

	seconds, waveform, err := analyzeOggOpus(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seconds != 11 {
		t.Errorf("duration = %d, want 11", seconds)
	}
	if len(waveform) != waveformLength {
		t.Errorf("waveform length = %d, want %d", len(waveform), waveformLength)
	}
}

func TestAnalyzeOggOpusClampsDuration(t *testing.T) {
	data := oggPage(0, 0, opusHead(0, 48000))
	data = append(data, oggPage(48000*3600, 1, nil)...)
	seconds, _, err := analyzeOggOpus(data)
	if err != nil {
		t.Fatal(err)
	}
	if seconds != maxVoiceNoteSeconds {
		t.Errorf("duration = %d, want clamp to %d", seconds, maxVoiceNoteSeconds)
	}
}

func TestAnalyzeOggOpusRejectsNonOgg(t *testing.T) {
	if _, _, err := analyzeOggOpus([]byte("RIFF....WAVE")); err == nil {
		t.Fatal("expected error for non-Ogg data")
	}
	if _, _, err := analyzeOggOpus(nil); err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestAnalyzeOggOpusTruncatedDoesNotPanic(t *testing.T) {
	data := oggPage(0, 0, opusHead(0, 48000))
	data = append(data, oggPage(48000*2, 1, make([]byte, 200))...)
	for n := 4; n < len(data); n++ {
		if _, _, err := analyzeOggOpus(data[:n]); err != nil {
			t.Fatalf("truncated at %d: unexpected error %v", n, err)
		}
	}
}

func TestPlaceholderWaveformIsDeterministicAndBounded(t *testing.T) {
	a := placeholderWaveform(12)
	b := placeholderWaveform(12)
	if string(a) != string(b) {
		t.Error("waveform should be deterministic for the same duration")
	}
	for i, v := range a {
		if v > 100 {
			t.Errorf("waveform[%d] = %d out of range", i, v)
		}
	}
}

func TestAnalyzeOggOpusRealFile(t *testing.T) {
	// testdata/voice-7.3s.ogg was produced by the same ffmpeg settings the MCP
	// server uses (libopus, 24 kHz, voip).
	data, err := os.ReadFile("testdata/voice-7.3s.ogg")
	if err != nil {
		t.Fatal(err)
	}
	seconds, _, err := analyzeOggOpus(data)
	if err != nil {
		t.Fatal(err)
	}
	if seconds != 8 {
		t.Errorf("duration = %d, want 8 (7.3 s rounded up)", seconds)
	}
}
