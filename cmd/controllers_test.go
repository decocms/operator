package main

import (
	"strings"
	"testing"

	"github.com/deco-sites/decofile-operator/internal/api"
	"github.com/deco-sites/decofile-operator/internal/controller"
)

func TestParseControllers_Star(t *testing.T) {
	enabled, err := parseControllers("*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, name := range knownControllers {
		if !enabled(name) {
			t.Errorf("expected %q to be enabled with *, but it wasn't", name)
		}
	}
}

func TestParseControllers_Subset(t *testing.T) {
	enabled, err := parseControllers("decoredirect,operator-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !enabled(controller.DecoRedirectControllerName) {
		t.Error("expected decoredirect to be enabled")
	}
	if !enabled(api.ControllerName) {
		t.Error("expected operator-api to be enabled")
	}
	if enabled(controller.DecofileControllerName) {
		t.Error("expected decofile to be disabled")
	}
	if enabled(controller.TenantControllerName) {
		t.Error("expected namespace to be disabled")
	}
	if enabled(controller.DecoControllerName) {
		t.Error("expected deco to be disabled")
	}
}

func TestParseControllers_UnknownName(t *testing.T) {
	_, err := parseControllers("decoredirect,xpto")
	if err == nil {
		t.Fatal("expected error for unknown controller name, got nil")
	}
	if !strings.Contains(err.Error(), "xpto") {
		t.Errorf("expected error to mention 'xpto', got: %v", err)
	}
}

func TestParseControllers_WhitespaceHandled(t *testing.T) {
	enabled, err := parseControllers("decoredirect, operator-api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !enabled(controller.DecoRedirectControllerName) {
		t.Error("expected decoredirect to be enabled")
	}
	if !enabled(api.ControllerName) {
		t.Error("expected operator-api to be enabled")
	}
}
