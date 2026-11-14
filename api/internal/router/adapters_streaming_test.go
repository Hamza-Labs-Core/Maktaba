package router

import (
	"context"
	"encoding/json"
	"testing"

	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/streaming"
)

// capsClient answers GetCapabilities only.
type capsClient struct {
	grpcstreaming.Client
	caps grpcstreaming.Capabilities
}

func (c capsClient) GetCapabilities(context.Context) (grpcstreaming.Capabilities, error) {
	return c.caps, nil
}

// Epic 59 story 59.05: the streaming role's software_only reaches the capabilities players
// read, with the rest of them.
func TestStreamingAdapterCapabilities(t *testing.T) {
	a := streamingServiceAdapter{client: capsClient{caps: grpcstreaming.Capabilities{
		HWAccel: "software", SoftwareOnly: true,
		TranscodeSlots: grpcstreaming.Slots{Used: 1, Capacity: 1},
	}}}
	got, err := a.Capabilities(context.Background())
	if err != nil || !got.SoftwareOnly || got.HWAccel != "software" || got.TranscodeSlots.Capacity != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got, _ := (streamingServiceAdapter{}).Capabilities(context.Background()); got.SoftwareOnly {
		t.Fatalf("no client: %+v", got)
	}
}

// openClient records the open it is handed.
type openClient struct {
	grpcstreaming.Client
	got *grpcstreaming.OpenSessionRequest
}

func (c openClient) OpenSession(_ context.Context, req grpcstreaming.OpenSessionRequest) (grpcstreaming.OpenSessionResponse, error) {
	*c.got = req
	return grpcstreaming.OpenSessionResponse{SessionID: "s1", Mode: "direct"}, nil
}

// Story 58.02: the device's report reaches the streaming service as the API
// read it.
func TestStreamingAdapterOpenPassesDeviceCaps(t *testing.T) {
	var got grpcstreaming.OpenSessionRequest
	a := streamingServiceAdapter{client: openClient{got: &got}}
	caps := json.RawMessage(`{"containers":["mkv"],"hdr":["hdr10"]}`)
	if _, err := a.Open(context.Background(), streaming.OpenSessionRequest{
		VideoID: "v1", ClientProfile: "tizen", DeviceCaps: caps,
	}); err != nil {
		t.Fatal(err)
	}
	if string(got.DeviceCaps) != string(caps) || got.ClientProfile != "tizen" || got.VideoID != "v1" {
		t.Fatalf("passed on %+v", got)
	}
}
