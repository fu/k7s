package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingFileGivesDefaultsAndIsWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k7s", "config.yaml")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != Version || c.Colors != Defaults {
		t.Errorf("defaults: %+v", c)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(data), "version: 1\n") || !strings.Contains(string(data), `linked: "23"`) {
		t.Errorf("written file:\n%s (%v)", data, err)
	}
}

func TestStateRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c, _ := Load(path)
	c.SetHidden("replicasets", true)
	c.SetHidden("deployments", true)
	c.SetHidden("deployments", false)
	c.SetNamespace("dev", "shop")
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Hidden("replicasets") || c.Hidden("deployments") || c.Namespaces["dev"] != "shop" {
		t.Errorf("reloaded: %+v", c)
	}
}

func TestOlderAndPartialFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// No version (written before versions existed) and only one color.
	os.WriteFile(path, []byte("colors:\n  linked: \"#123456\"\n"), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || c.Colors.Linked != "#123456" || c.Colors.Good != Defaults.Good {
		t.Errorf("migrated: %+v", c)
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
