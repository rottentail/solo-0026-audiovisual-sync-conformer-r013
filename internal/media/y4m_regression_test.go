package media

import (
	"bytes"
	"testing"

	"github.com/rottentail/solo-0026-audiovisual-sync-conformer/internal/domain"
)

type y4mCase struct {
	name       string
	width      int // geometry used for body generation
	height     int
	build      func() []byte
	wantError  bool
	wantFrames int
}

func y4mRegressionCases() []y4mCase {
	const w, h = 16, 16
	return []y4mCase{
		{
			name:       "valid minimal three frame stream",
			width:      w,
			height:     h,
			build:      func() []byte { return buildY4M(validY4MStream(w, h, 24, 1, 3), w, h) },
			wantFrames: 3,
		},
		{
			name:   "valid all accepted chroma spellings",
			width:  w,
			height: h,
			build: func() []byte {
				stream := validY4MStream(w, h, 24, 1, 1)
				stream.headerTokens = defaultY4MHeader(w, h, 24, 1, "C420")
				return buildY4M(stream, w, h)
			},
			wantFrames: 1,
		},
		{
			name:   "valid mpeg2 chroma spelling",
			width:  w,
			height: h,
			build: func() []byte {
				stream := validY4MStream(w, h, 25, 1, 2)
				stream.headerTokens = defaultY4MHeader(w, h, 25, 1, "C420mpeg2")
				return buildY4M(stream, w, h)
			},
			wantFrames: 2,
		},
		{
			name:   "valid fractional frame rate 120:1001 (~11.99 fps)",
			width:  w,
			height: h,
			build: func() []byte {
				return buildY4M(validY4MStream(w, h, 120, 1001, 1), w, h)
			},
			wantFrames: 1,
		},
		{
			name:   "valid frame with X comment parameters",
			width:  w,
			height: h,
			build: func() []byte {
				stream := validY4MStream(w, h, 24, 1, 2)
				stream.frameHeaders[0] = []string{"FRAME", "Xcomment=hello"}
				return buildY4M(stream, w, h)
			},
			wantFrames: 2,
		},
		{
			name:   "valid max geometry 1920x1080 single frame",
			width:  1920,
			height: 1080,
			build: func() []byte {
				return buildY4M(validY4MStream(1920, 1080, 24, 1, 1), 1920, 1080)
			},
			wantFrames: 1,
		},
		{
			name:       "valid frame count at limit 1200",
			width:      w,
			height:     h,
			build:      func() []byte { return buildY4M(validY4MStream(w, h, 24, 1, 1200), w, h) },
			wantFrames: 1200,
		},

		// --- structural rejection cases ---
		{name: "empty payload", build: func() []byte { return nil }, wantError: true},
		{name: "no newline anywhere", build: func() []byte {
			return []byte("YUV4MPEG2 W16 H16 F24:1 Ip C420jpeg")
		}, wantError: true},
		{name: "wrong magic", build: func() []byte {
			b := buildY4M(validY4MStream(w, h, 24, 1, 1), w, h)
			copy(b[0:9], "YUV4MPEG3")
			return b
		}, wantError: true},
		{name: "missing width", build: func() []byte {
			stream := validY4MStream(w, h, 24, 1, 1)
			stream.headerTokens = []string{"YUV4MPEG2", "H16", "F24:1", "Ip", "C420jpeg"}
			return buildY4M(stream, w, h)
		}, wantError: true},
		{name: "odd width", build: func() []byte {
			return buildY4M(validY4MStream(15, 16, 24, 1, 1), 15, 16)
		}, wantError: true},
		{name: "odd height", build: func() []byte {
			return buildY4M(validY4MStream(16, 15, 24, 1, 1), 16, 15)
		}, wantError: true},
		{name: "zero dimensions", build: func() []byte {
			stream := validY4MStream(w, h, 24, 1, 1)
			stream.headerTokens = defaultY4MHeader(0, 0, 24, 1)
			return buildY4M(stream, 0, 0)
		}, wantError: true},
		{name: "width over limit", build: func() []byte {
			return buildY4M(validY4MStream(1922, 16, 24, 1, 1), 1922, 16)
		}, wantError: true},
		{name: "height over limit", build: func() []byte {
			return buildY4M(validY4MStream(16, 1082, 24, 1, 1), 16, 1082)
		}, wantError: true},
		{name: "non numeric width token", build: func() []byte {
			stream := validY4MStream(w, h, 24, 1, 1)
			stream.headerTokens = defaultY4MHeader(w, h, 24, 1)
			stream.headerTokens[1] = "Wxx"
			return buildY4M(stream, w, h)
		}, wantError: true},
		{name: "frame rate missing colon", build: func() []byte {
			stream := validY4MStream(w, h, 24, 1, 1)
			stream.headerTokens = defaultY4MHeader(w, h, 24, 1)
			stream.headerTokens[3] = "F24"
			return buildY4M(stream, w, h)
		}, wantError: true},
		{name: "zero frame rate numerator", build: func() []byte {
			stream := validY4MStream(w, h, 24, 1, 1)
			stream.headerTokens = defaultY4MHeader(w, h, 24, 1)
			stream.headerTokens[3] = "F0:1"
			return buildY4M(stream, w, h)
		}, wantError: true},
		{name: "frame rate numerator over 120", build: func() []byte {
			return buildY4M(validY4MStream(w, h, 121, 1, 1), w, h)
		}, wantError: true},
		{name: "frame rate denominator over 1001", build: func() []byte {
			return buildY4M(validY4MStream(w, h, 24, 1002, 1), w, h)
		}, wantError: true},
		{name: "unsupported chroma 422", build: func() []byte {
			stream := validY4MStream(w, h, 24, 1, 1)
			stream.headerTokens = defaultY4MHeader(w, h, 24, 1, "C422")
			return buildY4M(stream, w, h)
		}, wantError: true},
		{name: "interlaced stream header rejected", build: func() []byte {
			stream := validY4MStream(w, h, 24, 1, 1)
			stream.headerTokens = defaultY4MHeader(w, h, 24, 1)
			stream.headerTokens[4] = "It"
			return buildY4M(stream, w, h)
		}, wantError: true},
		{name: "frame without FRAME marker", build: func() []byte {
			stream := validY4MStream(w, h, 24, 1, 1)
			stream.frameHeaders[0] = []string{"FAME"}
			return buildY4M(stream, w, h)
		}, wantError: true},
		{name: "truncated frame payload", build: func() []byte {
			stream := validY4MStream(w, h, 24, 1, 1)
			stream.frameBodies = [][]byte{make([]byte, 10)}
			return buildY4M(stream, w, h)
		}, wantError: true},
		{name: "no frames", build: func() []byte {
			return buildY4M(y4mStream{headerTokens: defaultY4MHeader(w, h, 24, 1)}, w, h)
		}, wantError: true},
		{name: "frame count over limit 1201", build: func() []byte {
			return buildY4M(validY4MStream(w, h, 24, 1, domain.MaxVideoFrames+1), w, h)
		}, wantError: true},
		{name: "single letter malformed header token", build: func() []byte {
			stream := validY4MStream(w, h, 24, 1, 1)
			stream.headerTokens = append(stream.headerTokens, "X")
			return buildY4M(stream, w, h)
		}, wantError: true},
	}
}

func TestY4MRegressionCorpus(t *testing.T) {
	for _, tc := range y4mRegressionCases() {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.build()
			clip, err := InspectVideo(data)
			if tc.wantError {
				if err == nil {
					t.Fatalf("InspectVideo accepted malformed payload: %+v", clip.Info)
				}
				return
			}
			if err != nil {
				t.Fatalf("InspectVideo rejected valid payload: %v", err)
			}
			if clip.Info.FrameCount != tc.wantFrames {
				t.Errorf("FrameCount = %d, want %d", clip.Info.FrameCount, tc.wantFrames)
			}
			if clip.Info.Digest != DigestBytes(data) {
				t.Error("descriptor digest does not match input bytes")
			}
			if clip.Info.ByteCount != len(data) {
				t.Errorf("ByteCount = %d, want %d", clip.Info.ByteCount, len(data))
			}
			wantFrameBytes := c420FrameSize(tc.width, tc.height)
			for i, frame := range clip.Frames {
				if len(frame) != wantFrameBytes {
					t.Fatalf("frame %d length = %d, want %d", i, len(frame), wantFrameBytes)
				}
			}
		})
	}
}

// TestY4MFrameContentPreserved proves every accepted frame's plane bytes are
// copied exactly into the in-memory representation used by analysis/export.
func TestY4MFrameContentPreserved(t *testing.T) {
	const w, h = 24, 18
	stream := validY4MStream(w, h, 24, 1, 4)
	original := buildY4M(stream, w, h)
	clip, err := InspectVideo(original)
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	frameSize := c420FrameSize(w, h)
	for i, frame := range clip.Frames {
		headerLen := bytes.IndexByte(original, '\n') + 1
		start := headerLen + i*(len("FRAME\n")+frameSize) + len("FRAME\n")
		want := original[start : start+frameSize]
		if !bytes.Equal(frame, want) {
			t.Fatalf("frame %d bytes were altered by the decoder", i)
		}
	}
}

// TestY4MDurationMath checks descriptor timing against hand calculation.
func TestY4MDurationMath(t *testing.T) {
	const w, h = 16, 16
	// 3 frames at 24 fps -> 3*1000/24 = 125 ms.
	clip, err := InspectVideo(buildY4M(validY4MStream(w, h, 24, 1, 3), w, h))
	if err != nil {
		t.Fatalf("InspectVideo: %v", err)
	}
	if clip.Info.DurationMS != 125 {
		t.Errorf("DurationMS = %d, want 125", clip.Info.DurationMS)
	}
	if clip.Info.Width != w || clip.Info.Height != h {
		t.Errorf("geometry = %dx%d, want %dx%d", clip.Info.Width, clip.Info.Height, w, h)
	}
	// Fractional fps under the numerator ceiling: 1 frame at 120:1001 -> 8341 ms.
	clip2, err := InspectVideo(buildY4M(validY4MStream(w, h, 120, 1001, 1), w, h))
	if err != nil {
		t.Fatalf("InspectVideo fractional: %v", err)
	}
	if clip2.Info.DurationMS != 8341 {
		t.Errorf("fractional DurationMS = %d, want 8341", clip2.Info.DurationMS)
	}
}

// TestY4MEncodeDecodeRoundTrip: valid streams must keep frame order and plane
// bytes through Inspect -> Encode, the exact path used at conform export.
func TestY4MEncodeDecodeRoundTrip(t *testing.T) {
	const w, h = 32, 32
	for _, chroma := range []string{"C420jpeg", "C420", "C420mpeg2"} {
		stream := validY4MStream(w, h, 24, 1, 5)
		stream.headerTokens = defaultY4MHeader(w, h, 24, 1, chroma)
		original := buildY4M(stream, w, h)
		clip, err := InspectVideo(original)
		if err != nil {
			t.Fatalf("chroma %s inspect: %v", chroma, err)
		}
		encoded, err := EncodeY4M(clip.Info, clip.Frames)
		if err != nil {
			t.Fatalf("chroma %s encode: %v", chroma, err)
		}
		reInspected, err := InspectVideo(encoded)
		if err != nil {
			t.Fatalf("chroma %s re-inspect: %v", chroma, err)
		}
		if len(reInspected.Frames) != len(clip.Frames) {
			t.Fatalf("chroma %s frame count changed", chroma)
		}
		for i := range clip.Frames {
			if !bytes.Equal(reInspected.Frames[i], clip.Frames[i]) {
				t.Fatalf("chroma %s frame %d changed through export", chroma, i)
			}
		}
		if reInspected.Info.Chroma != chroma {
			t.Errorf("chroma %s became %s on export", chroma, reInspected.Info.Chroma)
		}
	}
}
