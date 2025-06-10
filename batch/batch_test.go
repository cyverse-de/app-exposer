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
