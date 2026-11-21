package main

import (
	"os"
	"testing"
)

func TestAutoplayConfigPersists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := loadConfig()
	if err != nil || cfg.autoplay {
		t.Fatalf("initial config = %+v, err = %v", cfg, err)
	}
	if err := os.WriteFile(cfg.path, []byte("autoplay = false\n[other]\nkeep = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.saveAutoplay(true); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig()
	if err != nil || !cfg.autoplay {
		t.Fatalf("reloaded config = %+v, err = %v", cfg, err)
	}
	values, err := readConfig(cfg.path)
	if err != nil {
		t.Fatal(err)
	}
	if other, ok := values["other"].(map[string]any); !ok || other["keep"] != true {
		t.Errorf("unrelated settings were lost: %v", values)
	}
}
