package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	y := "version: 1\nname: pp\nmodel: {provider: vertex, name: m}\ngateway: {url: \"https://gw/mcp\", servers: [zypp]}\n"
	t.Setenv("CUSTOS_CONFIG", base64.StdEncoding.EncodeToString([]byte(y)))
	d, err := configFromEnv("CUSTOS_CONFIG")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(d) != "pp" {
		t.Errorf("dir %s", d)
	}
	b, _ := os.ReadFile(filepath.Join(d, "agent.yaml"))
	if string(b) != y {
		t.Errorf("agent.yaml %q", b)
	}
	t.Setenv("CUSTOS_CONFIG", "not base64!")
	if _, err := configFromEnv("CUSTOS_CONFIG"); err == nil {
		t.Error("bad value accepted")
	}
}
