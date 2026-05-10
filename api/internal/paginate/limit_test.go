package paginate

import (
	"net/url"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

func TestLimitDefault(t *testing.T) {
	q := url.Values{}
	n, err := ParseLimit(q)
	if err != nil {
		t.Fatalf("unexpected error: %+v", err)
	}
	if n != DefaultLimit {
		t.Fatalf("limit = %d, want %d", n, DefaultLimit)
	}
}

func TestLimitValid(t *testing.T) {
	q := url.Values{"limit": []string{"20"}}
	n, err := ParseLimit(q)
	if err != nil || n != 20 {
		t.Fatalf("got (%d, %+v), want (20, nil)", n, err)
	}
}

func TestLimitTooLow(t *testing.T) {
	q := url.Values{"limit": []string{"0"}}
	_, err := ParseLimit(q)
	if err == nil || err.Type != httperror.TypeInvalidQueryParam {
		t.Fatalf("expected invalid-query-parameter, got %+v", err)
	}
}

func TestLimitTooHigh(t *testing.T) {
	q := url.Values{"limit": []string{"201"}}
	_, err := ParseLimit(q)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Status != 400 {
		t.Fatalf("status = %d, want 400", err.Status)
	}
}

func TestLimitNonNumeric(t *testing.T) {
	q := url.Values{"limit": []string{"abc"}}
	_, err := ParseLimit(q)
	if err == nil {
		t.Fatal("expected error")
	}
}
