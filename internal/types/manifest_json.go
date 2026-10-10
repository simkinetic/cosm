// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package types

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// manifestPlain is Manifest without its JSON methods (avoids recursion).
type manifestPlain Manifest

// manifestKeys are the top-level keys Manifest models, read from its json tags so
// they can never drift from the struct.
var manifestKeys = func() map[string]bool {
	keys := map[string]bool{}
	t := reflect.TypeOf(manifestPlain{})
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}()

// modelled reports whether encoding/json maps key k onto a Manifest field. That
// match is case-insensitive ("Version" fills Version), so such a key is modelled,
// not extra — keeping it as well would write a stale duplicate that wins on reload.
func modelled(k string) bool {
	for known := range manifestKeys {
		if strings.EqualFold(k, known) {
			return true
		}
	}
	return false
}

// UnmarshalJSON decodes a manifest, keeping any key cosm doesn't model in Extra.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	var p manifestPlain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	for k := range all {
		if modelled(k) {
			delete(all, k)
		}
	}
	p.Extra = nil
	if len(all) > 0 {
		p.Extra = all
	}
	*m = Manifest(p)
	return nil
}

// MarshalJSON encodes a manifest with the modelled fields in their usual order,
// followed by any preserved Extra keys (sorted, for stable output).
func (m Manifest) MarshalJSON() ([]byte, error) {
	out, err := json.Marshal(manifestPlain(m))
	if err != nil || len(m.Extra) == 0 {
		return out, err
	}
	keys := make([]string, 0, len(m.Extra))
	for k := range m.Extra {
		if !modelled(k) { // a modelled field always wins over a stale extra
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.Write(out[:len(out)-1]) // drop the closing brace
	for _, k := range keys {
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(m.Extra[k])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// UnknownKeys lists the top-level cosm.json keys cosm doesn't use, sorted.
func (m Manifest) UnknownKeys() []string {
	keys := make([]string, 0, len(m.Extra))
	for k := range m.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ManifestKeys returns the top-level keys cosm.json models, sorted.
func ManifestKeys() []string {
	keys := make([]string, 0, len(manifestKeys))
	for k := range manifestKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
