package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsEmptyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned nil config")
	}
	if len(cfg.Boxes) != 0 {
		t.Fatalf("expected no boxes, got %d", len(cfg.Boxes))
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	want := &Config{
		Boxes: []Box{
			{Name: "dev", SSHHost: "dev-box", RemotePort: 9999},
			{Name: "prod", SSHHost: "prod-box", RemotePort: 8888},
		},
		AuthToken: "secret",
	}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Parent dir should be created.
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Boxes) != 2 || got.Boxes[0].Name != "dev" || got.Boxes[1].RemotePort != 8888 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.AuthToken != "secret" {
		t.Fatalf("auth token = %q", got.AuthToken)
	}
}

func TestSaveWritesPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, &Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
}

func TestControlSocketPathOverride(t *testing.T) {
	c := &Config{ControlSocket: "/custom/control.sock"}
	got, err := c.ControlSocketPath()
	if err != nil {
		t.Fatalf("ControlSocketPath: %v", err)
	}
	if got != "/custom/control.sock" {
		t.Fatalf("got %q, want override", got)
	}
}

func TestControlSocketPathDefaultUsesXDGRuntime(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	c := &Config{}
	got, err := c.ControlSocketPath()
	if err != nil {
		t.Fatalf("ControlSocketPath: %v", err)
	}
	if got != "/run/user/1000/tether/control.sock" {
		t.Fatalf("got %q, want XDG-based default", got)
	}
}

func TestAddBoxDefaultsRemotePort(t *testing.T) {
	c := &Config{}
	if err := c.AddBox(Box{Name: "dev", SSHHost: "dev-box"}); err != nil {
		t.Fatalf("AddBox: %v", err)
	}
	b, ok := c.Box("dev")
	if !ok {
		t.Fatal("box not found after add")
	}
	if b.RemotePort != DefaultRemotePort {
		t.Fatalf("remote port = %d, want default %d", b.RemotePort, DefaultRemotePort)
	}
}

func TestAddBoxRejectsDuplicateName(t *testing.T) {
	c := &Config{}
	if err := c.AddBox(Box{Name: "dev", SSHHost: "a"}); err != nil {
		t.Fatalf("first AddBox: %v", err)
	}
	err := c.AddBox(Box{Name: "dev", SSHHost: "b"})
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestAddBoxRequiresNameAndHost(t *testing.T) {
	c := &Config{}
	if err := c.AddBox(Box{Name: "", SSHHost: "a"}); err == nil {
		t.Fatal("expected error for empty name")
	}
	if err := c.AddBox(Box{Name: "dev", SSHHost: ""}); err == nil {
		t.Fatal("expected error for empty ssh host")
	}
}

func TestRemoveBox(t *testing.T) {
	c := &Config{}
	_ = c.AddBox(Box{Name: "dev", SSHHost: "a"})
	_ = c.AddBox(Box{Name: "prod", SSHHost: "b"})
	if err := c.RemoveBox("dev"); err != nil {
		t.Fatalf("RemoveBox: %v", err)
	}
	if _, ok := c.Box("dev"); ok {
		t.Fatal("dev still present after remove")
	}
	if _, ok := c.Box("prod"); !ok {
		t.Fatal("prod should still be present")
	}
	if err := c.RemoveBox("nope"); err == nil {
		t.Fatal("expected error removing missing box")
	}
}
