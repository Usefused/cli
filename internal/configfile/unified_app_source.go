package configfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
)

var fusedConfigDeclaration = regexp.MustCompile(`(?m)^[ \t]*export[ \t]+const[ \t]+fusedConfig[ \t]*=`)

// sourceConfigDeclarations accepts only declarations in code, not examples inside comments or strings.
func sourceConfigDeclarations(source []byte) [][2]int {
	candidates := fusedConfigDeclaration.FindAllIndex(source, -1)
	declarations := make([][2]int, 0, len(candidates))
	const (
		code = iota
		lineComment
		blockComment
		singleQuote
		doubleQuote
		templateQuote
	)
	state := code
	nextCandidate := 0
	for index := 0; index < len(source); index++ {
		// Only a lexical top-level declaration may own the Engine metadata cart.
		if nextCandidate < len(candidates) && index == candidates[nextCandidate][0] {
			if state == code {
				declarations = append(declarations, [2]int{candidates[nextCandidate][0], candidates[nextCandidate][1]})
			}
			nextCandidate++
		}
		character := source[index]
		// Comments and quoted strings can contain examples that must never affect deployment scope.
		switch state {
		case code:
			// A comment opener excludes everything until its matching line or block terminator.
			if character == '/' && index+1 < len(source) && source[index+1] == '/' {
				state, index = lineComment, index+1
			} else if character == '/' && index+1 < len(source) && source[index+1] == '*' {
				state, index = blockComment, index+1
			} else if character == '\'' {
				state = singleQuote
			} else if character == '"' {
				state = doubleQuote
			} else if character == '`' {
				state = templateQuote
			}
		case lineComment:
			// Line comments stop at the newline, before the next declaration candidate.
			if character == '\n' {
				state = code
			}
		case blockComment:
			// Block comments can span lines that look like complete exported declarations.
			if character == '*' && index+1 < len(source) && source[index+1] == '/' {
				state, index = code, index+1
			}
		case singleQuote, doubleQuote, templateQuote:
			// Escaped delimiters must not release a string into code prematurely.
			if character == '\\' {
				index++
			} else if (state == singleQuote && character == '\'') || (state == doubleQuote && character == '"') || (state == templateQuote && character == '`') {
				state = code
			}
		}
	}
	return declarations
}

// ParseUnifiedAppSource turns a literal TypeScript declaration into the existing Engine plan shape.
func ParseUnifiedAppSource(source []byte, path string) (*ParsedConfig, error) {
	// Bound the full declaration and code before passing it to Engine compilation.
	if len(source) == 0 || len(source) > 256*1024 {
		return nil, fmt.Errorf("Unified App TypeScript source must be nonempty and at most 256 KiB")
	}
	declarations := sourceConfigDeclarations(source)
	// Exactly one literal declaration makes metadata extraction deterministic without evaluating authored code.
	if len(declarations) != 1 {
		return nil, fmt.Errorf("Unified App TypeScript requires exactly one exported fusedConfig JSON object")
	}
	var config UnifiedAppConfig
	decoder := json.NewDecoder(bytes.NewReader(source[declarations[0][1]:]))
	decoder.DisallowUnknownFields()
	// JSON is valid TypeScript object syntax and can be decoded without executing tenant code locally.
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode fusedConfig in %s: use a literal JSON object: %w", path, err)
	}
	// Local source and bundle fields would create two competing authorities for the immutable version.
	if config.Source != "" || config.BundleDigest != "" {
		return nil, fmt.Errorf("fusedConfig must not declare source or bundle_digest")
	}
	// The author supplies product metadata; the CLI owns the fixed wire discriminator.
	if config.APIVersion != "" && config.APIVersion != APIVersionV1 {
		return nil, fmt.Errorf("fusedConfig apiVersion must be %q", APIVersionV1)
	}
	// A source-owned declaration cannot be routed through another app kind.
	if config.Kind != "" && config.Kind != KindUnifiedApp {
		return nil, fmt.Errorf("fusedConfig kind must be unified_app")
	}
	// Type is a local workspace discriminator and cannot change an authored app's identity.
	if config.Type != "" {
		return nil, fmt.Errorf("fusedConfig must not set type")
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
