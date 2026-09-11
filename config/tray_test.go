package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSystemTraySetting(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want bool
	}{
		{"missing", "{}", true},
		{"enabled", "system_tray: true", true},
		{"disabled", "system_tray: false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg Config
			if err := yaml.Unmarshal([]byte(tc.yaml), &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.ShouldShowSystemTray() != tc.want {
				t.Fatalf("ShouldShowSystemTray() = %t, want %t", cfg.ShouldShowSystemTray(), tc.want)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := Save(&cfg, path); err != nil {
				t.Fatal(err)
			}
			if err := SaveTokens(path, "new-access", "new-refresh"); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.ShouldShowSystemTray() != tc.want {
				t.Fatal("saving config or tokens changed tray visibility")
			}
		})
	}
}

func TestSaveSystemTrayPreservesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := "# Keep this comment\ntwitch:\n  client_id: ${TWITCH_CLIENT_ID}\nwatched_channels:\n  - legacy-channel\ncustom_setting: keep\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true, false} {
		if err := SaveSystemTray(path, enabled); err != nil {
			t.Fatal(err)
		}
		saved, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"# Keep this comment", "${TWITCH_CLIENT_ID}", "legacy-channel", "custom_setting: keep"} {
			if !strings.Contains(string(saved), text) {
				t.Errorf("saved config lost %q", text)
			}
		}
		var cfg Config
		if err := yaml.Unmarshal(saved, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.ShouldShowSystemTray() != enabled {
			t.Fatalf("tray visibility = %t, want %t", cfg.ShouldShowSystemTray(), enabled)
		}
	}
	if _, err := os.Stat(getChannelsPath(path)); !os.IsNotExist(err) {
		t.Fatalf("hiding the tray should not create a channels file: %v", err)
	}
}

func TestHideTrayDuringTokenRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("system_tray: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := SaveTokens(path, "new-access", "new-refresh"); err != nil {
			t.Error(err)
		}
	})
	wg.Go(func() {
		if err := SaveSystemTray(path, false); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ShouldShowSystemTray() || cfg.Twitch.AccessToken != "new-access" || cfg.Twitch.RefreshToken != "new-refresh" {
		t.Fatal("concurrent saves lost the tray setting or refreshed tokens")
	}
}
