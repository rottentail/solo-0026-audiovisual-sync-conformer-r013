package media

import (
	"bytes"
	"testing"
)

// fuzzCorpusBudget bounds the work a single fuzz input may demand. The
// production service independently caps admission at MaxAudioBytes /
// MaxVideoBytes; these mirrors keep the fuzz worker itself from being used as
// a resource-exhaustion amplifier and also pin the library-level contract.
const (
	fuzzAudioBudget = 8 << 20
	fuzzVideoBudget = 24 << 20
)

// FuzzInspectAudio throws arbitrary byte streams at the real WAV decoder
// boundary. Invariants under test:
//
//  1. the decoder never panics, regardless of declared sizes or layout;
//  2. every accepted file describes a stream whose samples are fully backed
//     by the input bytes (no phantom samples, no out-of-range slicing);
//  3. descriptors always match the bytes the decoder actually walked;
//  4. calling the decoder twice on the same bytes gives the same verdict.
func FuzzInspectAudio(f *testing.F) {
	seedAudioCorpus(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzAudioBudget {
			t.Skip("input beyond the admission budget is enforced upstream")
		}
		clip1, err1 := InspectAudio(data)
		_, err2 := InspectAudio(data)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("non-deterministic verdict: %v vs %v", err1, err2)
		}
		if err1 != nil {
			return
		}
		// Accepted-file invariants.
		if clip1.Info.SampleRate < 8000 || clip1.Info.SampleRate > 192000 {
			t.Fatalf("accepted sample rate outside policy: %d", clip1.Info.SampleRate)
		}
		if clip1.Info.Channels != 1 && clip1.Info.Channels != 2 {
			t.Fatalf("accepted channel count: %d", clip1.Info.Channels)
		}
		if clip1.Info.BitDepth != 16 {
			t.Fatalf("accepted bit depth: %d", clip1.Info.BitDepth)
		}
		if len(clip1.Samples) == 0 {
			t.Fatal("accepted file with zero samples")
		}
		if len(clip1.Samples)%clip1.Info.Channels != 0 {
			t.Fatalf("sample count %d not channel aligned (%d ch)", len(clip1.Samples), clip1.Info.Channels)
		}
		if clip1.Info.FrameCount != len(clip1.Samples)/clip1.Info.Channels {
			t.Fatalf("FrameCount %d != samples/channels %d", clip1.Info.FrameCount, len(clip1.Samples)/clip1.Info.Channels)
		}
		if clip1.Info.ByteCount != len(data) || clip1.Info.Digest != DigestBytes(data) {
			t.Fatal("descriptor does not describe the exact input bytes")
		}
		// Every decoded sample must correspond to two bytes inside the
		// accepted data chunk; the dataChunkBytes helper walks the same
		// container independently.
		backed := dataChunkBytes(data)
		if backed < 0 || backed != len(clip1.Samples)*2 {
			t.Fatalf("decoded %d samples but data chunk holds %d bytes", len(clip1.Samples), backed)
		}
		// Re-encoding an accepted stream must itself be accepted.
		reEncoded, err := EncodePCM16(clip1.Info.SampleRate, clip1.Info.Channels, clip1.Samples)
		if err != nil {
			t.Fatalf("re-encode accepted stream failed: %v", err)
		}
		reClip, err := InspectAudio(reEncoded)
		if err != nil {
			t.Fatalf("re-encoded stream rejected: %v", err)
		}
		if reClip.Info.SampleRate != clip1.Info.SampleRate ||
			reClip.Info.Channels != clip1.Info.Channels ||
			len(reClip.Samples) != len(clip1.Samples) {
			t.Fatal("re-encoded stream changed the declared media meaning")
		}
	})
}

// seedAudioCorpus registers hand-built boundary inputs as the fixed seed
// corpus. They live in code (rather than only testdata files) so a regression
// reader can see intent next to the builder that produced each case.
func seedAudioCorpus(f *testing.F) {
	pcm := pcmSine(1, 32, 48000)
	fmtGood := buildFmtChunk(defaultWAVFmt(), 16)
	f.Add(buildWAV(-1, fmtGood, buildDataChunk(pcm)))
	f.Add(buildWAV(-1, fmtGood))
	f.Add(buildWAV(-1, buildDataChunk(pcm)))
	f.Add([]byte{})
	f.Add([]byte("RIFF"))
	f.Add([]byte("RIFF\x00\x00\x00\x00WAVE"))
	// Truncations at every structural boundary.
	good := buildWAV(-1, fmtGood, buildDataChunk(pcm))
	for cut := 0; cut < len(good); cut += 7 {
		f.Add(good[:cut])
	}
	// Header field extremes.
	extremeFmt := func(mutate func(*wavFmtFields)) []byte {
		fields := defaultWAVFmt()
		mutate(&fields)
		return buildWAV(-1, buildFmtChunk(fields, 16), buildDataChunk(pcm))
	}
	f.Add(extremeFmt(func(x *wavFmtFields) { x.sampleRate = 0 }))
	f.Add(extremeFmt(func(x *wavFmtFields) { x.sampleRate = 7999 }))
	f.Add(extremeFmt(func(x *wavFmtFields) { x.sampleRate = 192001 }))
	f.Add(extremeFmt(func(x *wavFmtFields) { x.channels = 0 }))
	f.Add(extremeFmt(func(x *wavFmtFields) { x.channels = 3 }))
	f.Add(extremeFmt(func(x *wavFmtFields) { x.bitsPerSample = 8 }))
	f.Add(extremeFmt(func(x *wavFmtFields) { x.bitsPerSample = 32 }))
	f.Add(extremeFmt(func(x *wavFmtFields) { x.audioFormat = 0 }))
	f.Add(extremeFmt(func(x *wavFmtFields) { x.audioFormat = 0xFFFF }))
	f.Add(extremeFmt(func(x *wavFmtFields) { x.blockAlign = 0 }))
	f.Add(extremeFmt(func(x *wavFmtFields) { x.byteRate = 0 }))
	// Chunk size extremes and padding edge cases.
	f.Add(buildWAV(-1, buildFmtChunk(defaultWAVFmt(), 16), wavChunk{id: "data", body: []byte{0}}))
	f.Add(buildWAV(-1, buildFmtChunk(defaultWAVFmt(), 16), wavChunk{id: "data", body: []byte{0, 0, 0}}))
	f.Add(buildWAV(-1, buildFmtChunk(wavFmtFields{
		audioFormat: 1, channels: 1, sampleRate: 48000, byteRate: 96000, blockAlign: 2, bitsPerSample: 16,
	}, 18), buildDataChunk(pcm)))
	// RIFF size lying in both directions.
	f.Add(buildWAV(4, fmtGood, buildDataChunk(pcm)))
	f.Add(buildWAV(1<<31-1, fmtGood, buildDataChunk(pcm)))
	f.Add(buildWAV(int64(len(good))-1, fmtGood, buildDataChunk(pcm)))
	// Chunk ordering permutations and unknown chunks.
	f.Add(buildWAV(-1, buildDataChunk(pcm), fmtGood))
	f.Add(buildWAV(-1, fmtGood, buildUnknownChunk("JUNK", make([]byte, 256)), buildDataChunk(pcm)))
	f.Add(buildWAV(-1, fmtGood, fmtGood, buildDataChunk(pcm)))
	f.Add(buildWAV(-1, fmtGood, buildDataChunk(pcm), buildDataChunk(pcm)))
	// Random-looking binary soup.
	f.Add([]byte{0x52, 0x49, 0x46, 0x46, 0xFF, 0xFF, 0xFF, 0xFF, 0x57, 0x41, 0x56, 0x45, 0x00})
}

// FuzzInspectVideo throws arbitrary byte streams at the real Y4M decoder.
func FuzzInspectVideo(f *testing.F) {
	seedVideoCorpus(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzVideoBudget {
			t.Skip("input beyond the admission budget is enforced upstream")
		}
		clip1, err1 := InspectVideo(data)
		_, err2 := InspectVideo(data)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("non-deterministic verdict: %v vs %v", err1, err2)
		}
		if err1 != nil {
			return
		}
		if clip1.Info.Width <= 0 || clip1.Info.Height <= 0 {
			t.Fatal("accepted non-positive geometry")
		}
		if clip1.Info.Width%2 != 0 || clip1.Info.Height%2 != 0 {
			t.Fatal("accepted odd C420 geometry")
		}
		if clip1.Info.FrameCount <= 0 || clip1.Info.FrameCount > 1200 {
			t.Fatalf("accepted frame count outside limit: %d", clip1.Info.FrameCount)
		}
		frameBytes := c420FrameSize(clip1.Info.Width, clip1.Info.Height)
		for i, frame := range clip1.Frames {
			if len(frame) != frameBytes {
				t.Fatalf("frame %d length %d != geometry %d", i, len(frame), frameBytes)
			}
		}
		if clip1.Info.ByteCount != len(data) || clip1.Info.Digest != DigestBytes(data) {
			t.Fatal("descriptor does not describe the exact input bytes")
		}
		if clip1.Info.FPSNum <= 0 || clip1.Info.FPSDen <= 0 {
			t.Fatal("accepted non-positive frame rate")
		}
		// Re-emit through the production export path and re-decode: geometry,
		// frame count and every plane byte must survive unchanged.
		encoded, err := EncodeY4M(clip1.Info, clip1.Frames)
		if err != nil {
			t.Fatalf("export accepted stream failed: %v", err)
		}
		reClip, err := InspectVideo(encoded)
		if err != nil {
			t.Fatalf("exported stream rejected: %v", err)
		}
		if reClip.Info.Width != clip1.Info.Width ||
			reClip.Info.Height != clip1.Info.Height ||
			reClip.Info.FrameCount != clip1.Info.FrameCount {
			t.Fatal("export changed geometry or frame count")
		}
		for i := range clip1.Frames {
			if !bytes.Equal(reClip.Frames[i], clip1.Frames[i]) {
				t.Fatalf("frame %d plane bytes changed through export", i)
			}
		}
	})
}

func seedVideoCorpus(f *testing.F) {
	good := buildY4M(validY4MStream(16, 16, 24, 1, 3), 16, 16)
	f.Add(good)
	f.Add([]byte{})
	f.Add([]byte("YUV4MPEG2\n"))
	// Truncations across header/frame boundaries.
	for cut := 0; cut < len(good); cut += 31 {
		f.Add(good[:cut])
	}
	// Header parameter extremes.
	tokens := func(mutate func(tokens []string) []string) []byte {
		stream := validY4MStream(16, 16, 24, 1, 1)
		stream.headerTokens = mutate(defaultY4MHeader(16, 16, 24, 1))
		return buildY4M(stream, 16, 16)
	}
	f.Add(tokens(func(t []string) []string { t[1] = "W0"; return t }))
	f.Add(tokens(func(t []string) []string { t[1] = "W-2"; return t }))
	f.Add(tokens(func(t []string) []string { t[1] = "W1921"; return t }))
	f.Add(tokens(func(t []string) []string { t[2] = "H1081"; return t }))
	f.Add(tokens(func(t []string) []string { t[3] = "F121:1"; return t }))
	f.Add(tokens(func(t []string) []string { t[3] = "F24:1002"; return t }))
	f.Add(tokens(func(t []string) []string { t[4] = "It"; return t }))
	f.Add(tokens(func(t []string) []string { t[6] = "C422"; return t }))
	f.Add(tokens(func(t []string) []string { return append(t, "X") }))
	f.Add(tokens(func(t []string) []string { return append(t, "W32") }))
	// Frame-level corruption patterns.
	corruptFrame := func(mutate func(s *y4mStream)) []byte {
		stream := validY4MStream(16, 16, 24, 1, 2)
		mutate(&stream)
		return buildY4M(stream, 16, 16)
	}
	f.Add(corruptFrame(func(s *y4mStream) { s.frameHeaders[0] = []string{"FAME"} }))
	f.Add(corruptFrame(func(s *y4mStream) { s.frameBodies = [][]byte{make([]byte, 5), nil} }))
	f.Add(corruptFrame(func(s *y4mStream) { s.frameHeaders[0] = []string{"FRAME", "It"} }))
	f.Add(corruptFrame(func(s *y4mStream) { s.frameHeaders[0] = []string{"FRAME", "C422"} }))
	f.Add(corruptFrame(func(s *y4mStream) { s.newlineCRLF = true }))
	// Soup.
	f.Add(append([]byte("YUV4MPEG2 W999999 H999999 F1:1 Ip C420jpeg\nFRAME\n"), make([]byte, 64)...))
}
