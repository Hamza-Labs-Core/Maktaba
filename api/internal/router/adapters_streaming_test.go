package router

import (
	"context"
	"testing"

	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
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
