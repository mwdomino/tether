// Package config loads and persists the host daemon's list of boxes (SSH
// targets whose forwards tether supervises) plus daemon-wide settings.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultRemotePort is the port opened on the box by the managed SSH forward
// (the port `tether open` dials on the agent).
const DefaultRemotePort = 9999

// Box is one SSH target whose remote-forward tether keeps alive.
type Box struct {
	// Name is the unique, user-facing identifier shown in status output.
	Name string `json:"name"`
	// SSHHost is an ssh(1) destination — typically a Host alias from
	// ~/.ssh/config, so keys/ProxyJump/etc. are reused.
	SSHHost string `json:"ssh_host"`
	// RemotePort is the loopback port bound on the box by the forward.
	RemotePort int `json:"remote_port"`
}

// Config is the on-disk daemon configuration.
type Config struct {
	Boxes []Box `json:"boxes"`
	// ControlSocket overrides the daemon's control/IPC socket path.
	ControlSocket string `json:"control_socket,omitempty"`
	// AuthToken, if set, is the shared secret the daemon requires from agents.
	AuthToken string `json:"auth_token,omitempty"`
}

// DefaultPath returns the standard config location
// ($XDG_CONFIG_HOME/tether/config.json, falling back to ~/.config).
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "tether", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	return filepath.Join(home, ".config", "tether", "config.json"), nil
}

// DefaultControlSocket returns the daemon's control/IPC socket path
// ($XDG_RUNTIME_DIR/tether/control.sock, falling back to ~/.local/share).
func DefaultControlSocket() (string, error) {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "tether", "control.sock"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	return filepath.Join(home, ".local", "share", "tether", "control.sock"), nil
}

// ControlSocketPath returns the configured control socket, or the default.
func (c *Config) ControlSocketPath() (string, error) {
	if c.ControlSocket != "" {
		return c.ControlSocket, nil
	}
	return DefaultControlSocket()
}

// Load reads the config at path. A missing file is not an error — it returns
// an empty Config so first-run has no special case.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the config to path, creating parent directories, with
// owner-only permissions (it may hold an auth token).
func Save(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

// Box returns the named box and whether it was found.
func (c *Config) Box(name string) (*Box, bool) {
	for i := range c.Boxes {
		if c.Boxes[i].Name == name {
			return &c.Boxes[i], true
		}
	}
	return nil, false
}

// AddBox appends a box, defaulting RemotePort and rejecting empty fields or a
// duplicate name.
func (c *Config) AddBox(b Box) error {
	if b.Name == "" {
		return errors.New("config: box name is required")
	}
	if b.SSHHost == "" {
		return errors.New("config: box ssh_host is required")
	}
	if _, ok := c.Box(b.Name); ok {
		return fmt.Errorf("config: box %q already exists", b.Name)
	}
	if b.RemotePort == 0 {
		b.RemotePort = DefaultRemotePort
	}
	c.Boxes = append(c.Boxes, b)
	return nil
}

// RemoveBox deletes the named box, erroring if it is not present.
func (c *Config) RemoveBox(name string) error {
	for i := range c.Boxes {
		if c.Boxes[i].Name == name {
			c.Boxes = append(c.Boxes[:i], c.Boxes[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("config: box %q not found", name)
}
