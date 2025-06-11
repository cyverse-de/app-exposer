package batch

import "testing"

func TestEscapePath(t *testing.T) {
	pathList := map[string]string{
		"/iplant/home/test":   "/iplant/home/test",
		" /iplant/home/test":  "/iplant/home/test",
		"/iplant/home/test ":  "/iplant/home/test",
		" /iplant/home/test ": "/iplant/home/test",
		"/iplant/home/te$t":   "/iplant/home/'te$t'",
	}

	for input, expected := range pathList {
		output := escapePath(input)
		if output != expected {
			t.Errorf("expected '%s', got '%s'", expected, output)
		}
	}
}

func TestSluggy(t *testing.T) {
	testStrings := map[string]string{
		"foo_bar-baz_":  "foo-bar-baz",
		" foo_bar-baz ": "foo-bar-baz",
	}

	for input, expected := range testStrings {
		output := sluggy(input)
		if output != expected {
			t.Errorf("expected '%s', got '%s'", expected, output)
		}
	}
}
