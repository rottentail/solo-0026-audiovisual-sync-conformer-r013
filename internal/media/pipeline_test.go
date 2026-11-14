package media

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"

	"github.com/rottentail/solo-0026-audiovisual-sync-conformer/internal/domain"
)

// TestPipelineContentMeaningPreserved drives valid material through every
// production media stage a take crosses after admission:
//
//	Inspect -> AnalyzeSync -> AlignAudio / NormalizeY4M -> BuildReviewBundle
//
// and asserts the exported artifacts keep the original content meaning:
// sample-accurate audio timing shift (with zero-fill at the edges only),
// unchanged picture planes/order/geometry, and a receipt whose digests match
// the bytes actually inside the ZIP.
func TestPipelineContentMeaningPreserved(t *testing.T) {
	audio, video, err := CalibrationPair()
	if err != nil {
		t.Fatalf("CalibrationPair: %v", err)
	}
	audioClip, err := InspectAudio(audio)
	if err != nil {
		t.Fatalf("InspectAudio: %v", err)
	}
	videoClip, err := InspectVideo(video)
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}

	measurement, err := AnalyzeSync(audio, video)
	if err != nil {
		t.Fatalf("AnalyzeSync: %v", err)
	}
	if measurement.OffsetMS != 125 {
		t.Fatalf("calibration offset = %d ms, want 125", measurement.OffsetMS)
	}

	aligned, err := AlignAudio(audio, measurement.OffsetMS, 1000)
	if err != nil {
		t.Fatalf("AlignAudio: %v", err)
	}
	alignedClip, err := InspectAudio(aligned.WAV)
	if err != nil {
		t.Fatalf("inspect aligned wav: %v", err)
	}
	if alignedClip.Info.SampleRate != audioClip.Info.SampleRate ||
		alignedClip.Info.Channels != audioClip.Info.Channels ||
		len(alignedClip.Samples) != len(audioClip.Samples) {
		t.Fatal("alignment changed sample rate, channel count or sample count")
	}
	expectedShift := measurement.OffsetMS * audioClip.Info.SampleRate / 1000
	if aligned.AppliedSamples != expectedShift {
		t.Fatalf("AppliedSamples = %d, want %d", aligned.AppliedSamples, expectedShift)
	}
	// Interior content must be an exact sample-level shift: every output
	// sample equals the input sample at index+shift; only edges may zero-fill.
	for i := range alignedClip.Samples {
		source := i + expectedShift*audioClip.Info.Channels
		var want int16
		if source >= 0 && source < len(audioClip.Samples) {
			want = audioClip.Samples[source]
		}
		if alignedClip.Samples[i] != want {
			t.Fatalf("aligned sample %d = %d, want %d (content meaning changed)", i, alignedClip.Samples[i], want)
		}
	}

	conformed, conformedInfo, err := NormalizeY4M(video)
	if err != nil {
		t.Fatalf("NormalizeY4M: %v", err)
	}
	reClip, err := InspectVideo(conformed)
	if err != nil {
		t.Fatalf("re-inspect conformed y4m: %v", err)
	}
	if reClip.Info.Width != videoClip.Info.Width ||
		reClip.Info.Height != videoClip.Info.Height ||
		reClip.Info.FrameCount != videoClip.Info.FrameCount ||
		reClip.Info.FPSNum != videoClip.Info.FPSNum ||
		reClip.Info.FPSDen != videoClip.Info.FPSDen {
		t.Fatal("conform changed picture geometry, timing or frame count")
	}
	for i := range videoClip.Frames {
		if !bytes.Equal(reClip.Frames[i], videoClip.Frames[i]) {
			t.Fatalf("conform altered frame %d plane bytes", i)
		}
	}
	if conformedInfo.Digest != DigestBytes(conformed) {
		t.Fatal("conformed info digest does not match emitted bytes")
	}

	// Aligning by zero must reproduce the exact original PCM samples.
	zeroAligned, err := AlignAudio(audio, 0, 1000)
	if err != nil {
		t.Fatalf("zero AlignAudio: %v", err)
	}
	zeroClip, err := InspectAudio(zeroAligned.WAV)
	if err != nil {
		t.Fatalf("inspect zero-aligned: %v", err)
	}
	if !equalInt16(zeroClip.Samples, audioClip.Samples) {
		t.Fatal("zero-offset alignment changed audio samples")
	}

	receipt := stubReceipt(aligned.Digest, conformedInfo.Digest)
	bundle, receiptBytes, err := BuildReviewBundle(aligned.WAV, conformed, receipt)
	if err != nil {
		t.Fatalf("BuildReviewBundle: %v", err)
	}
	entries := unzipForTest(t, bundle)
	for _, name := range []string{"audio-aligned.wav", "video-conformed.y4m", "conform-receipt.json"} {
		if len(entries[name]) == 0 {
			t.Fatalf("bundle entry %s missing", name)
		}
	}
	if !bytes.Equal(entries["audio-aligned.wav"], aligned.WAV) {
		t.Fatal("bundle audio bytes differ from aligned output")
	}
	if !bytes.Equal(entries["video-conformed.y4m"], conformed) {
		t.Fatal("bundle video bytes differ from conformed output")
	}
	if !bytes.Equal(entries["conform-receipt.json"], receiptBytes) {
		t.Fatal("bundle receipt bytes differ from returned receipt")
	}
	// Bundle digest must equal an independently computed SHA-256 of the
	// exact archived bytes (not just the production helper re-reading itself).
	sum := sha256.Sum256(bundle)
	independentDigest := hex.EncodeToString(sum[:])
	if DigestBytes(bundle) != independentDigest {
		t.Fatal("bundle digest does not match independent SHA-256 of the bytes")
	}
	if independentDigest == "" {
		t.Fatal("bundle digest is empty")
	}
}

// TestPipelineNegativeAndCorrectionCeiling keeps the failure side stable:
// invalid media never enters analysis and over-ceiling corrections fail
// without emitting a bundle-able WAV.
func TestPipelineNegativeAndCorrectionCeiling(t *testing.T) {
	audio, video, err := CalibrationPair()
	if err != nil {
		t.Fatalf("CalibrationPair: %v", err)
	}
	if _, err := AnalyzeSync([]byte("not riff"), video); err == nil {
		t.Fatal("AnalyzeSync accepted non-WAV audio")
	}
	if _, err := AnalyzeSync(audio, []byte("not y4m")); err == nil {
		t.Fatal("AnalyzeSync accepted non-Y4M video")
	}
	if _, err := AlignAudio(audio, 501, 500); err == nil {
		t.Fatal("AlignAudio accepted correction above ceiling")
	}
	if _, err := AlignAudio([]byte("not riff"), 0, 500); err == nil {
		t.Fatal("AlignAudio accepted malformed audio")
	}
}

// TestResourceBoundedness constructs declaration/body attacks and asserts the
// decoders reject them after bounded work, instead of allocating for the
// declared (but absent) extent. These are the "resource runaway" regression
// guards: a change that starts trusting declared sizes will trip them.
func TestResourceBoundedness(t *testing.T) {
	t.Run("wav riff size cannot force phantom allocation", func(t *testing.T) {
		// Tiny payload advertising a near-4GB RIFF extent.
		payload := buildWAV(1<<31-1, buildFmtChunk(defaultWAVFmt(), 16), buildDataChunk(pcmSine(1, 4, 48000)))
		if clip, err := InspectAudio(payload); err == nil {
			t.Fatalf("decoder trusted overstated RIFF size: %+v", clip.Info)
		}
	})
	t.Run("wav chunk size cannot exceed payload", func(t *testing.T) {
		var b bytes.Buffer
		b.WriteString("RIFF")
		b.Write(make([]byte, 4)) // patched below
		b.WriteString("WAVE")
		b.WriteString("data")
		b.Write([]byte{0xFF, 0xFF, 0xFF, 0x7F})
		b.Write(make([]byte, 16))
		raw := b.Bytes()
		raw[4] = byte(len(raw) - 8)
		if clip, err := InspectAudio(raw); err == nil {
			t.Fatalf("decoder trusted overstated chunk size: %+v", clip.Info)
		}
	})
	t.Run("y4m huge geometry with small body rejected cheaply", func(t *testing.T) {
		stream := validY4MStream(1920, 1080, 24, 1, 1)
		stream.frameBodies = [][]byte{make([]byte, 64)}
		payload := buildY4M(stream, 1920, 1080)
		if clip, err := InspectVideo(payload); err == nil {
			t.Fatalf("decoder accepted truncated huge frame: %+v", clip.Info)
		}
	})
	t.Run("y4m frame limit enforced at the boundary", func(t *testing.T) {
		over := buildY4M(validY4MStream(8, 8, 24, 1, 1201), 8, 8)
		if clip, err := InspectVideo(over); err == nil {
			t.Fatalf("decoder accepted 1201 frames: %+v", clip.Info)
		}
		atLimit := buildY4M(validY4MStream(8, 8, 24, 1, 1200), 8, 8)
		clip, err := InspectVideo(atLimit)
		if err != nil {
			t.Fatalf("1200-frame stream rejected: %v", err)
		}
		if clip.Info.FrameCount != 1200 {
			t.Fatalf("FrameCount = %d, want 1200", clip.Info.FrameCount)
		}
	})
}

func stubReceipt(audioDigest, videoDigest string) domain.ConformReceipt {
	return domain.ConformReceipt{
		SchemaVersion:    1,
		ConformID:        "fuzz-conform",
		AnalysisID:       "fuzz-analysis",
		TakeID:           "fuzz-take",
		MeasuredOffsetMS: 125,
		AppliedOffsetMS:  125,
		AppliedSamples:   6000,
		AudioDigest:      audioDigest,
		VideoDigest:      videoDigest,
		InputTakeDigest:  "test-digest",
	}
}

func unzipForTest(t *testing.T, bundle []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	entries := make(map[string][]byte)
	for _, entry := range reader.File {
		file, err := entry.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", entry.Name, err)
		}
		content, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatalf("read entry %s: %v", entry.Name, err)
		}
		entries[entry.Name] = content
	}
	return entries
}

func equalInt16(a, b []int16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
