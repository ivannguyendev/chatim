package notify_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func equal(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func truth(t *testing.T, ok bool, msg ...any) {
	t.Helper()
	if !ok {
		t.Fatalf("condition failed %v", msg)
	}
}

func errIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error %v is not %v", err, target)
	}
}

func errContains(t *testing.T, err error, part string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), part) {
		t.Fatalf("error %v does not contain %q", err, part)
	}
}
