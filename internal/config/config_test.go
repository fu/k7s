package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultYAMLIsComplete(t *testing.T) {
	d := Default()
	if d.Version != Version {
		t.Errorf("default.yaml is version %d, k7s writes %d", d.Version, Version)
	}
	// Every color must come from default.yaml: nothing else fills them.
	for name, v := range map[string]string{
		"good": d.Colors.Good, "busy": d.Colors.Busy, "warn": d.Colors.Warn, "bad": d.Colors.Bad, "info": d.Colors.Info,
		"flash": d.Colors.Flash, "selected": d.Colors.Selected, "linked": d.Colors.Linked, "menu": d.Colors.Menu,
	} {
		if v == "" {
			t.Errorf("default.yaml has no color %q", name)
		}
	}
	if !d.StateHidden("pods", "completed") {
		t.Error("default.yaml hides completed pods")
	}
}

func TestMissingFileStartsAsDefaultYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k7s", "config.yaml")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != Version || c.Colors != Defaults || !c.StateHidden("replicasets", "old") {
		t.Errorf("defaults: %+v", c)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(defaultYAML) {
		t.Errorf("the first file is default.yaml, comments and all (%v)", err)
	}
}

func TestStateRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c, _ := Load(path)
	c.SetHidden("replicasets", true)
	c.SetHidden("deployments", true)
	c.SetHidden("deployments", false)
	c.SetStateHidden("pods", "completed", false)
	c.SetStateHidden("deployments", "scaled-down", true)
	c.SetNamespace("dev", "shop")
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Hidden("replicasets") || c.Hidden("deployments") || c.Namespaces["dev"] != "shop" ||
		c.StateHidden("pods", "completed") || !c.StateHidden("deployments", "scaled-down") {
		t.Errorf("reloaded: %+v", c)
	}
}

func TestVersion1FileIsMigrated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// Version 1 had no hide; this one also sets just one color.
	os.WriteFile(path, []byte("version: 1\nsections:\n  hidden: [secrets]\ncolors:\n  linked: \"#123456\"\n"), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 2 || !c.Hidden("secrets") || !c.StateHidden("pods", "completed") ||
		c.Colors.Linked != "#123456" || c.Colors.Good != Defaults.Good {
		t.Errorf("migrated: %+v", c)
	}
}

func TestEmptyHideIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("version: 2\nhide: {}\n"), 0o644)
	c, _ := Load(path)
	if c.StateHidden("pods", "completed") {
		t.Error("an empty hide means show everything, not the defaults")
	}
}

func TestNewerFileIsNeverOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	newer := "version: 99\nsections:\n  hidden: [pods]\nsomethingNew: true\n"
	os.WriteFile(path, []byte(newer), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.ReadOnly() || !c.Hidden("pods") {
		t.Errorf("newer file: %+v", c)
	}
	c.SetHidden("secrets", true)
	c.Save()
	if data, _ := os.ReadFile(path); string(data) != newer {
		t.Errorf("a newer file was overwritten:\n%s", data)
	}
}
