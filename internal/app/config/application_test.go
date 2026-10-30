package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeConfig(t *testing.T) {
	for _, tc := range []struct {
		yaml string
		ok   bool
	}{{"", true}, {"runtime:\n  max-model-calls: 0\n", false}, {"runtime:\n  model-timeout: -1s\n", false}, {"runtime:\n  request-timeout: 2s\n", true}} {
		p := filepath.Join(t.TempDir(), "application.yaml")
		if err := os.WriteFile(p, []byte(tc.yaml), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadApplication(p)
		if (err == nil) != tc.ok {
			t.Fatalf("%q: %v", tc.yaml, err)
		}
		if tc.yaml == "" && cfg.Runtime.RequestTimeout != 180*time.Second {
			t.Fatal("missing defaults")
		}
	}
}
