package configfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
)

var fusedConfigDeclaration = regexp.MustCompile(`(?m)^[ \t]*export[ \t]+const[ \t]+fusedConfig[ \t]*=`)

var unifiedAppOptions = regexp.MustCompile(`\bexport\s+default\s+buildUnifiedApp\s*\(\s*\{`)
var inlineAppConfig = regexp.MustCompile(`^\s*config\s*:\s*`)

// sourceCodeMask preserves byte offsets while excluding comments and quoted examples from structural metadata scans.
func sourceCodeMask(source []byte) []byte {
	masked := append([]byte(nil), source...)
	const (
		code = iota
		lineComment
		blockComment
		singleQuote
		doubleQuote
		templateQuote
	)
	state := code
	for index := 0; index < len(source); index++ {
		character := source[index]
		// Only delimiters in live code may open non-code regions.
		if state == code {
			switch {
			case character == '/' && index+1 < len(source) && source[index+1] == '/':
				state = lineComment
			case character == '/' && index+1 < len(source) && source[index+1] == '*':
				state = blockComment
			case character == '\'':
				state = singleQuote
			case character == '"':
				state = doubleQuote
			case character == '`':
				state = templateQuote
			default:
				continue
			}
			masked[index] = ' '
			// Skip the second opener byte so an empty string or comment cannot close itself.
			if state == lineComment || state == blockComment {
				index++
				masked[index] = ' '
			}
			continue
		}
		masked[index] = ' '
		// Escaped string delimiters remain part of their original quoted region.
		if state >= singleQuote && character == '\\' && index+1 < len(source) {
			index++
			masked[index] = ' '
			continue
		}
		switch state {
		case lineComment:
			// Preserve line boundaries for legacy exported declarations.
			if character == '\n' {
				state = code
				masked[index] = '\n'
			}
		case blockComment:
			// A comment ends only at its matching terminator.
			if character == '*' && index+1 < len(source) && source[index+1] == '/' {
				index++
				masked[index] = ' '
				state = code
			}
		default:
			// Quoted source examples cannot introduce metadata properties.
			if (state == singleQuote && character == '\'') || (state == doubleQuote && character == '"') || (state == templateQuote && character == '`') {
				state = code
			}
		}
	}
	return masked
}

// sourceConfigDeclarations accepts only the current inline metadata authority.
func sourceConfigDeclarations(source []byte) ([][2]int, error) {
	masked := sourceCodeMask(source)
	declarations := make([][2]int, 0)
	// Retired exports must be migrated rather than silently competing with the canonical config property.
	if fusedConfigDeclaration.Match(masked) {
		return nil, fmt.Errorf("fusedConfig is no longer supported; migrate metadata into buildUnifiedApp({ config: { ... } })")
	}
	apps := unifiedAppOptions.FindAllIndex(masked, -1)
	// More than one default app cannot define an unambiguous deployment configuration.
	if len(apps) > 1 {
		return nil, fmt.Errorf("use exactly one default buildUnifiedApp declaration")
	}
	for _, app := range apps {
		depth, propertyStart := 0, app[1]
		for index := app[1]; index < len(masked); index++ {
			// Only direct options can declare config; nested schemas and execute bodies never supply deployment metadata.
			if depth == 0 && index == propertyStart {
				// Spreads could override the statically reviewed config at runtime.
				if bytes.HasPrefix(bytes.TrimSpace(masked[index:]), []byte("...")) {
					return nil, fmt.Errorf("use explicit buildUnifiedApp properties instead of an options spread")
				}
				if match := inlineAppConfig.FindIndex(masked[index:]); match != nil {
					declarations = append(declarations, [2]int{index, index + match[1]})
				}
			}
			character := masked[index]
			// The default app's options end before any following source statements.
			if depth == 0 && character == '}' {
				break
			}
			switch character {
			case '{', '[', '(':
				depth++
			case '}', ']', ')':
				depth--
			case ',':
				// A root comma begins the next direct option rather than a nested object member.
				if depth == 0 {
					propertyStart = index + 1
				}
			}
		}
	}
	return declarations, nil
}

// EmbedUnifiedAppConfig places reviewed metadata inside the executable app without changing its implementation.
func EmbedUnifiedAppConfig(source []byte, metadata []byte) ([]byte, error) {
	masked := sourceCodeMask(source)
	apps := unifiedAppOptions.FindAllIndex(masked, -1)
	// Generated source must expose a single literal options object for portable static configuration.
	if len(apps) != 1 {
		return nil, fmt.Errorf("use one default buildUnifiedApp({ ... }) declaration")
	}
	declarations, err := sourceConfigDeclarations(source)
	// Replacing model-supplied deployment settings would conceal competing configuration authority.
	if err != nil {
		return nil, err
	}
	if len(declarations) != 0 {
		return nil, fmt.Errorf("source already declares deployment metadata")
	}
	start := apps[0][1]
	result := append([]byte(nil), source[:start]...)
	result = append(result, []byte("\n  config: ")...)
	result = append(result, metadata...)
	result = append(result, ',')
	result = append(result, source[start:]...)
	return result, nil
}

// ParseUnifiedAppSource turns a literal app metadata property into the existing Engine plan shape.
func ParseUnifiedAppSource(source []byte, path string) (*ParsedConfig, error) {
	// Bound the full declaration and code before passing it to Engine compilation.
	if len(source) == 0 || len(source) > 256*1024 {
		return nil, fmt.Errorf("Unified App TypeScript source must be nonempty and at most 256 KiB")
	}
	declarations, err := sourceConfigDeclarations(source)
	// A conflicting options expression cannot establish static deployment authority.
	if err != nil {
		return nil, err
	}
	// Exactly one literal declaration makes metadata extraction deterministic without evaluating authored code.
	if len(declarations) != 1 {
		return nil, fmt.Errorf("Unified App TypeScript requires exactly one config JSON object inside buildUnifiedApp")
	}
	var config UnifiedAppConfig
	decoder := json.NewDecoder(bytes.NewReader(source[declarations[0][1]:]))
	decoder.DisallowUnknownFields()
	// JSON is valid TypeScript object syntax and can be decoded without executing tenant code locally.
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode config in %s: use a literal JSON object: %w", path, err)
	}
	// Local source and bundle fields would create two competing authorities for the immutable version.
	if config.Source != "" || config.BundleDigest != "" {
		return nil, fmt.Errorf("config must not declare source or bundle_digest")
	}
	// The author supplies product metadata; the CLI owns the fixed wire discriminator.
	if config.APIVersion != "" && config.APIVersion != APIVersionV1 {
		return nil, fmt.Errorf("config apiVersion must be %q", APIVersionV1)
	}
	// A source-owned declaration cannot be routed through another app kind.
	if config.Kind != "" && config.Kind != KindUnifiedApp {
		return nil, fmt.Errorf("config kind must be unified_app")
	}
	// Type is a local workspace discriminator and cannot change an authored app's identity.
	if config.Type != "" {
		return nil, fmt.Errorf("config must not set type")
	}
	config.APIVersion = APIVersionV1
	config.Kind = KindUnifiedApp
	config.Source = string(source)
	// The entire file binds both reviewed metadata and executable code to a single plan receipt.
	hash := sha256.Sum256(source)
	parsed := &ParsedConfig{Kind: KindUnifiedApp, Path: path, SourceHash: fmt.Sprintf("sha256:%x", hash), UnifiedApp: &config}
	parsed.ConfigKey = appConfigKey(KindUnifiedApp, config.Name, config.Version)
	// Shared app validation checks the normalized Engine cart against the hosted runtime rules.
	if err := validateAppConfig(&config, KindUnifiedApp); err != nil {
		return nil, err
	}
	return parsed, nil
}
