package ali

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestNormalizeWan27I2VInputBuildsMediaAndClearsLegacyFields(t *testing.T) {
	request := relaycommon.TaskSubmitReq{
		Model:  "wan2.7-i2v",
		Images: []string{"https://example.com/first.png", "https://example.com/last.png"},
	}
	converted := &AliVideoRequest{
		Model: "wan2.7-i2v",
		Input: AliVideoInput{
			ImgURL:   request.Images[0],
			AudioURL: "https://example.com/audio.mp3",
		},
	}
	if err := normalizeWan27I2VInput(converted, request); err != nil {
		t.Fatal(err)
	}
	if len(converted.Input.Media) != 3 {
		t.Fatalf("expected 3 media entries, got %#v", converted.Input.Media)
	}
	if converted.Input.Media[0].Type != "first_frame" || converted.Input.Media[1].Type != "last_frame" || converted.Input.Media[2].Type != "driving_audio" {
		t.Fatalf("unexpected media mapping: %#v", converted.Input.Media)
	}
	if converted.Input.ImgURL != "" || converted.Input.AudioURL != "" {
		t.Fatalf("legacy fields were not cleared: %#v", converted.Input)
	}
}

func TestNormalizeWan27I2VInputRequiresMedia(t *testing.T) {
	converted := &AliVideoRequest{Model: "wan2.7-i2v"}
	if err := normalizeWan27I2VInput(converted, relaycommon.TaskSubmitReq{}); err == nil {
		t.Fatal("expected missing media error")
	}
}

func TestNormalizeWan27DoesNotChangeOlderModels(t *testing.T) {
	converted := &AliVideoRequest{
		Model: "wan2.6-i2v",
		Input: AliVideoInput{ImgURL: "https://example.com/first.png"},
	}
	if err := normalizeWan27I2VInput(converted, relaycommon.TaskSubmitReq{}); err != nil {
		t.Fatal(err)
	}
	if converted.Input.ImgURL == "" || len(converted.Input.Media) != 0 {
		t.Fatalf("older model changed: %#v", converted.Input)
	}
}
