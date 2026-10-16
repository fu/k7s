// Package config is k7s' YAML file, ~/.config/k7s/config.yaml: the colors,
// and the state k7s comes back to (hidden sections and rows, the last
// namespace per context). The defaults are default.yaml, compiled in.
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Version is the file format this k7s writes. Bump it when a key is added
// or changed (an older k7s would drop it when saving) and teach migrate how
// to read the older ones.
//
//	1  version, sections, namespaces, colors
//	2  + hide
//	3  + commands
const Version = 3

//go:embed default.yaml
var defaultYAML []byte

type Config struct {
	Version  int      `yaml:"version"`
	Sections Sections `yaml:"sections"`
	// Hide lists, per kind, the row states not shown, e.g. pods: [completed].
	Hide map[string][]string `yaml:"hide"`
	// Commands are, per section ("*" for every section), the commands the
	// selected row offers and their keys, e.g. pods: {shell: h, logs: l enter}.
	Commands map[string]map[string]string `yaml:"commands"`
	// Namespaces is the last namespace per context, used when -n is not given.
	Namespaces map[string]string `yaml:"namespaces,omitempty"`
	Colors     Colors            `yaml:"colors"`

	path     string
	readOnly bool // written by a newer k7s: never overwrite it
}

type Sections struct {
	Hidden []string `yaml:"hidden"`
}

// Colors are "#rrggbb" or ANSI numbers ("0"–"255").
type Colors struct {
	Good     string `yaml:"good"`
	Busy     string `yaml:"busy"`
	Warn     string `yaml:"warn"`
	Bad      string `yaml:"bad"`
	Info     string `yaml:"info"`
	Flash    string `yaml:"flash"`
	Selected string `yaml:"selected"`
	Linked   string `yaml:"linked"`
	Menu     string `yaml:"menu"`
}

// Defaults are the colors of default.yaml.
var Defaults = Default().Colors

// Default returns the built-in config (default.yaml); it is not saved.
func Default() *Config {
	c := &Config{}
	if err := yaml.Unmarshal(defaultYAML, c); err != nil {
		panic("config: default.yaml: " + err.Error())
	}
	return c
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

// Load reads the config at path. A missing file starts as default.yaml (and
// is written, comments and all); keys missing from a file take their
// defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := write(path, defaultYAML); err != nil {
			return nil, err
		}
		data, err = defaultYAML, nil
	}
	if err != nil {
		return nil, err
	}
	c := &Config{path: path}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.migrate()
	c.fill(Default())
	return c, nil
}

// migrate brings an older file to Version. A file without a version is
// version 1. A newer one is read as far as this k7s understands it and
// never written back, so its settings survive.
func (c *Config) migrate() {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Version > Version {
		c.readOnly = true
		return
	}
	// Versions 2 and 3 only added keys; fill gives them their defaults.
	c.Version = Version
}

// fill gives keys the file does not have their defaults. A section missing
// from commands gets its default commands (so a new section brings its
// own); "pods: {}" means none.
func (c *Config) fill(d *Config) {
	if c.Hide == nil {
		c.Hide = d.Hide
	}
	if c.Commands == nil {
		c.Commands = map[string]map[string]string{}
	}
	for section, cmds := range d.Commands {
		if _, ok := c.Commands[section]; !ok {
			c.Commands[section] = cmds
		}
	}
	for _, f := range []struct {
		v *string
		d string
	}{
		{&c.Colors.Good, d.Colors.Good}, {&c.Colors.Busy, d.Colors.Busy}, {&c.Colors.Warn, d.Colors.Warn},
		{&c.Colors.Bad, d.Colors.Bad}, {&c.Colors.Info, d.Colors.Info}, {&c.Colors.Flash, d.Colors.Flash},
		{&c.Colors.Selected, d.Colors.Selected}, {&c.Colors.Linked, d.Colors.Linked}, {&c.Colors.Menu, d.Colors.Menu},
	} {
		if *f.v == "" {
			*f.v = f.d
		}
	}
}

// ReadOnly reports whether the file came from a newer k7s.
func (c *Config) ReadOnly() bool { return c != nil && c.readOnly }

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
	return write(c.path, b.Bytes())
}

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Hidden reports whether a section is hidden.
func (c *Config) Hidden(section string) bool {
	return c != nil && slices.Contains(c.Sections.Hidden, section)
}

// SetHidden shows or hides a section.
func (c *Config) SetHidden(section string, hidden bool) {
	c.Sections.Hidden = toggle(c.Sections.Hidden, section, hidden)
}

// StateHidden reports whether rows of a kind in a state are hidden.
func (c *Config) StateHidden(kind, state string) bool {
	return c != nil && state != "" && slices.Contains(c.Hide[kind], state)
}

// SetStateHidden shows or hides the rows of a kind in a state.
func (c *Config) SetStateHidden(kind, state string, hidden bool) {
	if c.Hide == nil {
		c.Hide = map[string][]string{}
	}
	c.Hide[kind] = toggle(c.Hide[kind], state, hidden)
}

// CommandKeys returns the keys a section's command is bound to (none: not
// offered); "*" applies to every section unless the section names the
// command itself.
func (c *Config) CommandKeys(section, command string) []string {
	if c == nil {
		return nil
	}
	keys, ok := c.Commands[section][command]
	if !ok {
		keys = c.Commands["*"][command]
	}
	return strings.Fields(keys)
}

// SetNamespace remembers the namespace last used in a context.
func (c *Config) SetNamespace(context, ns string) {
	if c.Namespaces == nil {
		c.Namespaces = map[string]string{}
	}
	c.Namespaces[context] = ns
}

// toggle removes s from list, and adds it back when on; never nil.
func toggle(list []string, s string, on bool) []string {
	out := []string{}
	for _, it := range list {
		if it != s {
			out = append(out, it)
		}
	}
	if on {
		out = append(out, s)
	}
	return out
}
