package pgstore_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

func TestOpenSucceedsWhilePostgresIsDownAndPingReportsIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := pgstore.Open(ctx, "postgres://iam:secret-pw@127.0.0.1:1/iam?connect_timeout=1", pgstore.Options{MaxConns: 2, RevocationKeep: testKeep})
	if err != nil {
		t.Fatalf("Open = %v; want a lazy pool that opens without postgres", err)
	}
	defer s.Close()
	err = s.Ping(ctx)
	if err == nil {
		t.Fatal("Ping succeeded without postgres")
	}
	if strings.Contains(err.Error(), "secret-pw") {
		t.Fatalf("Ping error leaks the password: %v", err)
	}
}

func TestOpenRejectsAMalformedURI(t *testing.T) {
	if _, err := pgstore.Open(context.Background(), "postgres://iam@host:notaport/iam", pgstore.Options{MaxConns: 1, RevocationKeep: testKeep}); err == nil {
		t.Fatal("Open accepted a malformed uri")
	}
}
