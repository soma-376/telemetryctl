package config

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/BurntSushi/toml"
	"github.com/your-org/pulsemetry/internal/contract"
)

var codexManagedOTelKeys = []string{"environment", "log_user_prompt", "endpoint", "protocol"}

func codexOTelTable(m *contract.Manifest) map[string]any {
	environment := "production"
	if value := m.ResourceAttributes["deployment.environment"]; value != "" {
		environment = value
	}
	return map[string]any{
		"environment":     environment,
		"log_user_prompt": m.Privacy.CollectUserPrompts,
		"endpoint":        m.OTLP.Endpoint,
		"protocol":        m.OTLP.Protocol,
	}
}

// MergeCodex authoritatively synchronizes only Pulsemetry-managed [otel] keys.
func MergeCodex(path string, m *contract.Manifest, _ string, _ bool) (Result, error) {
	raw, existed, err := readFileIfExists(path)
	if err != nil {
		return Result{}, err
	}
	root := map[string]any{}
	if existed && len(raw) > 0 {
		if err := toml.Unmarshal(raw, &root); err != nil {
			return Result{}, fmt.Errorf("parse Codex config %s: %w", path, err)
		}
	}
	otel, _ := root["otel"].(map[string]any)
	if otel == nil {
		otel = map[string]any{}
	}
	for _, key := range codexManagedOTelKeys {
		delete(otel, key)
	}
	desired := codexOTelTable(m)
	managed := make([]string, 0, len(desired))
	for key, value := range desired {
		otel[key] = value
		managed = append(managed, "otel."+key)
	}
	sort.Strings(managed)
	root["otel"] = otel
	var out bytes.Buffer
	if err := toml.NewEncoder(&out).Encode(root); err != nil {
		return Result{}, err
	}
	if err := AtomicWriteFile(path, out.Bytes(), 0o600); err != nil {
		return Result{}, err
	}
	return Result{Path: path, ManagedKeys: managed, Created: !existed}, nil
}
