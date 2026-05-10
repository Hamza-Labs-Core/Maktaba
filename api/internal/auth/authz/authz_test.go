package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

type mockLib map[string]string // resourceID → libraryID

func (m mockLib) LibraryOf(_ context.Context, _, id string) (string, error) {
	if v, ok := m[id]; ok {
		return v, nil
	}
	return "", nil
}

type mockOwner map[string]string

func (m mockOwner) OwnerOf(_ context.Context, _, id string) (string, error) {
	return m[id], nil
}

func ctxWith(p *principal.Principal) context.Context {
	return principal.WithPrincipal(context.Background(), p)
}

func TestAuthz_Unauthenticated(t *testing.T) {
	v := &V1{}
	if err := v.Can(context.Background(), "video.read", "x"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("anon: got %v, want ErrUnauthenticated", err)
	}
}

func TestAuthz_AdminBypass(t *testing.T) {
	v := &V1{}
	ctx := ctxWith(&principal.Principal{UserID: "u", IsAdmin: true})
	if err := v.Can(ctx, "library.write", "lib-x"); err != nil {
		t.Errorf("admin should bypass: %v", err)
	}
	if err := v.Can(ctx, "video.read", "vid-x"); err != nil {
		t.Errorf("admin should bypass: %v", err)
	}
}

func TestAuthz_LibraryWriteForbidsNonAdmin(t *testing.T) {
	v := &V1{}
	ctx := ctxWith(&principal.Principal{UserID: "u", Libraries: []string{"lib-a"}})
	if err := v.Can(ctx, "library.write", "lib-a"); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-admin POST /libraries: got %v, want ErrForbidden", err)
	}
}

func TestAuthz_VideoRead_AllowedWhenLibraryIsInLibSlice(t *testing.T) {
	v := &V1{Lib: mockLib{"vid-1": "lib-a"}}
	ctx := ctxWith(&principal.Principal{UserID: "u", Libraries: []string{"lib-a"}})
	if err := v.Can(ctx, "video.read", "vid-1"); err != nil {
		t.Errorf("read with matching lib: %v", err)
	}
}

func TestAuthz_VideoRead_DeniedWhenLibraryMissing(t *testing.T) {
	v := &V1{Lib: mockLib{"vid-1": "lib-a"}}
	ctx := ctxWith(&principal.Principal{UserID: "u", Libraries: []string{"lib-b"}})
	if err := v.Can(ctx, "video.read", "vid-1"); !errors.Is(err, ErrForbidden) {
		t.Errorf("read without lib: got %v, want ErrForbidden", err)
	}
}

func TestAuthz_SingleUserMode_AnyAuthenticatedReads(t *testing.T) {
	v := &V1{SingleUserMode: true} // no Lib resolver wired
	ctx := ctxWith(&principal.Principal{UserID: "u"})
	if err := v.Can(ctx, "video.read", "vid-1"); err != nil {
		t.Errorf("single-user-mode read: %v", err)
	}
}

func TestAuthz_PlaybackState_OwnerCanWrite(t *testing.T) {
	v := &V1{Owner: mockOwner{"ps-1": "u-self"}}
	ctx := ctxWith(&principal.Principal{UserID: "u-self"})
	if err := v.Can(ctx, "playback_state.write", "ps-1"); err != nil {
		t.Errorf("owner write: %v", err)
	}
	other := ctxWith(&principal.Principal{UserID: "u-other"})
	if err := v.Can(other, "playback_state.write", "ps-1"); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-owner write: got %v, want ErrForbidden", err)
	}
}

func TestAuthz_VideoWrite_AdminOnly(t *testing.T) {
	v := &V1{}
	ctx := ctxWith(&principal.Principal{UserID: "u", Libraries: []string{"lib-a"}})
	if err := v.Can(ctx, "video.write", "vid-1"); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-admin video write: got %v, want ErrForbidden", err)
	}
}

func TestSplitAction(t *testing.T) {
	cases := map[string][2]string{
		"video.read":          {"video", "read"},
		"library.write":       {"library", "write"},
		"playback_state.read": {"playback_state", "read"},
		"global":              {"global", ""},
	}
	for in, want := range cases {
		r, s := splitAction(in)
		if r != want[0] || s != want[1] {
			t.Errorf("splitAction(%q) = (%q,%q), want (%q,%q)", in, r, s, want[0], want[1])
		}
	}
}

func TestStatic(t *testing.T) {
	if err := (Static{Allow: true}).Can(context.Background(), "x.read", "y"); err != nil {
		t.Errorf("Static{Allow:true}: %v", err)
	}
	if err := (Static{Allow: false}).Can(context.Background(), "x.read", "y"); !errors.Is(err, ErrForbidden) {
		t.Errorf("Static{Allow:false}: got %v, want ErrForbidden", err)
	}
}
