package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Usefused/cli/internal/configfile"
	"gopkg.in/yaml.v3"
)

// unifiedSourceConfigPaths selects explicit or discovered local declarations without contacting Engine.
func unifiedSourceConfigPaths(explicit string) ([]string, error) {
	// An explicit file is the only declaration the caller authorized this command to rewrite.
	if explicit != "" {
		return []string{explicit}, nil
	}
	paths := make([]string, 0)
	configDir := filepath.Join(".fused", "unified_app")
	entries, err := os.ReadDir(configDir)
	// A workspace without Unified App declarations has nothing to synchronize.
	if os.IsNotExist(err) {
		return paths, nil
	}
	// Read failures other than absence must not turn into an incomplete plan cart.
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		// TypeScript and editor files in this directory are not app declarations.
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}
		paths = append(paths, filepath.Join(configDir, entry.Name()))
	}
	return paths, nil
}

// syncUnifiedAppSources converts each selected declaration before planning hashes its source bytes.
func syncUnifiedAppSources(explicit string, normalizePath bool) (int, error) {
	paths, err := unifiedSourceConfigPaths(explicit)
	// Discovery errors must stop before any declaration is changed.
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, path := range paths {
		updated, err := materializeUnifiedAppSource(path, normalizePath)
		// A partial sync is reported immediately so the caller can inspect the written files.
		if err != nil {
			return changed, fmt.Errorf("sync unified_app source %s: %w", path, err)
		}
		if updated {
			changed++
		}
	}
	return changed, nil
}

// materializeUnifiedAppSource moves inline or externally linked code into the canonical editable file.
func materializeUnifiedAppSource(path string, normalizePath bool) (bool, error) {
	original, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	parsed, err := configfile.Parse(original, path)
	// Validation precedes filesystem mutation so malformed scope cannot publish code.
	if err != nil {
		return false, err
	}
	// Digest-only apps have no source file to synchronize.
	if parsed.Kind != configfile.KindUnifiedApp || parsed.UnifiedApp.Source == "" {
		return false, nil
	}
	app := parsed.UnifiedApp
	sourceFile, reference, err := unifiedAppSourcePaths(path, app.Name)
	// A source filename must be unambiguous before any existing file is inspected.
	if err != nil {
		return false, err
	}
	// Planning only migrates inline source; explicit links remain under their author's control.
	if app.SourcePath != "" && (!normalizePath || app.SourcePath == reference) {
		return false, nil
	}
	return writeUnifiedAppSource(path, sourceFile, reference, []byte(app.Source), original)
}

// writeUnifiedAppSource publishes code before its YAML reference and refuses conflicting existing code.
func writeUnifiedAppSource(configPath, sourceFile, reference string, source, original []byte) (bool, error) {
	created := false
	existing, err := os.ReadFile(sourceFile)
	// A different file under the app's canonical name may belong to another version.
	if err == nil && !bytes.Equal(existing, source) {
		return false, fmt.Errorf("unified_app source %s already exists with different content", sourceFile)
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	// Publishing source first prevents YAML from ever pointing to a missing file.
	if os.IsNotExist(err) {
		if err := atomicCreateFile(sourceFile, source, 0o644, nil); err != nil {
			return false, err
		}
		created = true
	}
	updated, err := unifiedAppSourceReference(original, reference)
	if err == nil {
		err = atomicWriteFile(configPath, updated, 0o644, func(candidate []byte) error {
			_, parseErr := configfile.Parse(candidate, configPath)
			return parseErr
		})
	}
	// A failed YAML publication must not leave a newly created orphan beside the config.
	if err != nil && created {
		_ = os.Remove(sourceFile)
	}
	return err == nil, err
}

// unifiedAppSourceReference replaces code-bearing YAML fields while retaining unrelated mapping nodes and comments.
func unifiedAppSourceReference(document []byte, reference string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(document, &root); err != nil {
		return nil, err
	}
	// A config document must be one top-level map before its source fields can be rewritten.
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("unified_app config must be a YAML mapping")
	}
	mapping := root.Content[0]
	fields := make([]*yaml.Node, 0, len(mapping.Content)+2)
	for index := 0; index < len(mapping.Content); index += 2 {
		// Replace only the source authority, leaving service and auth declarations intact.
		if mapping.Content[index].Value == "source" || mapping.Content[index].Value == "source_path" {
			continue
		}
		fields = append(fields, mapping.Content[index], mapping.Content[index+1])
	}
	fields = append(fields, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "source_path"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: reference})
	mapping.Content = fields
	return yaml.Marshal(&root)
}
