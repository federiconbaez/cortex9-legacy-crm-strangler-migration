package logging

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNewJSONConServicio(t *testing.T) {
	var buf bytes.Buffer
	New("facade", &buf, false).Info("hola", "route", "customer_get")
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("no es JSON: %v (%q)", err, buf.String())
	}
	if m["service"] != "facade" || m["msg"] != "hola" || m["route"] != "customer_get" {
		t.Fatalf("campos inesperados: %v", m)
	}
}

func TestNewDiscardYStderr(t *testing.T) {
	var buf bytes.Buffer
	New("x", &buf, true).Info("no debería salir")
	if buf.Len() != 0 {
		t.Fatalf("discard escribió %q", buf.String())
	}
	if New("x", nil, false) == nil {
		t.Fatal("logger nil con w nil")
	}
}
