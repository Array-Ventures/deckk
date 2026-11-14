package main

import (
	"context"
	"errors"
	"github.com/holman/deckk/internal/adapter"
	"io"
	"strings"
	"testing"
)

func TestProviderOverrideAndFields(t *testing.T) {
	if a := adapter.FindByName("papermark"); a == nil || a.Name() != "papermark" {
		t.Fatal("Papermark cannot be selected for a branded domain")
	}
	if adapter.FindByName("unknown") != nil {
		t.Fatal("unknown provider was accepted")
	}
	fields := fieldFlags{}
	if err := fields.Set("company=Example=Company"); err != nil || fields["company"] != "Example=Company" {
		t.Fatalf("field value changed: %v", err)
	}
	for _, invalid := range []string{"company=duplicate", "missing-value", "=empty-key", "agreement=true", "password=secret"} {
		if fields.Set(invalid) == nil {
			t.Errorf("accepted invalid field %q", invalid)
		}
	}
}

func TestReadVerificationCode(t *testing.T) {
	for _, input := range []string{"123456\n", " 123456\r\n", "123456"} {
		got, err := readVerificationCode(context.Background(), strings.NewReader(input))
		if err != nil || got != "123456" {
			t.Errorf("input %q: got %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"", "\n"} {
		if _, err := readVerificationCode(context.Background(), strings.NewReader(input)); err == nil {
			t.Errorf("empty input %q accepted", input)
		}
	}
}

func TestVerificationPromptCancellation(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readVerificationCode(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v; want cancellation", err)
	}
}
