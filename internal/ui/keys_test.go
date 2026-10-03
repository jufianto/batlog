package ui

import "testing"

func TestKeyNamesMatchBubbleTea(t *testing.T) {
	for name := range namedKeys {
		if got := keyPress(name).String(); got != name {
			t.Errorf("%s prints as %q", name, got)
		}
	}
	for _, k := range []string{"q", "1", "[", "]", "G", "?"} {
		if got := keyPress(k).String(); got != k {
			t.Errorf("%s prints as %q", k, got)
		}
	}
}
