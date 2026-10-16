// Package config is k7s' YAML file, ~/.config/k7s/config.yaml: the colors,
// and the state k7s comes back to (hidden sections, the last namespace per
// context). k7s writes it itself; a file from a newer k7s is only read.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// Version is the file format this k7s writes. Bump it on a breaking
// change and teach migrate how to read the older ones.
const Version = 1

type Config struct {
	Version  int      `yaml:"version"`
	Sections Sections `yaml:"sections"`
	// Namespaces is the last namespace per context, used when -n is not given.
	Namespaces map[string]string `yaml:"namespaces,omitempty"`
	Colors     Colors            `yaml:"colors"`

	path     string
	readOnly bool // written by a newer k7s: never overwrite it
}

type Sections struct {
	Hidden []string `yaml:"hidden"`
}

// Colors are ANSI numbers ("0"–"255") or "#rrggbb".
type Colors struct {
	Good     string `yaml:"good"`     // running, synced, healthy
	Busy     string `yaml:"busy"`     // on its way: pending, rolling out
	Warn     string `yaml:"warn"`     // worth a look: restarts, high usage
	Bad      string `yaml:"bad"`      // failing
	Info     string `yaml:"info"`     // accents, the k7s badge, the menus' keys
	Flash    string `yaml:"flash"`    // background of a cell that just changed
	Selected string `yaml:"selected"` // background of the selected row
	Linked   string `yaml:"linked"`   // background of rows linked to it
	Menu     string `yaml:"menu"`     // background of the menus
}

// Defaults are the built-in colors.
var Defaults = Colors{
	Good: "2", Busy: "214", Warn: "3", Bad: "1", Info: "6",
	Flash: "3", Selected: "237", Linked: "23", Menu: "236",
}

// Path is where the config lives: $XDG_CONFIG_HOME/k7s/config.yaml, by
// default ~/.config/k7s/config.yaml.
func Path() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "k7s", "config.yaml")
}

// Load reads the config at path. A missing file gives the defaults (and is
// written, so the colors can be found and edited); a missing color falls
// back to its default.
func Load(path string) (*Config, error) {
	c := &Config{path: path}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.Version, c.Colors = Version, Defaults
		return c, c.Save()
	case err != nil:
		return nil, err
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.migrate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.Colors.fill(Defaults)
	return c, nil
}

// migrate brings an older file to Version. A file without a version is
// version 1. A newer one is read as far as this k7s understands it and
// never written back, so its settings survive.
func (c *Config) migrate() error {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Version > Version {
		c.readOnly = true
		return nil
	}
	// Future: if c.Version == 1 { …convert…; c.Version = 2 }
	return nil
}

func (cs *Colors) fill(d Colors) {
	for _, f := range []struct {
		v *string
		d string
	}{
		{&cs.Good, d.Good}, {&cs.Busy, d.Busy}, {&cs.Warn, d.Warn}, {&cs.Bad, d.Bad}, {&cs.Info, d.Info},
		{&cs.Flash, d.Flash}, {&cs.Selected, d.Selected}, {&cs.Linked, d.Linked}, {&cs.Menu, d.Menu},
	} {
		if *f.v == "" {
			*f.v = f.d
		}
	}
}

// ReadOnly reports whether the file came from a newer k7s.
func (c *Config) ReadOnly() bool { return c.readOnly }

// Save writes the config (atomically: a crash leaves the old file).
func (c *Config) Save() error {
	if c == nil || c.readOnly || c.path == "" {
		return nil
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return err
	}
	data := b.Bytes()
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Hidden reports whether a section is hidden.
func (c *Config) Hidden(section string) bool {
	for _, s := range c.Sections.Hidden {
		if s == section {
			return true
		}
	}
	return false
}

// SetHidden shows or hides a section.
func (c *Config) SetHidden(section string, hidden bool) {
	var out []string
	for _, s := range c.Sections.Hidden {
		if s != section {
			out = append(out, s)
		}
	}
	if hidden {
		out = append(out, section)
	}
	c.Sections.Hidden = out
}

// SetNamespace remembers the namespace last used in a context.
func (c *Config) SetNamespace(context, ns string) {
	if c.Namespaces == nil {
		c.Namespaces = map[string]string{}
	}
	c.Namespaces[context] = ns
}
