package domain_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestParseKindMapsConfigNamesToKinds(t *testing.T) {
	got, err := domain.ParseKind("text")
	if err != nil || got != domain.KindText {
		t.Fatalf(`ParseKind("text") = %d, %v; want KindText`, got, err)
	}
	for _, name := range []string{"", "system", "Text"} {
		if got, err := domain.ParseKind(name); !errors.Is(err, apperr.ErrInvalidArgument) || got != 0 {
			t.Fatalf("ParseKind(%q) = %d, %v; want 0 and ErrInvalidArgument", name, got, err)
		}
	}
}
