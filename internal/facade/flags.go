package facade

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"sync"
)

// Mode es el modo de enrutamiento de una ruta.
type Mode string

// Modos posibles, en el orden típico de avance.
const (
	ModeLegacy Mode = "legacy" // todo al legado
	ModeShadow Mode = "shadow" // responde el legado; el nuevo corre en sombra y se compara
	ModeCanary Mode = "canary" // Percent% (estable por clave) al nuevo, el resto al legado
	ModeModern Mode = "modern" // todo al nuevo
)

// RouteFlag es la configuración de una ruta.
type RouteFlag struct {
	Mode    Mode `json:"mode"`
	Percent int  `json:"percent,omitempty"` // solo en canary, 0..100
	// Shadow en canary: las requests que atiende el legado también se
	// ejecutan en sombra contra el nuevo.
	Shadow bool `json:"shadow,omitempty"`
}

// Validate revisa que la configuración tenga sentido.
func (f RouteFlag) Validate() error {
	switch f.Mode {
	case ModeLegacy, ModeShadow, ModeModern:
		if f.Percent != 0 {
			return fmt.Errorf("percent solo aplica a canary")
		}
	case ModeCanary:
		if f.Percent < 0 || f.Percent > 100 {
			return fmt.Errorf("percent fuera de rango: %d", f.Percent)
		}
	default:
		return fmt.Errorf("modo desconocido %q", f.Mode)
	}
	return nil
}

// Bucket asigna una clave a un balde 0..99 de forma estable. Incluye el
// nombre de la ruta para que el 10% de una ruta no sea siempre el mismo 10%
// de clientes en todas las rutas.
func Bucket(route, key string) int {
	h := fnv.New32a()
	_, _ = io.WriteString(h, route)
	_, _ = h.Write([]byte{0})
	_, _ = io.WriteString(h, key)
	return int(h.Sum32() % 100)
}

// UseModern decide si una request con esa clave va al servicio nuevo. Es
// monótona: subir Percent nunca saca a nadie que ya estaba adentro.
func (f RouteFlag) UseModern(route, key string) bool {
	switch f.Mode {
	case ModeModern:
		return true
	case ModeCanary:
		return Bucket(route, key) < f.Percent
	}
	return false
}

// Flags guarda la configuración por ruta. Seguro para uso concurrente.
type Flags struct {
	mu     sync.RWMutex
	routes map[string]RouteFlag
}

// NewFlags crea flags con todas las rutas en legacy.
func NewFlags() *Flags { return &Flags{routes: map[string]RouteFlag{}} }

// Get devuelve la configuración (legacy si no hay).
func (f *Flags) Get(route string) RouteFlag {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if rf, ok := f.routes[route]; ok {
		return rf
	}
	return RouteFlag{Mode: ModeLegacy}
}

// Set cambia la configuración de una ruta.
func (f *Flags) Set(route string, rf RouteFlag) error {
	if err := rf.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[route] = rf
	return nil
}

// Snapshot devuelve una copia ordenable de la configuración.
func (f *Flags) Snapshot() map[string]RouteFlag {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make(map[string]RouteFlag, len(f.routes))
	for k, v := range f.routes {
		out[k] = v
	}
	return out
}

// Load lee un JSON {"ruta": {"mode": ..., "percent": ...}} y lo aplica
// completo o no aplica nada.
func (f *Flags) Load(r io.Reader) error {
	var m map[string]RouteFlag
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return err
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := m[k].Validate(); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range m {
		f.routes[k] = v
	}
	return nil
}
