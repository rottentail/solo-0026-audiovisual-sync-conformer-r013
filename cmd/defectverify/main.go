// Command defectverify replays every minimal evidence file under
// defects/evidence against the real production decoders and prints the
// observed verdict next to the expected (spec) verdict. It performs no
// assertion weakening: a defect is "open" exactly while observed != expected.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rottentail/solo-0026-audiovisual-sync-conformer/internal/media"
)

type expectation struct {
	file        string
	kind        string // wav | y4m
	specVerdict string // accept | reject
	note        string
}

func main() {
	root := "defects/evidence"
	cases := []expectation{
		{"w1-riff-size-understatement.wav", "wav", "reject", "RIFF size understates payload; hidden data chunk parsed"},
		{"w2-lying-byte-rate.wav", "wav", "reject", "fmt byteRate contradicts sampleRate*blockAlign"},
		{"w3a-duplicate-fmt.wav", "wav", "reject", "second conflicting fmt chunk silently overrides first"},
		{"w3b-duplicate-data.wav", "wav", "reject", "duplicate data chunks; earlier samples discarded"},
		{"w4-data-before-fmt.wav", "wav", "reject", "data chunk appears before fmt chunk"},
		{"w5-zero-duration-one-sample.wav", "wav", "accept", "accepted but descriptor DurationMS collapses to 0"},
		{"y1-frame-interlace-ignored.y4m", "y4m", "reject", "FRAME It ignored under progressive-only policy"},
		{"y2-frame-chroma-ignored.y4m", "y4m", "reject", "FRAME C422 ignored while body is C420-sized"},
		{"y3-duplicate-width-token.y4m", "y4m", "reject", "duplicate W header token resolved last-one-wins"},
		{"y4-aspect-ratio-dropped.y4m", "y4m", "accept", "accepted but export rewrites A16:9 to A0:0"},
		{"y5-ntsc-fractional-fps-rejected.y4m", "y4m", "accept", "standard NTSC 30000:1001 falsely rejected"},
	}
	failures := 0
	for _, tc := range cases {
		data, err := os.ReadFile(filepath.Join(root, tc.file))
		if err != nil {
			fmt.Printf("ERROR  %s: %v\n", tc.file, err)
			failures++
			continue
		}
		var observed string
		var detail string
		switch tc.kind {
		case "wav":
			clip, err := media.InspectAudio(data)
			if err != nil {
				observed, detail = "reject", err.Error()
			} else {
				observed = "accept"
				detail = fmt.Sprintf("frames=%d duration_ms=%d channels=%d rate=%d",
					clip.Info.FrameCount, clip.Info.DurationMS, clip.Info.Channels, clip.Info.SampleRate)
				if tc.file == "w5-zero-duration-one-sample.wav" && clip.Info.DurationMS == 0 && clip.Info.FrameCount > 0 {
					detail += " | MEANING-DEFECT: non-empty track reports zero duration"
				}
			}
		case "y4m":
			clip, err := media.InspectVideo(data)
			if err != nil {
				observed, detail = "reject", err.Error()
			} else {
				observed = "accept"
				detail = fmt.Sprintf("frames=%d %dx%d fps=%d:%d chroma=%s",
					clip.Info.FrameCount, clip.Info.Width, clip.Info.Height,
					clip.Info.FPSNum, clip.Info.FPSDen, clip.Info.Chroma)
				if tc.file == "y4-aspect-ratio-dropped.y4m" {
					if exported, _, err := media.NormalizeY4M(data); err == nil {
						header := string(exported[:bytes.IndexByte(exported, '\n')])
						if !bytes.Contains(exported[:bytes.IndexByte(exported, '\n')], []byte("A16:9")) {
							detail += " | MEANING-DEFECT: export header=" + header
						}
					}
				}
			}
		}
		status := "ok"
		if observed != tc.specVerdict {
			status = "OPEN-DEFECT"
			failures++
		} else if bytes.Contains([]byte(detail), []byte("MEANING-DEFECT")) {
			status = "OPEN-DEFECT"
			failures++
		}
		fmt.Printf("%-12s spec=%-6s observed=%-6s %-40s %s\n", status, tc.specVerdict, observed, tc.file, tc.note)
		fmt.Printf("               detail: %s\n", detail)
	}
	if failures > 0 {
		fmt.Printf("\n%d defect(s) still open against the production decoders\n", failures)
		os.Exit(1)
	}
	fmt.Println("\nall evidence files now match the spec verdict")
}
