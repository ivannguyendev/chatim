package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/auth"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestListSessionsShowsOwnSessionsAndMarksTheCurrentOne(t *testing.T) {
	h, phone := loggedIn(t)
	laptop, err := h.login("lan", lanPassword, "jkt-laptop")
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	h.addAccount(t, "b1aaaaaaaaaaaaaaaaaaaaaaaa", "binh", lanPassword, false)
	if _, err := h.login("binh", lanPassword, "jkt-binh"); err != nil {
		t.Fatalf("binh login: %v", err)
	}
	caller := auth.Caller{Tenant: "shop", Account: "a1aaaaaaaaaaaaaaaaaaaaaaaa", Session: phone.SessionID}
	views, err := h.svc.ListSessions(context.Background(), caller)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	current := 0
	for _, v := range views {
		if v.ID != phone.SessionID && v.ID != laptop.SessionID {
			t.Fatalf("listed a session of another account: %s", v.ID)
		}
		if v.Current {
			current++
			if v.ID != phone.SessionID {
				t.Fatal("current flag on the wrong session")
			}
		}
	}
	if len(views) != 2 || current != 1 {
		t.Fatalf("views = %d, current = %d; want 2 and 1", len(views), current)
	}
}

func TestRevokeSessionOnlyTouchesOwnSessions(t *testing.T) {
	h, phone := loggedIn(t)
	laptop, _ := h.login("lan", lanPassword, "jkt-laptop")
	h.addAccount(t, "b1aaaaaaaaaaaaaaaaaaaaaaaa", "binh", lanPassword, false)
	binh, _ := h.login("binh", lanPassword, "jkt-binh")
	ctx := context.Background()
	caller := auth.Caller{Tenant: "shop", Account: "a1aaaaaaaaaaaaaaaaaaaaaaaa", Session: phone.SessionID}
	if err := h.svc.RevokeSession(ctx, caller, binh.SessionID); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("revoke another account's session = %v, want ErrNotFound", err)
	}
	if err := h.svc.RevokeSession(ctx, caller, laptop.SessionID); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if h.store.sessions[laptop.SessionID].State != store.StateDisabled || h.store.sessions[binh.SessionID].State != store.StateActive {
		t.Fatal("wrong sessions revoked")
	}
}

func TestCallerWithARevokedSessionIsRejected(t *testing.T) {
	h, phone := loggedIn(t)
	ctx := context.Background()
	if err := h.svc.Revoke(ctx, auth.RevokeRequest{Tenant: "shop", Token: phone.RefreshToken}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	caller := auth.Caller{Tenant: "shop", Account: "a1aaaaaaaaaaaaaaaaaaaaaaaa", Session: phone.SessionID}
	if _, err := h.svc.ListSessions(ctx, caller); !errors.Is(err, apperr.ErrUnauthenticated) {
		t.Fatalf("ListSessions with a revoked session = %v, want ErrUnauthenticated", err)
	}
	forged := auth.Caller{Tenant: "shop", Account: "b1aaaaaaaaaaaaaaaaaaaaaaaa", Session: phone.SessionID}
	if _, err := h.svc.ListSessions(ctx, forged); !errors.Is(err, apperr.ErrUnauthenticated) {
		t.Fatalf("ListSessions with a session of another account = %v, want ErrUnauthenticated", err)
	}
}
