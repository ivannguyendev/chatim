package api_test

import (
	"context"
	"crypto/rand"
	"fmt"
	mrand "math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/keyring"
	"github.com/ivannguyendev/chatim/apps/iam/internal/session"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/iamsync"
	iamv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/iam/v1/iamv1connect"
	"github.com/ivannguyendev/chatim/pkg/revocation"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

const (
	benchDirectoryUsers = 100000
	benchCallers        = 256
	benchRevoked        = 1000
	benchIDsPerCall     = 50
)

type benchKeys struct{ keys []store.SigningKey }

func (k benchKeys) SigningKeys(context.Context) ([]store.SigningKey, error) { return k.keys, nil }

type readyDirectory struct{ *directory.Cache }

func (readyDirectory) Ready() bool { return true }

func benchUserID(n int) string { return fmt.Sprintf("u%06d", n) }

func benchRing(b *testing.B) *keyring.Ring {
	b.Helper()
	kek := make([]byte, signing.KEKLength)
	_, _ = rand.Read(kek)
	sealer, err := signing.NewSealer(kek, rand.Reader)
	if err != nil {
		b.Fatal(err)
	}
	key, err := signing.Generate(rand.Reader)
	if err != nil {
		b.Fatal(err)
	}
	sealed, err := sealer.Seal(key)
	if err != nil {
		b.Fatal(err)
	}
	src := benchKeys{keys: []store.SigningKey{{ID: key.ID, State: store.KeyActive, PublicKey: key.PublicKey().Key, PrivateSealed: sealed, CreatedAt: time.Now()}}}
	ring := keyring.New(src, sealer, keyring.Config{
		Token:  signing.TokenConfig{Issuer: "iss", Audience: "aud", TTL: 15 * time.Minute},
		Leeway: 30 * time.Second,
	})
	if err := ring.Reload(context.Background()); err != nil {
		b.Fatalf("Reload: %v", err)
	}
	return ring
}

func benchTokens(b *testing.B, ring *keyring.Ring) []string {
	b.Helper()
	signer, err := ring.Current()
	if err != nil {
		b.Fatal(err)
	}
	tokens := make([]string, benchCallers)
	for i := range tokens {
		sid, err := session.NewID(rand.Reader)
		if err != nil {
			b.Fatal(err)
		}
		tokens[i], _, err = signer.Sign(signing.Claims{Tenant: "shop", Subject: benchUserID(i + 1), Session: sid, IssuedAt: time.Now()})
		if err != nil {
			b.Fatal(err)
		}
	}
	return tokens
}

func benchRevocations(b *testing.B) *revocation.List {
	b.Helper()
	list, err := iamsync.NewList(30 * time.Second)
	if err != nil {
		b.Fatal(err)
	}
	now := time.Now()
	sessions := make([]revocation.Session, benchRevoked)
	for i := range sessions {
		sid, err := session.NewID(rand.Reader)
		if err != nil {
			b.Fatal(err)
		}
		sessions[i] = revocation.Session{ID: sid, Tenant: "shop", ExpiresAt: now.Add(15 * time.Minute)}
	}
	list.Merge(revocation.Snapshot{Sessions: sessions}, now)
	return list
}

func benchDirectory() readyDirectory {
	entries := make([]store.DirectoryEntry, benchDirectoryUsers)
	for i := range entries {
		id := benchUserID(i + 1)
		entries[i] = store.DirectoryEntry{TenantID: "shop", ID: id, DisplayName: fmt.Sprintf("User %d", i+1), AvatarURL: "https://cdn.example.com/avatars/" + id + ".png", State: store.StateActive, Ver: 1}
	}
	cache := directory.NewCache()
	cache.ApplyEntries(entries)
	return readyDirectory{Cache: cache}
}

func benchServer(b *testing.B) (iamv1connect.DirectoryServiceClient, []string) {
	b.Helper()
	ring := benchRing(b)
	gate, err := userlimit.NewGate(10000)
	if err != nil {
		b.Fatal(err)
	}
	users, err := userlimit.NewUsers(userlimit.UserConfig{Rate: 1 << 30, Burst: 1 << 30, Inflight: 1 << 20, Capacity: 100000})
	if err != nil {
		b.Fatal(err)
	}
	handler, err := api.New(api.Options{
		Auth: &fakeAuth{}, Proofs: fakeProofs{}, Callers: ring, Keys: ring, BodyMax: 16 << 10,
		Directory: benchDirectory(), Revocations: benchRevocations(b), Claims: ring, DirectoryGate: gate, DirectoryUsers: users,
	})
	if err != nil {
		b.Fatalf("api.New: %v", err)
	}
	srv := httptest.NewServer(handler)
	transport := &http.Transport{MaxIdleConns: 1024, MaxIdleConnsPerHost: 1024}
	b.Cleanup(func() {
		transport.CloseIdleConnections()
		srv.Close()
	})
	client := iamv1connect.NewDirectoryServiceClient(&http.Client{Transport: transport}, srv.URL, connect.WithProtoJSON())
	return client, benchTokens(b, ring)
}

func processCPU() time.Duration {
	var u syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &u)
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}

func BenchmarkGetUsersHTTP50(b *testing.B) {
	client, tokens := benchServer(b)
	batches := make([][]string, 1024)
	for i := range batches {
		batches[i] = make([]string, benchIDsPerCall)
		for j := range batches[i] {
			batches[i][j] = benchUserID(1 + mrand.IntN(benchDirectoryUsers))
		}
	}
	var caller atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	cpuBefore := processCPU()
	b.RunParallel(func(pb *testing.PB) {
		n := int(caller.Add(1))
		token := tokens[n%len(tokens)]
		for pb.Next() {
			n++
			resp, err := client.GetUsers(context.Background(), withToken(token, &iamv1.GetUsersRequest{UserIds: batches[n%len(batches)]}))
			if err != nil {
				b.Errorf("GetUsers: %v", err)
				return
			}
			if len(resp.Msg.GetUsers()) == 0 {
				b.Errorf("GetUsers found no users")
				return
			}
		}
	})
	cpu := processCPU() - cpuBefore
	b.ReportMetric(float64(cpu.Nanoseconds())/float64(b.N), "cpu-ns/op")
}
