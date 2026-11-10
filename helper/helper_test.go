package helper

import (
	"errors"
	"testing"
)

func TestIsSimpleIncludesError(t *testing.T) {
	if !IsSimple(errors.New("x")) {
		t.Fatal("un error debe imprimirse con su mensaje, no como JSON")
	}
}
