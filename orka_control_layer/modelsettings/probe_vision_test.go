package modelsettings

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"testing"

	"github.com/orka-oss/orka_control_layer/llm"
)

func TestVisionProbeRequiresEightImageColorsInOrder(t *testing.T) {
	req, accept, err := probeRequest("candidate", "vision")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(req.Messages[0].Images[0], "data:image/png;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 32 {
		t.Fatalf("vision challenge must contain eight equal cells, got %v", img.Bounds())
	}
	colors := make([]string, 8)
	for i := range colors {
		red, green, blue, _ := img.At(i*32+16, 16).RGBA()
		switch {
		case red > 0 && green > 0:
			colors[i] = "yellow"
		case red > 0:
			colors[i] = "red"
		case green > 0:
			colors[i] = "green"
		case blue > 0:
			colors[i] = "blue"
		default:
			t.Fatal("unrecognized challenge pixel")
		}
	}
	if !accept(llm.Response{Content: strings.Join(colors, ",")}) {
		t.Fatal("actual image colors were rejected")
	}
	for _, word := range []string{"red", "green", "blue", "yellow"} {
		if accept(llm.Response{Content: word}) {
			t.Fatalf("blind single-word answer %q verified vision", word)
		}
	}
	wrong := append([]string(nil), colors...)
	wrong[0] = "red"
	if colors[0] == "red" {
		wrong[0] = "blue"
	}
	if accept(llm.Response{Content: strings.Join(wrong, ",")}) {
		t.Fatal("incorrect ordered sequence verified vision")
	}
	if strings.Contains(req.Messages[0].Content, strings.Join(colors, ",")) {
		t.Fatal("challenge answer leaked through text prompt")
	}
}
