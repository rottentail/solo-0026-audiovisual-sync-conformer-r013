//go:build ignore

// evidence-gen materializes one minimal binary file per confirmed production
// defect under defects/evidence/. Each file is hand-built through the same
// byte-level builders as the regression corpus and is accompanied in
// DEFECTS.md by the exact decoder observation (accepted vs rejected and why
// that violates the spec). The production source tree is never modified.
//
// Run from the repository root:
//
//	go run ./internal/media/defect_evidence_gen.go
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type wavFmtFields struct {
	audioFormat, channels uint16
	sampleRate, byteRate  uint32
	blockAlign, bits      uint16
}

type wavChunk struct {
	id   string
	body []byte
}

func fmtChunk(f wavFmtFields) wavChunk {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, f.audioFormat)
	_ = binary.Write(&b, binary.LittleEndian, f.channels)
	_ = binary.Write(&b, binary.LittleEndian, f.sampleRate)
	_ = binary.Write(&b, binary.LittleEndian, f.byteRate)
	_ = binary.Write(&b, binary.LittleEndian, f.blockAlign)
	_ = binary.Write(&b, binary.LittleEndian, f.bits)
	return wavChunk{"fmt ", b.Bytes()}
}

func dataChunk(pcm []byte) wavChunk { return wavChunk{"data", pcm} }

func wav(riffSize int64, chunks ...wavChunk) []byte {
	var p bytes.Buffer
	p.WriteString("WAVE")
	for _, c := range chunks {
		p.WriteString(c.id)
		_ = binary.Write(&p, binary.LittleEndian, uint32(len(c.body)))
		p.Write(c.body)
		if len(c.body)%2 == 1 {
			p.WriteByte(0)
		}
	}
	if riffSize < 0 {
		riffSize = int64(p.Len())
	}
	var out bytes.Buffer
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(uint64(riffSize)))
	out.Write(p.Bytes())
	return out.Bytes()
}

func pcm(frames int) []byte {
	out := make([]byte, frames*2)
	for i := 0; i < frames; i++ {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(16000-i*7))
	}
	return out
}

func y4mHeader(tokens ...string) []byte {
	return append([]byte(join(tokens)), '\n')
}

func join(tokens []string) string {
	var b bytes.Buffer
	for i, t := range tokens {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(t)
	}
	return b.String()
}

func frameBody(w, h, seed int) []byte {
	body := make([]byte, w*h*3/2)
	for i := range body {
		body[i] = byte((i*37 + seed*11) % 256)
	}
	return body
}

func y4m(header []byte, frameHeaders [][]string, bodies [][]byte) []byte {
	var out bytes.Buffer
	out.Write(header)
	for i, fh := range frameHeaders {
		out.WriteString(join(fh))
		out.WriteByte('\n')
		out.Write(bodies[i])
	}
	return out.Bytes()
}

type evidence struct {
	id   string
	file string
	data []byte
}

func main() {
	dir := filepath.Join("defects", "evidence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	mono := wavFmtFields{1, 1, 48000, 96000, 2, 16}
	stereo := wavFmtFields{1, 2, 48000, 192000, 4, 16}

	// W1: declared RIFF extent excludes a trailing data chunk that is parsed anyway.
	w1declared := wav(-1, fmtChunk(mono), dataChunk(pcm(16)))
	w1 := append(append([]byte(nil), w1declared...), wav(-1, dataChunk(pcm(4)))[12:]...)

	// W2: byteRate contradicts sampleRate*blockAlign.
	w2fields := mono
	w2fields.byteRate = 1
	w2 := wav(-1, fmtChunk(w2fields), dataChunk(pcm(16)))

	// W3a: two fmt chunks, second flips mono to stereo.
	w3a := wav(-1, fmtChunk(mono), fmtChunk(stereo), dataChunk(pcm(16)))
	// W3b: two data chunks; the first is silently discarded.
	w3b := wav(-1, fmtChunk(mono), dataChunk(pcm(32)), dataChunk(pcm(16)))

	// W4: data precedes fmt.
	w4 := wav(-1, dataChunk(pcm(16)), fmtChunk(mono))

	// W5: one sample at 8 kHz -> descriptor DurationMS collapses to 0.
	w5fields := wavFmtFields{1, 1, 8000, 16000, 2, 16}
	w5 := wav(-1, fmtChunk(w5fields), dataChunk(pcm(1)))

	const w, h = 16, 16
	base := []string{"YUV4MPEG2", "W16", "H16", "F24:1", "Ip", "A0:0", "C420jpeg"}
	body1, body2 := frameBody(w, h, 0), frameBody(w, h, 1)

	// Y1: per-frame interlacing parameter ignored under a progressive stream.
	y1 := y4m(y4mHeader(base...), [][]string{{"FRAME", "It"}, {"FRAME"}}, [][]byte{body1, body2})
	// Y2: per-frame C422 declaration ignored while body stays C420-sized.
	y2 := y4m(y4mHeader(base...), [][]string{{"FRAME", "C422"}, {"FRAME"}}, [][]byte{body1, body2})
	// Y3: duplicate W token resolved last-one-wins (W16 then W32).
	y3tokens := append(append([]string{}, base...), "W32")
	y3 := y4m(y4mHeader(y3tokens...), [][]string{{"FRAME"}}, [][]byte{frameBody(32, 16, 0)})
	// Y4: declared A16:9 aspect ratio silently rewritten to A0:0 on export.
	y4tokens := []string{"YUV4MPEG2", "W16", "H16", "F24:1", "Ip", "A16:9", "C420jpeg"}
	y4 := y4m(y4mHeader(y4tokens...), [][]string{{"FRAME"}}, [][]byte{body1})
	// Y5: standard NTSC 30000:1001 (~29.97 fps) rejected by raw numerator check.
	y5tokens := []string{"YUV4MPEG2", "W16", "H16", "F30000:1001", "Ip", "A0:0", "C420jpeg"}
	y5 := y4m(y4mHeader(y5tokens...), [][]string{{"FRAME"}}, [][]byte{body1})

	items := []evidence{
		{"DEFECT-W1", "w1-riff-size-understatement.wav", w1},
		{"DEFECT-W2", "w2-lying-byte-rate.wav", w2},
		{"DEFECT-W3a", "w3a-duplicate-fmt.wav", w3a},
		{"DEFECT-W3b", "w3b-duplicate-data.wav", w3b},
		{"DEFECT-W4", "w4-data-before-fmt.wav", w4},
		{"DEFECT-W5", "w5-zero-duration-one-sample.wav", w5},
		{"DEFECT-Y1", "y1-frame-interlace-ignored.y4m", y1},
		{"DEFECT-Y2", "y2-frame-chroma-ignored.y4m", y2},
		{"DEFECT-Y3", "y3-duplicate-width-token.y4m", y3},
		{"DEFECT-Y4", "y4-aspect-ratio-dropped.y4m", y4},
		{"DEFECT-Y5", "y5-ntsc-fractional-fps-rejected.y4m", y5},
	}
	for _, item := range items {
		path := filepath.Join(dir, item.file)
		if err := os.WriteFile(path, item.data, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%-12s %5d bytes  %s\n", item.id, len(item.data), path)
	}
}
