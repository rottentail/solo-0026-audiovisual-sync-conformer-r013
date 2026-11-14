package media

// Byte-level WAV and Y4M corpus builders used by the regression table tests and
// the Go fuzz targets. The production encoders (EncodePCM16 / EncodeY4M) only
// emit well-formed streams, so the fuzzing and boundary work needs independent
// builders that can express malformed and internally inconsistent files.

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// wavFmtFields mirrors the fields carried in a WAV "fmt " chunk body.
type wavFmtFields struct {
	audioFormat   uint16
	channels      uint16
	sampleRate    uint32
	byteRate      uint32
	blockAlign    uint16
	bitsPerSample uint16
}

func defaultWAVFmt() wavFmtFields {
	return wavFmtFields{
		audioFormat:   1,
		channels:      1,
		sampleRate:    48000,
		byteRate:      48000 * 2,
		blockAlign:    2,
		bitsPerSample: 16,
	}
}

// wavChunk is one RIFF chunk with an explicit id, body and padding policy.
type wavChunk struct {
	id      string
	body    []byte
	padByte bool // emit the odd-size padding byte even when len(body) is even
}

func buildFmtChunk(f wavFmtFields, bodySize uint32) wavChunk {
	var body bytes.Buffer
	_ = binary.Write(&body, binary.LittleEndian, f.audioFormat)
	_ = binary.Write(&body, binary.LittleEndian, f.channels)
	_ = binary.Write(&body, binary.LittleEndian, f.sampleRate)
	_ = binary.Write(&body, binary.LittleEndian, f.byteRate)
	_ = binary.Write(&body, binary.LittleEndian, f.blockAlign)
	_ = binary.Write(&body, binary.LittleEndian, f.bitsPerSample)
	raw := body.Bytes()
	if uint32(len(raw)) > bodySize {
		// Deliberately truncated fmt body: keep only the declared prefix so
		// the decoder's "format chunk is too short" boundary is exercisable.
		raw = raw[:bodySize]
		return wavChunk{id: "fmt ", body: append([]byte(nil), raw...)}
	}
	return wavChunk{id: "fmt ", body: append(raw, make([]byte, int(bodySize)-len(raw))...)}
}

func buildDataChunk(pcm []byte) wavChunk {
	return wavChunk{id: "data", body: append([]byte(nil), pcm...)}
}

func buildUnknownChunk(id string, body []byte) wavChunk {
	return wavChunk{id: id, body: append([]byte(nil), body...)}
}

// buildWAV assembles a RIFF/WAVE container. riffSize controls the declared
// RIFF size independently of the actual emitted bytes so declaration/body
// mismatches can be expressed. A negative riffSize means "compute the exact
// size of the chunk payload".
func buildWAV(riffSize int64, chunks ...wavChunk) []byte {
	var payload bytes.Buffer
	payload.WriteString("WAVE")
	for _, chunk := range chunks {
		payload.WriteString(chunk.id)
		_ = binary.Write(&payload, binary.LittleEndian, uint32(len(chunk.body)))
		payload.Write(chunk.body)
		if len(chunk.body)%2 == 1 || chunk.padByte {
			payload.WriteByte(0)
		}
	}
	declared := riffSize
	if riffSize < 0 {
		declared = int64(payload.Len())
	}
	var out bytes.Buffer
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(uint64(declared)))
	out.Write(payload.Bytes())
	return out.Bytes()
}

// pcmSine fills frames of 16-bit little-endian PCM with a deterministic
// non-zero waveform so signal analysis has measurable amplitude.
func pcmSine(channels, frames, sampleRate int) []byte {
	total := channels * frames
	var out bytes.Buffer
	for i := 0; i < total; i++ {
		amplitude := 20000
		if i%7 == 0 {
			amplitude = 4000 // keep a varied but bounded signal
		}
		_ = binary.Write(&out, binary.LittleEndian, int16(amplitude))
	}
	_ = sampleRate
	return out.Bytes()
}

// y4mStream describes a hand-built Y4M file. Header tokens are emitted
// verbatim so malformed or duplicated parameters can be expressed.
type y4mStream struct {
	headerTokens []string
	frameHeaders [][]string
	frameBodies  [][]byte // one body per frame; nil body uses frameBodyBytes
	newlineCRLF  bool
}

func defaultY4MHeader(width, height, fpsNum, fpsDen int, extraTokens ...string) []string {
	tokens := []string{
		"YUV4MPEG2",
		fmt.Sprintf("W%d", width),
		fmt.Sprintf("H%d", height),
		fmt.Sprintf("F%d:%d", fpsNum, fpsDen),
		"Ip",
		"A0:0",
		"C420jpeg",
	}
	return append(tokens, extraTokens...)
}

func c420FrameSize(width, height int) int {
	return width*height + 2*(width/2)*(height/2)
}

func deterministicFrame(width, height, seed int) []byte {
	body := make([]byte, c420FrameSize(width, height))
	for i := range body {
		body[i] = byte((i*37 + seed*11) % 256)
	}
	return body
}

// buildY4M serializes a y4mStream. Frame bodies are taken from frameBodies
// when provided, otherwise a correctly sized deterministic body is generated.
func buildY4M(s y4mStream, width, height int) []byte {
	nl := []byte("\n")
	if s.newlineCRLF {
		nl = []byte("\r\n")
	}
	var out bytes.Buffer
	out.WriteString(joinTokens(s.headerTokens))
	out.Write(nl)
	for i := 0; i < len(s.frameHeaders); i++ {
		out.WriteString(joinTokens(s.frameHeaders[i]))
		out.Write(nl)
		if i < len(s.frameBodies) && s.frameBodies[i] != nil {
			out.Write(s.frameBodies[i])
		} else {
			out.Write(deterministicFrame(width, height, i))
		}
	}
	return out.Bytes()
}

func joinTokens(tokens []string) string {
	var b bytes.Buffer
	for i, token := range tokens {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(token)
	}
	return b.String()
}

func validY4MStream(width, height, fpsNum, fpsDen, frames int) y4mStream {
	stream := y4mStream{
		headerTokens: defaultY4MHeader(width, height, fpsNum, fpsDen),
	}
	for i := 0; i < frames; i++ {
		stream.frameHeaders = append(stream.frameHeaders, []string{"FRAME"})
	}
	return stream
}

// serializeChunk emits the on-disk encoding of one RIFF chunk (header, body
// and odd-size padding), useful for appending raw bytes outside a container.
func serializeChunk(chunk wavChunk) []byte {
	var b bytes.Buffer
	b.WriteString(chunk.id)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(chunk.body)))
	b.Write(chunk.body)
	if len(chunk.body)%2 == 1 {
		b.WriteByte(0)
	}
	return b.Bytes()
}
