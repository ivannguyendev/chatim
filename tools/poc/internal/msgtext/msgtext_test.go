package msgtext

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyntheticHasTwentyToFiftyNineWords(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for range 1000 {
		if n := len(strings.Fields(Synthetic(rng))); n < 20 || n > 59 {
			t.Fatalf("Synthetic produced %d words", n)
		}
	}
}

func TestLoadSkipsBlankLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "texts.txt")
	if err := os.WriteFile(path, []byte("  xin chào \n\n\tđơn hàng DH1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	texts, err := Load(path)
	if err != nil || len(texts) != 2 || texts[0] != "xin chào" || texts[1] != "đơn hàng DH1" {
		t.Fatalf("Load = %q, %v", texts, err)
	}
	if texts, err := Load(""); err != nil || texts != nil {
		t.Fatalf(`Load("") = %q, %v; want nil, nil`, texts, err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("Load of a missing file succeeded")
	}
}

func TestPickUsesRealTextsWhenGiven(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	if got := Pick([]string{"only"}, rng); got != "only" {
		t.Fatalf("Pick = %q", got)
	}
	if got := Pick(nil, rng); got == "" {
		t.Fatal("Pick without texts returned empty text")
	}
	if s := Sender(rng); !strings.HasPrefix(s, "user-") || len(s) != len("user-a") {
		t.Fatalf("Sender = %q", s)
	}
}
