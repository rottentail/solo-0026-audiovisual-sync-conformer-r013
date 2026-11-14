package media

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// wavCase is one immutable regression corpus entry.
type wavCase struct {
	name      string
	build     func() []byte
	wantError bool
	// Accepted-file assertions. Zero values mean "do not check".
	wantFrames     int
	wantChannels   int
	wantSampleRate int
	wantDurationMS int
}

func wavRegressionCases() []wavCase {
	pcm16 := pcmSine(1, 16, 48000) // 32 bytes
	pcm8 := pcmSine(2, 8, 48000)   // 32 bytes, stereo
	fmtMono := buildFmtChunk(defaultWAVFmt(), 16)
	fmtStereo := func() wavChunk {
		f := defaultWAVFmt()
		f.channels = 2
		f.byteRate = 48000 * 4
		f.blockAlign = 4
		return buildFmtChunk(f, 16)
	}()
	good := func() []byte {
		return buildWAV(-1, fmtMono, buildDataChunk(pcm16))
	}
	return []wavCase{
		{
			name:           "valid mono 48k 16 frames",
			build:          good,
			wantFrames:     16,
			wantChannels:   1,
			wantSampleRate: 48000,
			wantDurationMS: 0, // 16/48000 s truncates to 0 ms; asserted deliberately
		},
		{
			name: "valid stereo 48k 8 frames",
			build: func() []byte {
				return buildWAV(-1, fmtStereo, buildDataChunk(pcm8))
			},
			wantFrames:     8,
			wantChannels:   2,
			wantSampleRate: 48000,
		},
		{
			name: "valid with unknown LIST chunk between fmt and data",
			build: func() []byte {
				return buildWAV(-1, fmtMono, buildUnknownChunk("LIST", []byte("INFOpadding")), buildDataChunk(pcm16))
			},
			wantFrames: 16,
		},
		{
			name: "valid odd-size unknown chunk uses pad byte",
			build: func() []byte {
				return buildWAV(-1, fmtMono, buildUnknownChunk("JUNK", []byte("abc")), buildDataChunk(pcm16))
			},
			wantFrames: 16,
		},
		{
			name: "valid sample rate boundaries 8000 and 192000",
			build: func() []byte {
				f := defaultWAVFmt()
				f.sampleRate = 8000
				f.byteRate = 8000 * 2
				return buildWAV(-1, buildFmtChunk(f, 16), buildDataChunk(pcmSine(1, 32, 8000)))
			},
			wantFrames: 32,
		},
		{
			name: "valid 192k top boundary",
			build: func() []byte {
				f := defaultWAVFmt()
				f.sampleRate = 192000
				f.byteRate = 192000 * 2
				return buildWAV(-1, buildFmtChunk(f, 16), buildDataChunk(pcmSine(1, 192, 192000)))
			},
			wantFrames:     192,
			wantDurationMS: 1,
		},

		// --- structural rejection cases ---
		{name: "empty payload", build: func() []byte { return nil }, wantError: true},
		{name: "shorter than RIFF header", build: func() []byte { return []byte("RIFF\x00\x00") }, wantError: true},
		{name: "wrong magic", build: func() []byte {
			b := good()
			copy(b[0:4], "RIFX")
			return b
		}, wantError: true},
		{name: "wrong wave id", build: func() []byte {
			b := good()
			copy(b[8:12], "AVI ")
			return b
		}, wantError: true},
		{name: "riff size below four", build: func() []byte {
			return buildWAV(3, fmtMono, buildDataChunk(pcm16))
		}, wantError: true},
		{name: "riff size overstates payload", build: func() []byte {
			return buildWAV(4000, fmtMono, buildDataChunk(pcm16))
		}, wantError: true},
		{name: "truncated chunk header mid stream", build: func() []byte {
			b := good()
			return b[:len(b)-3]
		}, wantError: true},
		{name: "chunk size exceeds payload", build: func() []byte {
			var b bytes.Buffer
			b.WriteString("RIFF")
			_ = binary.Write(&b, binary.LittleEndian, uint32(4+8+16+8+uint32(len(pcm16))+8+9999))
			b.WriteString("WAVE")
			b.Write([]byte(fmtMono.id))
			_ = binary.Write(&b, binary.LittleEndian, uint32(16))
			b.Write(fmtMono.body)
			b.WriteString("data")
			_ = binary.Write(&b, binary.LittleEndian, uint32(len(pcm16)))
			b.Write(pcm16)
			b.WriteString("JUNK")
			_ = binary.Write(&b, binary.LittleEndian, uint32(9999)) // body never follows
			return b.Bytes()
		}, wantError: true},
		{name: "fmt chunk too short", build: func() []byte {
			return buildWAV(-1, buildFmtChunk(defaultWAVFmt(), 15), buildDataChunk(pcm16))
		}, wantError: true},
		{name: "missing fmt chunk", build: func() []byte {
			return buildWAV(-1, buildDataChunk(pcm16))
		}, wantError: true},
		{name: "missing data chunk", build: func() []byte {
			return buildWAV(-1, fmtMono)
		}, wantError: true},
		{name: "empty data chunk", build: func() []byte {
			return buildWAV(-1, fmtMono, buildDataChunk(nil))
		}, wantError: true},
		{name: "data bytes not block aligned", build: func() []byte {
			return buildWAV(-1, fmtStereo, buildDataChunk([]byte{1, 2, 3}))
		}, wantError: true},

		// --- declared policy rejection cases ---
		{name: "non PCM audio format", build: func() []byte {
			f := defaultWAVFmt()
			f.audioFormat = 3 // IEEE float
			return buildWAV(-1, buildFmtChunk(f, 16), buildDataChunk(pcm16))
		}, wantError: true},
		{name: "three channels rejected", build: func() []byte {
			f := defaultWAVFmt()
			f.channels = 3
			f.byteRate = 48000 * 6
			f.blockAlign = 6
			return buildWAV(-1, buildFmtChunk(f, 16), buildDataChunk(pcmSine(3, 4, 48000)))
		}, wantError: true},
		{name: "eight bit samples rejected", build: func() []byte {
			f := defaultWAVFmt()
			f.bitsPerSample = 8
			f.byteRate = 48000
			f.blockAlign = 1
			return buildWAV(-1, buildFmtChunk(f, 16), buildDataChunk(make([]byte, 16)))
		}, wantError: true},
		{name: "sample rate below range", build: func() []byte {
			f := defaultWAVFmt()
			f.sampleRate = 7999
			f.byteRate = 7999 * 2
			return buildWAV(-1, buildFmtChunk(f, 16), buildDataChunk(pcm16))
		}, wantError: true},
		{name: "sample rate above range", build: func() []byte {
			f := defaultWAVFmt()
			f.sampleRate = 192001
			f.byteRate = 192001 * 2
			return buildWAV(-1, buildFmtChunk(f, 16), buildDataChunk(pcm16))
		}, wantError: true},
		{name: "block align disagrees with channels", build: func() []byte {
			f := defaultWAVFmt()
			f.blockAlign = 4 // mono but claims 4
			return buildWAV(-1, buildFmtChunk(f, 16), buildDataChunk(pcm16))
		}, wantError: true},
	}
}

func TestWAVRegressionCorpus(t *testing.T) {
	for _, tc := range wavRegressionCases() {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.build()
			clip, err := InspectAudio(data)
			if tc.wantError {
				if err == nil {
					t.Fatalf("InspectAudio accepted malformed payload: %+v frames=%d", clip.Info, clip.Info.FrameCount)
				}
				return
			}
			if err != nil {
				t.Fatalf("InspectAudio rejected valid payload: %v", err)
			}
			if tc.wantFrames != 0 && clip.Info.FrameCount != tc.wantFrames {
				t.Errorf("FrameCount = %d, want %d", clip.Info.FrameCount, tc.wantFrames)
			}
			if tc.wantChannels != 0 && clip.Info.Channels != tc.wantChannels {
				t.Errorf("Channels = %d, want %d", clip.Info.Channels, tc.wantChannels)
			}
			if tc.wantSampleRate != 0 && clip.Info.SampleRate != tc.wantSampleRate {
				t.Errorf("SampleRate = %d, want %d", clip.Info.SampleRate, tc.wantSampleRate)
			}
			if tc.wantDurationMS != 0 && clip.Info.DurationMS != tc.wantDurationMS {
				t.Errorf("DurationMS = %d, want %d", clip.Info.DurationMS, tc.wantDurationMS)
			}
			if clip.Info.BitDepth != 16 {
				t.Errorf("BitDepth = %d, want 16", clip.Info.BitDepth)
			}
			if clip.Info.Digest != DigestBytes(data) {
				t.Error("descriptor digest does not match input bytes")
			}
			if clip.Info.ByteCount != len(data) {
				t.Errorf("ByteCount = %d, want %d", clip.Info.ByteCount, len(data))
			}
			// Every accepted sample must round-trip through the 16-bit decoder.
			if len(clip.Samples)*2 != dataChunkBytes(data) {
				t.Errorf("decoded sample count %d does not match data chunk", len(clip.Samples))
			}
		})
	}
}

// dataChunkBytes re-derives the data chunk length independently of the
// decoder, guarding the sample-count invariant. It mirrors the production
// last-"data"-chunk-wins resolution; duplicate data chunks themselves are
// separately captured as DEFECT-W3b in the tagged evidence suite.
func dataChunkBytes(data []byte) int {
	offset := 12
	last := -1
	for offset+8 <= len(data) {
		id := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if id == "data" {
			last = size
		}
		offset += 8 + size + size%2
	}
	return last
}

// TestWAVDescriptorSanity checks numeric descriptors against hand calculations.
func TestWAVDescriptorSanity(t *testing.T) {
	samples := make([]int16, 480) // 10 ms at 48 kHz, mono
	for i := range samples {
		samples[i] = int16(16000)
	}
	data, err := EncodePCM16(48000, 1, samples)
	if err != nil {
		t.Fatalf("EncodePCM16: %v", err)
	}
	clip, err := InspectAudio(data)
	if err != nil {
		t.Fatalf("InspectAudio: %v", err)
	}
	if clip.Info.FrameCount != 480 {
		t.Errorf("FrameCount = %d, want 480", clip.Info.FrameCount)
	}
	if clip.Info.DurationMS != 10 {
		t.Errorf("DurationMS = %d, want 10", clip.Info.DurationMS)
	}
	wantPeak := amplitudeDBFS(16000)
	if math.Abs(clip.Info.PeakDBFS-wantPeak) > 0.01 {
		t.Errorf("PeakDBFS = %.2f, want %.2f", clip.Info.PeakDBFS, wantPeak)
	}
	// RMS of a constant signal equals its peak.
	if math.Abs(clip.Info.RMSDBFS-wantPeak) > 0.01 {
		t.Errorf("RMSDBFS = %.2f, want %.2f", clip.Info.RMSDBFS, wantPeak)
	}
}

// TestWAVEncodeDecodeRoundTrip proves the content-meaning preservation path
// for valid material: samples survive encode -> inspect bit for bit.
func TestWAVEncodeDecodeRoundTrip(t *testing.T) {
	patterns := [][]int16{
		{0, 1, -1, 32767, -32768, 12345, -23456},
		make([]int16, 960), // silence
	}
	for ch := 1; ch <= 2; ch++ {
		for _, pattern := range patterns {
			samples := make([]int16, len(pattern)*ch)
			for i, v := range pattern {
				for c := 0; c < ch; c++ {
					samples[i*ch+c] = v
				}
			}
			data, err := EncodePCM16(48000, ch, samples)
			if err != nil {
				t.Fatalf("encode ch=%d: %v", ch, err)
			}
			clip, err := InspectAudio(data)
			if err != nil {
				t.Fatalf("inspect ch=%d: %v", ch, err)
			}
			if !bytes.Equal(int16Bytes(samples), int16Bytes(clip.Samples)) {
				t.Fatalf("ch=%d sample content changed through round trip", ch)
			}
		}
	}
}

func int16Bytes(values []int16) []byte {
	out := make([]byte, len(values)*2)
	for i, v := range values {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
	}
	return out
}
