package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/deco-sites/decofile-operator/internal/api"
	"github.com/deco-sites/decofile-operator/internal/controller"
)

var knownControllers = []string{
	controller.TenantControllerName,
	controller.DecofileControllerName,
	controller.DecoControllerName,
	controller.DecoRedirectControllerName,
	api.ControllerName,
}

// parseControllers parses a comma-separated list of controller names.
// "*" enables all known controllers.
// Returns an error if any name is not in knownControllers.
func parseControllers(input string) (func(string) bool, error) {
	if strings.TrimSpace(input) == "*" {
		return func(string) bool { return true }, nil
	}
	parts := strings.Split(input, ",")
	set := make(map[string]bool, len(parts))
	for _, name := range parts {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("controller name must not be empty; valid values: %s",
				strings.Join(knownControllers, ", "))
		}
		if !slices.Contains(knownControllers, name) {
			return nil, fmt.Errorf("unknown controller %q; valid values: %s",
				name, strings.Join(knownControllers, ", "))
		}
		set[name] = true
	}
	return func(name string) bool { return set[name] }, nil
}
