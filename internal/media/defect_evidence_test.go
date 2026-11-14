//go:build defectevidence

// Package media — defect evidence suite.
//
// These tests encode the strict, spec-derived behavior the WAV and Y4M
// decoders *should* provide. They fail against the current implementation and
// are therefore excluded from the default test run via the "defectevidence"
// build tag. They are never weakened to match the implementation: when a
// defect is fixed in production, the corresponding case starts passing and can
// be promoted into the main regression corpus.
//
// Run with:
//
//	go test -tags defectevidence ./internal/media/ -run TestDefect -v
package media

import (
	"bytes"
	"strings"
	"testing"
)

// acceptExpectError fails if the decoder accepts a payload that the contract
// says must be rejected (a mis-acceptance / false negative).
func acceptExpectError(t *testing.T, defectID string, data []byte, inspect func([]byte) error) {
	t.Helper()
	if err := inspect(data); err == nil {
		t.Fatalf("%s: decoder accepted payload that must be rejected", defectID)
	}
}

func inspectAudioErr(data []byte) error {
	_, err := InspectAudio(data)
	return err
}

func inspectVideoErr(data []byte) error {
	_, err := InspectVideo(data)
	return err
}

// DEFECT-W1: RIFF size declaration vs actual payload inconsistency.
// The loop walks the whole buffer regardless of the declared RIFF extent, so
// bytes beyond the declared RIFF size are parsed as chunks and a hidden extra
// data chunk is silently ingested.
func TestDefectW1RiffSizeUnderstatement(t *testing.T) {
	pcm := pcmSine(1, 16, 48000)
	fmtChunk := buildFmtChunk(defaultWAVFmt(), 16)
	declared := buildWAV(-1, fmtChunk, buildDataChunk(pcm))
	hidden := append(declared, serializeChunk(buildDataChunk(pcmSine(1, 4, 48000)))...)
	// Keep the RIFF size pointing at the original extent only.
	acceptExpectError(t, "DEFECT-W1", hidden, inspectAudioErr)
}

// DEFECT-W2: fmt byteRate is accepted without cross-checking
// sampleRate*blockAlign, so a header can advertise an impossible byte rate.
func TestDefectW2LyingByteRate(t *testing.T) {
	fields := defaultWAVFmt()
	fields.byteRate = 1 // contradicts 48000 Hz * 2 block align
	payload := buildWAV(-1, buildFmtChunk(fields, 16), buildDataChunk(pcmSine(1, 16, 48000)))
	acceptExpectError(t, "DEFECT-W2", payload, inspectAudioErr)
}

// DEFECT-W3: duplicate fmt/data chunks. A second, conflicting fmt chunk
// silently overrides the first (mono -> stereo here), and duplicate data
// chunks make earlier sample bytes disappear without any error.
func TestDefectW3DuplicateChunks(t *testing.T) {
	pcm := pcmSine(1, 16, 48000)
	mono := buildFmtChunk(defaultWAVFmt(), 16)
	stereoFields := defaultWAVFmt()
	stereoFields.channels = 2
	stereoFields.byteRate = 48000 * 4
	stereoFields.blockAlign = 4
	stereo := buildFmtChunk(stereoFields, 16)

	dupFmt := buildWAV(-1, mono, stereo, buildDataChunk(pcm))
	acceptExpectError(t, "DEFECT-W3a", dupFmt, inspectAudioErr)

	dupData := buildWAV(-1, mono, buildDataChunk(pcmSine(1, 32, 48000)), buildDataChunk(pcm))
	acceptExpectError(t, "DEFECT-W3b", dupData, inspectAudioErr)
}

// DEFECT-W4: a data chunk placed before the fmt chunk is accepted. The chunk
// loop records pcm before the format is known, so ordering invariants of the
// RIFF/WAVE layout are not enforced.
func TestDefectW4DataBeforeFmt(t *testing.T) {
	pcm := pcmSine(1, 16, 48000)
	payload := buildWAV(-1, buildDataChunk(pcm), buildFmtChunk(defaultWAVFmt(), 16))
	acceptExpectError(t, "DEFECT-W4", payload, inspectAudioErr)
}

// DEFECT-W5: any stream shorter than one millisecond reports DurationMS == 0,
// indistinguishable from an empty/zero-length track. The descriptor claims a
// duration that does not exist in the declared samples (integer truncation
// with no minimum/rounding policy).
func TestDefectW5ZeroDurationDescriptor(t *testing.T) {
	fields := defaultWAVFmt()
	fields.sampleRate = 8000
	fields.byteRate = 16000
	payload := buildWAV(-1, buildFmtChunk(fields, 16), buildDataChunk(pcmSine(1, 1, 8000)))
	clip, err := InspectAudio(payload)
	if err != nil {
		t.Fatalf("DEFECT-W5: valid one-sample file rejected: %v", err)
	}
	if clip.Info.DurationMS == 0 {
		t.Fatalf("DEFECT-W5: one-sample track reported zero duration (frames=%d rate=%d)", clip.Info.FrameCount, clip.Info.SampleRate)
	}
}

// DEFECT-Y1: per-frame "FRAME It" interlacing parameters are ignored even
// though the stream header declares progressive-only acceptance.
func TestDefectY1FrameInterlaceIgnored(t *testing.T) {
	stream := validY4MStream(16, 16, 24, 1, 2)
	stream.frameHeaders[0] = []string{"FRAME", "It"}
	payload := buildY4M(stream, 16, 16)
	acceptExpectError(t, "DEFECT-Y1", payload, inspectVideoErr)
}

// DEFECT-Y2: per-frame "FRAME C422" chroma parameters are ignored. The frame
// body is still sized as C420 while the frame header declares 4:2:2, a direct
// declaration/sample inconsistency.
func TestDefectY2FrameChromaIgnored(t *testing.T) {
	stream := validY4MStream(16, 16, 24, 1, 2)
	stream.frameHeaders[0] = []string{"FRAME", "C422"}
	payload := buildY4M(stream, 16, 16)
	acceptExpectError(t, "DEFECT-Y2", payload, inspectVideoErr)
}

// DEFECT-Y3: duplicated stream-header parameters are resolved last-one-wins
// with no error, letting a second W/H/F/C token silently override the first.
// The stream here carries W16 then W32; the decoder trusts the override and
// accepts geometry the first declaration never advertised.
func TestDefectY3DuplicateHeaderTokens(t *testing.T) {
	stream := validY4MStream(32, 16, 24, 1, 1)
	stream.headerTokens = defaultY4MHeader(16, 16, 24, 1)
	stream.headerTokens = append(stream.headerTokens, "W32")
	payload := buildY4M(stream, 32, 16)
	acceptExpectError(t, "DEFECT-Y3", payload, inspectVideoErr)
}

// DEFECT-Y4: the conform export path (NormalizeY4M) rewrites the pixel aspect
// ratio token to A0:0 even when the admitted stream declared a real ratio,
// changing the content meaning of the exported picture.
func TestDefectY4AspectRatioDroppedOnExport(t *testing.T) {
	stream := validY4MStream(16, 16, 24, 1, 1)
	stream.headerTokens = defaultY4MHeader(16, 16, 24, 1, "A16:9")
	original := buildY4M(stream, 16, 16)
	exported, _, err := NormalizeY4M(original)
	if err != nil {
		t.Fatalf("NormalizeY4M: %v", err)
	}
	header := string(exported[:bytes.IndexByte(exported, '\n')])
	if !strings.Contains(header, "A16:9") {
		t.Fatalf("DEFECT-Y4: export lost declared aspect ratio; header=%q", header)
	}
}

// DEFECT-Y5: the frame-rate range check compares the raw numerator against
// 120 instead of the effective rate num/den, so the standard NTSC rate
// 30000:1001 (~29.97 fps, well within policy) is falsely rejected.
func TestDefectY5FractionalNTSCRejected(t *testing.T) {
	payload := buildY4M(validY4MStream(16, 16, 30000, 1001, 1), 16, 16)
	if err := inspectVideoErr(payload); err != nil {
		t.Fatalf("DEFECT-Y5: standard NTSC 30000:1001 stream rejected: %v", err)
	}
}
