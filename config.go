// Package forge provides a framework for building performant libp2p server applications.
package forge

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	forgehost "github.com/twostack/go-p2p-forge/host"
	"github.com/twostack/go-p2p-forge/node"
)

// Config holds framework-level server configuration.
// Application-specific config (storage, domain logic) lives in the application.
type Config struct {
	// Host configuration (network, relay, transport)
	Host forgehost.Config `yaml:"host"`

	// Node configuration (DHT, PubSub)
	Node node.Config `yaml:"node"`

	// Identity
	IdentityFile  string `yaml:"identity_file"`
	DataDirectory string `yaml:"data_directory"`

	// Rate limiting defaults (can be overridden per-protocol)
	RateLimitWindow      time.Duration `yaml:"rate_limit_window"`
	MaxRequestsPerWindow int           `yaml:"max_requests_per_window"`

	// ShutdownTimeout is the maximum time to wait for active streams to drain
	// during graceful shutdown. Zero means no waiting (immediate close).
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// DefaultConfig returns sensible defaults for a forge server.
func DefaultConfig() *Config {
	return &Config{
		Host:                 *forgehost.DefaultConfig(),
		Node:                 *node.DefaultConfig(),
		DataDirectory:        "./data",
		RateLimitWindow:      time.Minute,
		MaxRequestsPerWindow: 100,
		ShutdownTimeout:      5 * time.Second,
	}
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if c.Host.Port < 0 || c.Host.Port > 65535 {
		return fmt.Errorf("invalid port: %d", c.Host.Port)
	}
	if c.MaxRequestsPerWindow <= 0 {
		return fmt.Errorf("max_requests_per_window must be positive")
	}
	if c.Host.EnableAutoRelay && !c.Host.EnableRelay {
		return fmt.Errorf("enable_auto_relay requires enable_relay")
	}
	if c.Host.EnableHolePunching && !c.Host.EnableRelay {
		return fmt.Errorf("enable_hole_punching requires enable_relay")
	}
	if c.Host.EnableRelayService && !c.Host.EnableRelay {
		return fmt.Errorf("enable_relay_service requires enable_relay")
	}
	return nil
}

// Preset modifies a Config with predefined values.
type Preset func(cfg *Config)

// DevelopmentPreset configures for local development.
var DevelopmentPreset Preset = func(cfg *Config) {
	cfg.RateLimitWindow = time.Minute
	cfg.MaxRequestsPerWindow = 1000
}

// ProductionPreset configures for production deployment.
var ProductionPreset Preset = func(cfg *Config) {
	cfg.RateLimitWindow = time.Minute
	cfg.MaxRequestsPerWindow = 100
}

// LoadConfigFromFile reads a YAML config file and applies non-zero values
// on top of the provided base config.
func LoadConfigFromFile(path string, base *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, base); err != nil {
		return fmt.Errorf("parse config file: %w", err)
	}

	return nil
}
