package paginate

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	c := Cursor{
		Updated: time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC),
		ID:      "01902f00-7c80-77c8-9c00-000000000000",
	}
	enc := Encode(c)
	if len(enc) >= 128 {
		t.Fatalf("encoded length = %d, want < 128", len(enc))
	}
	dec, err := Decode(enc)
	if err != nil {
		t.Fatalf("decode: %+v", err)
	}
	if !dec.Updated.Equal(c.Updated) || dec.ID != c.ID {
		t.Fatalf("round trip mismatch: in=%+v out=%+v", c, dec)
	}
	if dec.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", dec.Version, CurrentVersion)
	}
}

func TestDecodeRejectsBase64(t *testing.T) {
	_, err := Decode("%%%")
	if err == nil {
		t.Fatal("expected error for bad base64")
	}
	if err.Type != httperror.TypeInvalidCursor {
		t.Fatalf("type = %s, want %s", err.Type, httperror.TypeInvalidCursor)
	}
}

func TestDecodeRejectsMalformedJSON(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString([]byte("not json"))
	_, err := Decode(enc)
	if err == nil {
		t.Fatal("expected error for non-JSON body")
	}
	if err.Type != httperror.TypeInvalidCursor {
		t.Fatalf("type = %s, want %s", err.Type, httperror.TypeInvalidCursor)
	}
}

func TestDecodeFutureVersion(t *testing.T) {
	raw, _ := json.Marshal(Cursor{
		Updated: time.Now(), ID: "x", Version: CurrentVersion + 1,
	})
	enc := base64.RawURLEncoding.EncodeToString(raw)
	_, err := Decode(enc)
	if err == nil {
		t.Fatal("expected error for future version")
	}
	if err.Type != httperror.TypeCursorUnsupported {
		t.Fatalf("type = %s, want %s", err.Type, httperror.TypeCursorUnsupported)
	}
}

func TestDecodeEmptyReturnsZero(t *testing.T) {
	c, err := Decode("")
	if err != nil {
		t.Fatalf("expected no error: %+v", err)
	}
	if c != (Cursor{}) {
		t.Fatalf("expected zero cursor, got %+v", c)
	}
}
