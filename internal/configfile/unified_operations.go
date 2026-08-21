package configfile

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	MaxUnifiedOperations       = 64
	MaxUnifiedBindings         = 16
	MaxUnifiedExpressions      = 512
	MaxUnifiedValueNodes       = 10_000
	MaxUnifiedValueDepth       = 32
	MaxUnifiedEncodedBytes     = 1 << 20
	MaxUnifiedNameBytes        = 256
	MaxUnifiedDescriptionBytes = 4 << 10
	MaxUnifiedOperationIDBytes = 512
	MaxUnifiedTargetBytes      = 253
	MaxUnifiedExpressionBytes  = 4 << 10
)

var unifiedOperationNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// UnmarshalYAML inspects the original operation node before yaml.v3 can expand
// aliases. That prevents YAML-only graph structure from masquerading as a JSON
// DynamicValue while preserving strict unknown-field rejection.
func (o *UnifiedOperation) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("unified operation must be a mapping")
	}
	if err := rejectUnknownMappingKeys(node, "unified operation", "description", "input", "bindings", "output"); err != nil {
		return err
	}
	if err := rejectUnifiedYAMLGraphFeatures(node); err != nil {
		return err
	}
	type plainOperation UnifiedOperation
	var decoded plainOperation
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*o = UnifiedOperation(decoded)
	return nil
}

// rejectUnifiedYAMLGraphFeatures keeps YAML-only references out of the
// portable JSON definition sent to Engine.
func rejectUnifiedYAMLGraphFeatures(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return fmt.Errorf("DynamicValue YAML aliases and anchors are not supported")
	}
	if node.ShortTag() == "!!merge" {
		return fmt.Errorf("DynamicValue YAML merge keys are not supported")
	}
	for _, child := range node.Content {
		if err := rejectUnifiedYAMLGraphFeatures(child); err != nil {
			return err
		}
	}
	return nil
}

// UnmarshalYAML accepts the documented scalar shorthand while keeping the
// expanded binding closed to unknown fields even though it uses custom decode.
func (b *UnifiedOperationBinding) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		if node.Tag != "!!str" {
			return fmt.Errorf("unified operation binding shorthand must be an operationId string")
		}
		*b = UnifiedOperationBinding{Operation: node.Value, compact: true}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("unified operation binding must be an operationId string or mapping")
	}
	if err := rejectUnknownMappingKeys(node, "unified operation binding", "service", "operation", "input", "depends_on", "rollback", "output"); err != nil {
		return err
	}
	type expandedBinding UnifiedOperationBinding
	var decoded expandedBinding
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*b = UnifiedOperationBinding(decoded)
	return nil
}

// MarshalYAML preserves shorthand when a CLI edit rewrites an SDK config.
func (b UnifiedOperationBinding) MarshalYAML() (any, error) {
	// Service selection makes the binding expanded because scalar shorthand always selects the binding key.
	if b.compact && b.Service == "" && b.Input == nil && len(b.DependsOn) == 0 && b.Rollback == nil && b.Output == nil {
		return b.Operation, nil
	}
	type expandedBinding UnifiedOperationBinding
	return expandedBinding(b), nil
}

// UnmarshalJSON shares the closed binding decoder with YAML so both config
// formats accept exactly the same contract.
func (b *UnifiedOperationBinding) UnmarshalJSON(data []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}
	if len(node.Content) != 1 {
		return fmt.Errorf("unified operation binding must contain one value")
	}
	return b.UnmarshalYAML(node.Content[0])
}

// MarshalJSON preserves compact bindings unless expanded-only fields exist.
func (b UnifiedOperationBinding) MarshalJSON() ([]byte, error) {
	// Service selection makes the binding expanded because scalar shorthand always selects the binding key.
	if b.compact && b.Service == "" && b.Input == nil && len(b.DependsOn) == 0 && b.Rollback == nil && b.Output == nil {
		return json.Marshal(b.Operation)
	}
	type expandedBinding UnifiedOperationBinding
	return json.Marshal(expandedBinding(b))
}

// UnmarshalYAML keeps rollback definitions closed while preserving their
// DynamicValue input document for Engine's expression compiler.
func (r *UnifiedOperationRollback) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("unified operation rollback must be a mapping")
	}
	if err := rejectUnknownMappingKeys(node, "unified operation rollback", "operation", "input"); err != nil {
		return err
	}
	type plainRollback UnifiedOperationRollback
	var decoded plainRollback
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*r = UnifiedOperationRollback(decoded)
	return nil
}

// UnmarshalYAML preserves the recursive output DSL as an exact JSON-compatible
// object; semantic node validation runs after the complete SDK is decoded.
func (o *UnifiedOperationOutput) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("unified operation output must be a mapping")
	}
	decoded, err := decodeDynamicYAML(node, 0)
	if err != nil {
		return err
	}
	fields, ok := decoded.Raw.(map[string]DynamicValue)
	if !ok {
		return fmt.Errorf("unified operation output must be an object")
	}
	o.Fields = fields
	return nil
}

// UnmarshalJSON keeps JSON and YAML output definitions on one exact recursive contract.
func (o *UnifiedOperationOutput) UnmarshalJSON(data []byte) error {
	var decoded DynamicValue
	if err := decoded.UnmarshalJSON(data); err != nil {
		return err
	}
	fields, ok := decoded.Raw.(map[string]DynamicValue)
	if !ok {
		return fmt.Errorf("unified operation output must be an object")
	}
	o.Fields = fields
	return nil
}

// MarshalJSON emits the authored recursive node without an extra transport wrapper.
func (o UnifiedOperationOutput) MarshalJSON() ([]byte, error) { return json.Marshal(o.Fields) }

// MarshalYAML preserves scalar property shorthand when configuration is rewritten.
func (o UnifiedOperationOutput) MarshalYAML() (any, error) { return o.Fields, nil }

// rejectUnknownMappingKeys preserves strict decoding inside custom YAML types.
func rejectUnknownMappingKeys(node *yaml.Node, label string, allowed ...string) error {
	known := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		known[key] = struct{}{}
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if _, ok := known[key]; !ok {
			return fmt.Errorf("%s contains unknown field %q", label, key)
		}
	}
	return nil
}

// validateUnifiedOperations applies availability, naming, and structural rules.
func validateUnifiedOperations(cfg *AppConfig, kind ConfigKind) error {
	if err := validateUnifiedAvailability(cfg, kind); err != nil {
		return err
	}
	if cfg.UnifiedOperations == nil {
		return nil
	}
	if err := validateUnifiedSetBounds(cfg.UnifiedOperations); err != nil {
		return err
	}
	if err := validateUnifiedGeneratedNames(cfg.UnifiedOperations, cfg.Language); err != nil {
		return err
	}
	budget := &unifiedValueBudget{}
	for name, operation := range cfg.UnifiedOperations {
		if err := validateUnifiedOperation(name, operation, cfg.Services, budget); err != nil {
			return err
		}
	}
	return nil
}

// validateUnifiedGeneratedNames prevents language-specific symbol collisions.
func validateUnifiedGeneratedNames(operations map[string]UnifiedOperation, language string) error {
	names := make([]string, 0, len(operations))
	for name := range operations {
		names = append(names, name)
	}
	sort.Strings(names)
	if err := validateUnifiedNormalizedNames(names); err != nil {
		return err
	}
	if language == "python" {
		return validateUnifiedPythonSegments(names)
	}
	return nil
}

// validateUnifiedNormalizedNames rejects type and namespace normalization collisions.
func validateUnifiedNormalizedNames(names []string) error {
	types := make(map[string]string, len(names))
	namespaces := make(map[string]string)
	for _, name := range names {
		generated := unifiedGeneratedName(name)
		if previous, exists := types[generated]; exists && previous != name {
			return fmt.Errorf("sdk unified operations %q and %q collide as generated type names", previous, name)
		}
		types[generated] = name
		if err := validateUnifiedNamespaceNames(name, namespaces); err != nil {
			return err
		}
	}
	return nil
}

// validateUnifiedNamespaceNames tracks generated namespace paths across operations.
func validateUnifiedNamespaceNames(name string, seen map[string]string) error {
	segments := strings.Split(name, ".")
	for end := 1; end < len(segments); end++ {
		path := strings.Join(segments[:end], ".")
		generated := unifiedGeneratedName(path)
		if previous, exists := seen[generated]; exists && previous != path {
			return fmt.Errorf("sdk unified namespaces %q and %q collide after code generation", previous, path)
		}
		seen[generated] = path
	}
	return nil
}

// validateUnifiedPythonSegments rejects operation paths that cannot be emitted in Python.
func validateUnifiedPythonSegments(names []string) error {
	for _, name := range names {
		for _, segment := range strings.Split(name, ".") {
			if pythonKeyword(segment) {
				return fmt.Errorf("sdk unified operation %q contains Python keyword segment %q", name, segment)
			}
		}
	}
	return nil
}

// unifiedGeneratedName projects a dotted operation path into its generated type name.
func unifiedGeneratedName(value string) string {
	segments := strings.FieldsFunc(value, func(char rune) bool { return char == '.' || char == '_' })
	for index, segment := range segments {
		segments[index] = strings.ToUpper(segment[:1]) + segment[1:]
	}
	return strings.Join(segments, "")
}

// pythonKeyword reports whether a namespace segment is reserved by Python.
func pythonKeyword(value string) bool {
	switch value {
	case "False", "None", "True", "and", "as", "assert", "async", "await", "break", "class", "continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in", "is", "lambda", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while", "with", "yield":
		return true
	default:
		return false
	}
}

// validateUnifiedAvailability restricts composition to supported SDK languages.
func validateUnifiedAvailability(cfg *AppConfig, kind ConfigKind) error {
	if kind == KindMCP {
		if cfg.UnifiedOperations != nil {
			return fmt.Errorf("mcp config must not set unified_operations")
		}
		return nil
	}
	if cfg.UnifiedOperations == nil {
		return nil
	}
	if cfg.Language != "typescript" && cfg.Language != "python" {
		return fmt.Errorf("sdk unified_operations require language typescript or python")
	}
	return nil
}

// validateUnifiedSetBounds enforces operation count, size, and name limits.
func validateUnifiedSetBounds(operations map[string]UnifiedOperation) error {
	if len(operations) == 0 {
		return fmt.Errorf("sdk unified_operations requires at least one operation")
	}
	if len(operations) > MaxUnifiedOperations {
		return fmt.Errorf("sdk unified_operations exceeds %d operations", MaxUnifiedOperations)
	}
	if err := validateUnifiedEncodedSize(operations); err != nil {
		return err
	}
	return validateUnifiedOperationNames(operations)
}

// validateUnifiedEncodedSize bounds the canonical definition sent to Engine.
func validateUnifiedEncodedSize(operations map[string]UnifiedOperation) error {
	encoded, err := json.Marshal(operations)
	if err != nil {
		return fmt.Errorf("sdk unified_operations must contain JSON-compatible values: %w", err)
	}
	if len(encoded) > MaxUnifiedEncodedBytes {
		return fmt.Errorf("sdk unified_operations exceeds %d encoded bytes", MaxUnifiedEncodedBytes)
	}
	return nil
}

// validateUnifiedOperationNames validates source names and namespace prefixes.
func validateUnifiedOperationNames(operations map[string]UnifiedOperation) error {
	names := make([]string, 0, len(operations))
	for name := range operations {
		if len(name) == 0 || len(name) > MaxUnifiedNameBytes || !unifiedOperationNamePattern.MatchString(name) {
			return fmt.Errorf("sdk unified operation %q must use dot-separated identifier segments and at most %d bytes", name, MaxUnifiedNameBytes)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for i := 1; i < len(names); i++ {
		if strings.HasPrefix(names[i], names[i-1]+".") {
			return fmt.Errorf("sdk unified operations %q and %q collide as generated namespace paths", names[i-1], names[i])
		}
	}
	return nil
}

// validateUnifiedOperation validates one wrapper and its dependency graph.
func validateUnifiedOperation(name string, operation UnifiedOperation, services map[string]AppService, budget *unifiedValueBudget) error {
	if len(operation.Description) > MaxUnifiedDescriptionBytes {
		return fmt.Errorf("sdk unified operation %q description exceeds %d bytes", name, MaxUnifiedDescriptionBytes)
	}
	if operation.Input == nil {
		return fmt.Errorf("sdk unified operation %q requires input schema", name)
	}
	if len(operation.Bindings) == 0 {
		return fmt.Errorf("sdk unified operation %q requires at least one binding", name)
	}
	if len(operation.Bindings) > MaxUnifiedBindings {
		return fmt.Errorf("sdk unified operation %q exceeds %d bindings", name, MaxUnifiedBindings)
	}
	if err := validateUnifiedOutput(name, "root", operation.Output); err != nil {
		return err
	}
	for target, binding := range operation.Bindings {
		if err := validateUnifiedBinding(name, target, binding, services); err != nil {
			return err
		}
	}
	if err := validateUnifiedDependencies(name, operation.Bindings); err != nil {
		return err
	}
	if err := validateUnifiedDataflow(name, operation); err != nil {
		return err
	}
	return budget.addOperation(name, operation)
}

// validateUnifiedBinding checks one aliased forward call and its same-service rollback.
func validateUnifiedBinding(name, target string, binding UnifiedOperationBinding, services map[string]AppService) error {
	service, err := unifiedBindingService(name, target, binding.Service, services)
	if err != nil {
		return err
	}
	if !validUnifiedOperationID(binding.Operation) {
		return fmt.Errorf("sdk unified operation %q binding %q requires an exact operationId of at most %d bytes", name, target, MaxUnifiedOperationIDBytes)
	}
	if !service.SelectAll && !containsExactString(service.Operations, binding.Operation) {
		return fmt.Errorf("sdk unified operation %q binding %q operationId %q is not selected by that service", name, target, binding.Operation)
	}
	if err := validateUnifiedRollback(name, target, binding.Rollback, service); err != nil {
		return err
	}
	return validateUnifiedOutput(name, fmt.Sprintf("binding %q", target), binding.Output)
}

// unifiedBindingService resolves an optional explicit service while the
// binding target remains the public graph alias used by dependencies.
func unifiedBindingService(name, target, selectedService string, services map[string]AppService) (AppService, error) {
	// Every alias crosses the generated SDK and Engine boundary, so it retains the established public-target bound.
	if len(target) == 0 || len(target) > MaxUnifiedTargetBytes || strings.TrimSpace(target) != target {
		return AppService{}, fmt.Errorf("sdk unified operation %q binding target %q is not a bounded exact alias", name, target)
	}
	// Omitting service preserves the existing contract where the binding key selects the service directly.
	serviceKey := selectedService
	if serviceKey == "" {
		serviceKey = target
	}
	service, ok := services[serviceKey]
	if !ok {
		return AppService{}, fmt.Errorf("sdk unified operation %q binding %q service %q must match a configured service key", name, target, serviceKey)
	}
	return service, nil
}

// validateUnifiedRollback ensures compensation cannot escape the binding's
// already-selected service surface.
func validateUnifiedRollback(name, target string, rollback *UnifiedOperationRollback, service AppService) error {
	if rollback == nil {
		return nil
	}
	if !validUnifiedOperationID(rollback.Operation) {
		return fmt.Errorf("sdk unified operation %q binding %q rollback requires an exact operationId of at most %d bytes", name, target, MaxUnifiedOperationIDBytes)
	}
	// Rollbacks use ordinary physical execution, so they need the same immutable operation scope as forward calls.
	if !service.SelectAll && !containsExactString(service.Operations, rollback.Operation) {
		return fmt.Errorf("sdk unified operation %q binding %q rollback operationId %q is not selected by that service", name, target, rollback.Operation)
	}
	return nil
}

// validateUnifiedDependencies checks exact direct edges before detecting cycles.
func validateUnifiedDependencies(name string, bindings map[string]UnifiedOperationBinding) error {
	for _, target := range sortedUnifiedBindingTargets(bindings) {
		if err := validateUnifiedDependencyList(name, target, bindings[target].DependsOn, bindings); err != nil {
			return err
		}
	}
	return validateUnifiedDependencyCycles(name, bindings)
}

// validateUnifiedDependencyList rejects hidden, self-referential, or repeated calls.
func validateUnifiedDependencyList(name, target string, dependencies []string, bindings map[string]UnifiedOperationBinding) error {
	if len(dependencies) > MaxUnifiedBindings-1 {
		return fmt.Errorf("sdk unified operation %q binding %q exceeds %d dependencies", name, target, MaxUnifiedBindings-1)
	}
	seen := make(map[string]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		// Exact binding targets keep scheduling explicit and prevent dependencies from adding unselected provider calls.
		if _, ok := bindings[dependency]; !ok {
			return fmt.Errorf("sdk unified operation %q binding %q depends_on target %q must match another binding key", name, target, dependency)
		}
		if dependency == target {
			return fmt.Errorf("sdk unified operation %q binding %q cannot depend on itself", name, target)
		}
		if _, exists := seen[dependency]; exists {
			return fmt.Errorf("sdk unified operation %q binding %q repeats depends_on target %q", name, target, dependency)
		}
		seen[dependency] = struct{}{}
	}
	return nil
}

const (
	unifiedDependencyVisiting = iota + 1
	unifiedDependencyVisited
)

// validateUnifiedDependencyCycles rejects graphs that can never become ready.
func validateUnifiedDependencyCycles(name string, bindings map[string]UnifiedOperationBinding) error {
	states := make(map[string]int, len(bindings))
	for _, target := range sortedUnifiedBindingTargets(bindings) {
		if err := visitUnifiedDependency(name, target, bindings, states); err != nil {
			return err
		}
	}
	return nil
}

// visitUnifiedDependency performs one bounded DFS over the dependency graph.
func visitUnifiedDependency(name, target string, bindings map[string]UnifiedOperationBinding, states map[string]int) error {
	if states[target] == unifiedDependencyVisiting {
		return fmt.Errorf("sdk unified operation %q contains a depends_on cycle at binding %q", name, target)
	}
	if states[target] == unifiedDependencyVisited {
		return nil
	}
	states[target] = unifiedDependencyVisiting
	for _, dependency := range bindings[target].DependsOn {
		if err := visitUnifiedDependency(name, dependency, bindings, states); err != nil {
			return err
		}
	}
	states[target] = unifiedDependencyVisited
	return nil
}

// sortedUnifiedBindingTargets makes validation failures stable across Go map iteration.
func sortedUnifiedBindingTargets(bindings map[string]UnifiedOperationBinding) []string {
	targets := make([]string, 0, len(bindings))
	for target := range bindings {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return targets
}

// validateUnifiedDataflow limits response namespaces to the scheduling edges
// that make those responses available at execution time.

func validateUnifiedDataflow(name string, operation UnifiedOperation) error {
	bindings := operation.Bindings
	knownTargets := sortedUnifiedBindingTargets(bindings)
	for _, target := range knownTargets {
		binding := bindings[target]
		if err := validateUnifiedDynamicTargets(binding.Input, knownTargets, binding.DependsOn); err != nil {
			return fmt.Errorf("sdk unified operation %q binding %q input: %w", name, target, err)
		}
		if err := validateUnifiedBindingDataflow(name, target, binding, knownTargets); err != nil {
			return err
		}
	}
	if err := validateUnifiedOutputTargets(operation.Output, knownTargets, knownTargets); err != nil {
		return fmt.Errorf("sdk unified operation %q root output: %w", name, err)
	}
	return nil
}

// validateUnifiedBindingDataflow applies the response scope owned by one graph step.
func validateUnifiedBindingDataflow(name, target string, binding UnifiedOperationBinding, knownTargets []string) error {
	if binding.Rollback != nil {
		// A rollback compensates its own successful call, so no other provider response is available to it.
		if err := validateUnifiedDynamicTargets(binding.Rollback.Input, knownTargets, []string{target}); err != nil {
			return fmt.Errorf("sdk unified operation %q binding %q rollback input: %w", name, target, err)
		}
	}
	if err := validateUnifiedOutputTargets(binding.Output, knownTargets, []string{target}); err != nil {
		return fmt.Errorf("sdk unified operation %q binding %q output: %w", name, target, err)
	}
	return nil
}

// validateUnifiedDynamicTargets walks a DynamicValue without coercing its exact JSON leaves.
func validateUnifiedDynamicTargets(value any, knownTargets, allowedTargets []string) error {
	switch typed := value.(type) {
	case DynamicValue:
		return validateUnifiedDynamicTargets(typed.Raw, knownTargets, allowedTargets)
	case map[string]DynamicValue:
		return validateUnifiedDynamicTargetMap(typed, knownTargets, allowedTargets)
	case []DynamicValue:
		return validateUnifiedDynamicTargetSlice(typed, knownTargets, allowedTargets)
	case string:
		return validateUnifiedExpressionTargets(typed, knownTargets, allowedTargets)
	default:
		return nil
	}
}

// validateUnifiedDynamicTargetMap validates every object leaf under one policy.
func validateUnifiedDynamicTargetMap(values map[string]DynamicValue, knownTargets, allowedTargets []string) error {
	for _, value := range values {
		if err := validateUnifiedDynamicTargets(value, knownTargets, allowedTargets); err != nil {
			return err
		}
	}
	return nil
}

// validateUnifiedDynamicTargetSlice validates every array leaf under one policy.
func validateUnifiedDynamicTargetSlice(values []DynamicValue, knownTargets, allowedTargets []string) error {
	for _, value := range values {
		if err := validateUnifiedDynamicTargets(value, knownTargets, allowedTargets); err != nil {
			return err
		}
	}
	return nil
}

// validateUnifiedExpressionTargets checks every complete or interpolated
// response operand while leaving the expression grammar authoritative in plan.
func validateUnifiedExpressionTargets(value string, knownTargets, allowedTargets []string) error {
	scan, err := scanUnifiedTemplate(value)
	if err != nil {
		return err
	}
	for _, expression := range scan.expressions {
		for _, operand := range strings.Split(expression, "??") {
			rest, ok := strings.CutPrefix(strings.TrimSpace(operand), "response.")
			if !ok {
				continue
			}
			target := unifiedResponseTarget(rest, knownTargets)
			if target == "" {
				return fmt.Errorf("response reference must name a binding target")
			}
			if !containsExactString(allowedTargets, target) {
				return fmt.Errorf("response target %q is not available from the declared dependency edges", target)
			}
		}
	}
	return nil
}

type unifiedTemplateScan struct {
	expressions []string
	partCount   int
}

// scanUnifiedTemplate recognizes `${...}` expressions and the `$${` literal
// escape using the same bounded, non-recursive surface accepted by Engine.
func scanUnifiedTemplate(value string) (unifiedTemplateScan, error) {
	result := unifiedTemplateScan{}
	literal := false
	for index := 0; index < len(value); {
		if strings.HasPrefix(value[index:], "$${") {
			literal = true
			index += 3
			continue
		}
		if !strings.HasPrefix(value[index:], "${") {
			literal = true
			index++
			continue
		}
		if literal {
			result.partCount++
			literal = false
		}
		end := strings.IndexByte(value[index+2:], '}')
		if end < 0 {
			return unifiedTemplateScan{}, fmt.Errorf("DynamicValue contains unterminated interpolation")
		}
		expression := value[index+2 : index+2+end]
		if expression == "" || expression != strings.TrimSpace(expression) || strings.Contains(expression, "${") {
			return unifiedTemplateScan{}, fmt.Errorf("DynamicValue contains invalid interpolation")
		}
		result.expressions = append(result.expressions, expression)
		result.partCount++
		index += end + 3
	}
	if literal {
		result.partCount++
	}
	return result, nil
}

// unifiedResponseTarget chooses the longest exact target prefix so dotted or
// provider-qualified binding keys cannot be mistaken for shorter targets.
func unifiedResponseTarget(reference string, targets []string) string {
	matched := ""
	for _, target := range targets {
		if strings.HasPrefix(reference, target+".") && len(target) > len(matched) {
			matched = target
		}
	}
	return matched
}

// validUnifiedOperationID accepts opaque provider operation IDs while rejecting unsafe delimiters.
func validUnifiedOperationID(value string) bool {
	return value != "" && len(value) <= MaxUnifiedOperationIDBytes && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

// containsExactString tests immutable operation selection without normalization.
func containsExactString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// validateUnifiedOutput admits only the recursive typed output contract.
func validateUnifiedOutput(name, location string, output *UnifiedOperationOutput) error {
	if output == nil {
		return nil
	}
	if err := validateUnifiedOutputNode(output.Fields, true, 0); err != nil {
		return fmt.Errorf("sdk unified operation %q %s output is invalid: %w", name, location, err)
	}
	return nil
}

// validateUnifiedOutputNode checks one expanded node without attempting Engine expression compilation.
func validateUnifiedOutputNode(fields map[string]DynamicValue, root bool, depth int) error {
	if depth > MaxUnifiedValueDepth {
		return fmt.Errorf("output exceeds maximum depth %d", MaxUnifiedValueDepth)
	}
	if err := validateUnifiedOutputKeys(fields); err != nil {
		return err
	}
	typeName, ok := unifiedOutputString(fields["type"])
	if !ok || !validUnifiedOutputType(typeName) {
		return fmt.Errorf("output requires a valid type")
	}
	if root && !isConstructedUnifiedOutputObject(fields, typeName) {
		return fmt.Errorf("root output must be a constructed object with properties")
	}
	return validateUnifiedOutputNodeShape(fields, typeName, depth)
}

// isConstructedUnifiedOutputObject distinguishes the operation/binding root
// from nested pass-through objects that carry a value expression.
func isConstructedUnifiedOutputObject(fields map[string]DynamicValue, typeName string) bool {
	if typeName != "object" {
		return false
	}
	_, hasValue := fields["value"]
	_, hasProperties := unifiedOutputMap(fields["properties"])
	return !hasValue && hasProperties
}

// validateUnifiedOutputKeys keeps the authoring surface closed to misspelled schema or mapping fields.
func validateUnifiedOutputKeys(fields map[string]DynamicValue) error {
	allowed := map[string]struct{}{
		"type": {}, "value": {}, "properties": {}, "required": {}, "items": {}, "additionalProperties": {},
	}
	for _, key := range sortedUnifiedDynamicKeys(fields) {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("output contains unknown field %q", key)
		}
	}
	return nil
}

// validateUnifiedOutputNodeShape applies the mutually exclusive scalar, object, and array forms.
func validateUnifiedOutputNodeShape(fields map[string]DynamicValue, typeName string, depth int) error {
	_, hasValue := fields["value"]
	properties, hasProperties := unifiedOutputMap(fields["properties"])
	switch typeName {
	case "object":
		if hasValue {
			return validateUnifiedOutputObjectValueFields(fields, properties, hasProperties, depth+1)
		}
		if !hasProperties {
			return fmt.Errorf("constructed object output requires properties")
		}
		return validateUnifiedOutputProperties(fields, properties, depth+1)
	case "array":
		if !hasValue || hasProperties {
			return fmt.Errorf("array output requires value and cannot declare properties")
		}
		return validateUnifiedOutputArrayFields(fields)
	default:
		if !hasValue || hasProperties {
			return fmt.Errorf("%s output requires value and cannot declare properties", typeName)
		}
		return rejectUnifiedOutputStructuralFields(fields, "required", "items", "additionalProperties")
	}
}

// validateUnifiedOutputObjectValueFields rejects construction-only fields on a pass-through object.
func validateUnifiedOutputObjectValueFields(fields, properties map[string]DynamicValue, hasProperties bool, depth int) error {
	if err := rejectUnifiedOutputStructuralFields(fields, "items"); err != nil {
		return err
	}
	if err := validateUnifiedOutputAdditionalProperties(fields); err != nil {
		return err
	}
	if hasProperties {
		if err := validateUnifiedOutputSchemaProperties(properties, depth); err != nil {
			return err
		}
	}
	return validateUnifiedOutputRequired(fields, properties)
}

// validateUnifiedOutputArrayFields accepts an optional schema-only items node.
func validateUnifiedOutputArrayFields(fields map[string]DynamicValue) error {
	if err := rejectUnifiedOutputStructuralFields(fields, "required", "additionalProperties"); err != nil {
		return err
	}
	items, present := fields["items"]
	if present {
		return validateUnifiedOutputItemSchema(items)
	}
	return nil
}

// validateUnifiedOutputProperties validates constructed fields plus object-required semantics.
func validateUnifiedOutputProperties(fields map[string]DynamicValue, properties map[string]DynamicValue, depth int) error {
	if err := rejectUnifiedOutputStructuralFields(fields, "items"); err != nil {
		return err
	}
	if err := validateUnifiedOutputAdditionalProperties(fields); err != nil {
		return err
	}
	for _, name := range sortedUnifiedDynamicKeys(properties) {
		property := properties[name]
		if name == "" {
			return fmt.Errorf("output property name cannot be empty")
		}
		if nested, expanded := unifiedOutputExpandedNode(property); expanded {
			if err := validateUnifiedOutputNode(nested, false, depth); err != nil {
				return fmt.Errorf("property %q: %w", name, err)
			}
			continue
		}
		if err := validateUnifiedOutputShorthand(property, depth); err != nil {
			return fmt.Errorf("property %q: %w", name, err)
		}
	}
	return validateUnifiedOutputRequired(fields, properties)
}

// validateUnifiedOutputShorthand admits only concise scalar leaves; object and
// array properties must select their recursive contract explicitly.
func validateUnifiedOutputShorthand(value DynamicValue, depth int) error {
	if depth > MaxUnifiedValueDepth {
		return fmt.Errorf("output exceeds maximum depth %d", MaxUnifiedValueDepth)
	}
	switch value.Raw.(type) {
	case string, bool, json.Number, nil:
		return nil
	default:
		return fmt.Errorf("output shorthand must be scalar; object and array properties require type")
	}
}

// validateUnifiedOutputRequired rejects null, duplicate, or undeclared required names.
func validateUnifiedOutputRequired(fields, properties map[string]DynamicValue) error {
	value, present := fields["required"]
	if !present {
		return nil
	}
	items, ok := value.Raw.([]DynamicValue)
	if !ok {
		return fmt.Errorf("output required must be an array of property names")
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		name, ok := item.Raw.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return fmt.Errorf("output required entries must be non-empty strings")
		}
		if _, exists := properties[name]; !exists {
			return fmt.Errorf("output required property %q is not declared", name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("output required property %q is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// validateUnifiedOutputItemSchema keeps array items schema-only while leaving
// the full JSON Schema vocabulary for Engine's authoritative validator.
func validateUnifiedOutputItemSchema(value DynamicValue) error {
	return validateUnifiedOutputSchemaNode(value, 0)
}

// validateUnifiedOutputSchemaNode checks pass-through object properties and
// array items without requiring a mapping value at their scalar leaves.
func validateUnifiedOutputSchemaNode(value DynamicValue, depth int) error {
	if depth > MaxUnifiedValueDepth {
		return fmt.Errorf("output schema exceeds maximum depth %d", MaxUnifiedValueDepth)
	}
	fields, typeName, err := unifiedOutputSchemaFields(value)
	if err != nil {
		return err
	}
	return validateUnifiedOutputSchemaShape(fields, typeName, depth)
}

// unifiedOutputSchemaFields admits an explicitly typed schema node with no mapping value.
func unifiedOutputSchemaFields(value DynamicValue) (map[string]DynamicValue, string, error) {
	fields, ok := unifiedOutputMap(value)
	if !ok {
		return nil, "", fmt.Errorf("output schema must be an object")
	}
	if _, exists := fields["value"]; exists {
		return nil, "", fmt.Errorf("output schema cannot declare value")
	}
	if err := validateUnifiedOutputKeys(fields); err != nil {
		return nil, "", err
	}
	typeName, ok := unifiedOutputString(fields["type"])
	if !ok || !validUnifiedOutputType(typeName) {
		return nil, "", fmt.Errorf("output schema requires a valid type")
	}
	return fields, typeName, nil
}

// validateUnifiedOutputSchemaShape scopes recursive schema controls to their type.
func validateUnifiedOutputSchemaShape(fields map[string]DynamicValue, typeName string, depth int) error {
	switch typeName {
	case "object":
		return validateUnifiedOutputSchemaObject(fields, depth)
	case "array":
		return validateUnifiedOutputArraySchema(fields, depth)
	default:
		return rejectUnifiedOutputStructuralFields(fields, "properties", "required", "items", "additionalProperties")
	}
}

// validateUnifiedOutputSchemaObject validates optional recursive object metadata.
func validateUnifiedOutputSchemaObject(fields map[string]DynamicValue, depth int) error {
	if err := rejectUnifiedOutputStructuralFields(fields, "items"); err != nil {
		return err
	}
	properties, hasProperties := unifiedOutputMap(fields["properties"])
	if _, exists := fields["properties"]; exists && !hasProperties {
		return fmt.Errorf("output schema properties must be an object")
	}
	if hasProperties {
		if err := validateUnifiedOutputSchemaProperties(properties, depth+1); err != nil {
			return err
		}
	}
	if err := validateUnifiedOutputRequired(fields, properties); err != nil {
		return err
	}
	return validateUnifiedOutputAdditionalProperties(fields)
}

// validateUnifiedOutputArraySchema validates optional recursive array metadata.
func validateUnifiedOutputArraySchema(fields map[string]DynamicValue, depth int) error {
	if err := rejectUnifiedOutputStructuralFields(fields, "properties", "required", "additionalProperties"); err != nil {
		return err
	}
	if items, exists := fields["items"]; exists {
		return validateUnifiedOutputSchemaNode(items, depth+1)
	}
	return nil
}

// validateUnifiedOutputSchemaProperties recursively validates metadata-only
// children without admitting a value expression inside pass-through schemas.
func validateUnifiedOutputSchemaProperties(properties map[string]DynamicValue, depth int) error {
	for _, name := range sortedUnifiedDynamicKeys(properties) {
		property := properties[name]
		if name == "" {
			return fmt.Errorf("output schema property name cannot be empty")
		}
		if err := validateUnifiedOutputSchemaNode(property, depth); err != nil {
			return fmt.Errorf("output schema property %q: %w", name, err)
		}
	}
	return nil
}

// sortedUnifiedDynamicKeys makes recursive validation failures deterministic
// despite Go map iteration order.
func sortedUnifiedDynamicKeys(values map[string]DynamicValue) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// validateUnifiedOutputAdditionalProperties admits only the implemented boolean schema option.
func validateUnifiedOutputAdditionalProperties(fields map[string]DynamicValue) error {
	value, exists := fields["additionalProperties"]
	if !exists {
		return nil
	}
	if _, ok := value.Raw.(bool); !ok {
		return fmt.Errorf("output additionalProperties must be boolean")
	}
	return nil
}

// validateUnifiedOutputTargets walks only mapping-bearing output fields; array
// item schemas are metadata and therefore cannot create response dependencies.
func validateUnifiedOutputTargets(output *UnifiedOperationOutput, knownTargets, allowedTargets []string) error {
	if output == nil {
		return nil
	}
	return validateUnifiedOutputNodeTargets(output.Fields, knownTargets, allowedTargets)
}

// validateUnifiedOutputNodeTargets visits only executable value/shorthand
// nodes, leaving schema-only properties and items outside response dataflow.
func validateUnifiedOutputNodeTargets(fields map[string]DynamicValue, knownTargets, allowedTargets []string) error {
	if value, exists := fields["value"]; exists {
		return validateUnifiedDynamicTargets(value, knownTargets, allowedTargets)
	}
	properties, _ := unifiedOutputMap(fields["properties"])
	for _, name := range sortedUnifiedDynamicKeys(properties) {
		property := properties[name]
		if nested, expanded := unifiedOutputExpandedNode(property); expanded {
			if err := validateUnifiedOutputNodeTargets(nested, knownTargets, allowedTargets); err != nil {
				return err
			}
			continue
		}
		if err := validateUnifiedDynamicTargets(property, knownTargets, allowedTargets); err != nil {
			return err
		}
	}
	return nil
}

// rejectUnifiedOutputStructuralFields keeps fields scoped to the node kinds that implement them.
func rejectUnifiedOutputStructuralFields(fields map[string]DynamicValue, names ...string) error {
	for _, name := range names {
		if _, exists := fields[name]; exists {
			return fmt.Errorf("output type cannot declare %s", name)
		}
	}
	return nil
}

// unifiedOutputExpandedNode identifies only mappings that explicitly opt into a typed node.
func unifiedOutputExpandedNode(value DynamicValue) (map[string]DynamicValue, bool) {
	fields, ok := value.Raw.(map[string]DynamicValue)
	if !ok {
		return nil, false
	}
	_, expanded := fields["type"]
	return fields, expanded
}

// unifiedOutputMap reads an authored object without coercing scalar shorthand
// into an expanded node.
func unifiedOutputMap(value DynamicValue) (map[string]DynamicValue, bool) {
	fields, ok := value.Raw.(map[string]DynamicValue)
	return fields, ok
}

// unifiedOutputString reads exact string controls such as the declared type.
func unifiedOutputString(value DynamicValue) (string, bool) {
	text, ok := value.Raw.(string)
	return text, ok
}

// validUnifiedOutputType mirrors the JSON types supported by Engine output
// compilation and generated client schema types.
func validUnifiedOutputType(value string) bool {
	switch value {
	case "string", "number", "integer", "boolean", "object", "array", "null":
		return true
	default:
		return false
	}
}

type unifiedValueBudget struct {
	nodes       int
	expressions int
}

// addOperation charges every DynamicValue document against the shared bound.
func (b *unifiedValueBudget) addOperation(name string, operation UnifiedOperation) error {
	values := []any{operation.Input}
	if operation.Output != nil {
		values = append(values, operation.Output.Fields)
	}
	for _, binding := range operation.Bindings {
		values = append(values, binding.Input)
		if binding.Rollback != nil {
			values = append(values, binding.Rollback.Input)
		}
		if binding.Output != nil {
			values = append(values, binding.Output.Fields)
		}
	}
	for _, value := range values {
		if err := b.walk(value, 0); err != nil {
			return fmt.Errorf("sdk unified operation %q: %w", name, err)
		}
	}
	return nil
}

// walk counts one DynamicValue subtree while enforcing shared depth and node bounds.
func (b *unifiedValueBudget) walk(value any, depth int) error {
	if value == nil {
		return nil
	}
	if depth > MaxUnifiedValueDepth {
		return fmt.Errorf("dynamic values exceed maximum depth %d", MaxUnifiedValueDepth)
	}
	b.nodes++
	if b.nodes > MaxUnifiedValueNodes {
		return fmt.Errorf("dynamic values exceed %d nodes", MaxUnifiedValueNodes)
	}
	switch typed := value.(type) {
	case DynamicValue:
		return b.walk(typed.Raw, depth)
	case map[string]DynamicValue:
		return b.walkMap(typed, depth)
	case []DynamicValue:
		return b.walkSlice(typed, depth)
	case string:
		return b.countExpression(typed)
	default:
		return nil
	}
}

// walkMap visits each DynamicValue object member at the next depth.
func (b *unifiedValueBudget) walkMap(values map[string]DynamicValue, depth int) error {
	for _, value := range values {
		if err := b.walk(value, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// walkSlice visits each DynamicValue array item at the next depth.
func (b *unifiedValueBudget) walkSlice(values []DynamicValue, depth int) error {
	for _, value := range values {
		if err := b.walk(value, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// countExpression enforces interpolation source, expression, and compiled-node budgets.
func (b *unifiedValueBudget) countExpression(value string) error {
	if !strings.Contains(value, "${") {
		return nil
	}
	scan, err := scanUnifiedTemplate(value)
	if err != nil {
		return err
	}
	if len(scan.expressions) == 0 {
		return nil
	}
	if len(value) > MaxUnifiedExpressionBytes {
		return fmt.Errorf("DynamicValue expression exceeds %d bytes", MaxUnifiedExpressionBytes)
	}
	// The scalar itself was already charged by walk. Mixed templates also
	// compile one child node per canonical literal/expression segment.
	if !isCompleteUnifiedExpression(value, scan) {
		if err := b.addTemplateNodes(scan.partCount); err != nil {
			return err
		}
	}
	return b.addExpressions(len(scan.expressions))
}

// isCompleteUnifiedExpression distinguishes typed whole-value references from
// mixed strings that require a template parent and child nodes.
func isCompleteUnifiedExpression(value string, scan unifiedTemplateScan) bool {
	return len(scan.expressions) == 1 && scan.partCount == 1 &&
		strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}")
}

// addTemplateNodes charges the child parts emitted by Engine's template compiler.
func (b *unifiedValueBudget) addTemplateNodes(count int) error {
	b.nodes += count
	if b.nodes > MaxUnifiedValueNodes {
		return fmt.Errorf("dynamic values exceed %d nodes", MaxUnifiedValueNodes)
	}
	return nil
}

// addExpressions charges every embedded reference against the shared budget.
func (b *unifiedValueBudget) addExpressions(count int) error {
	b.expressions += count
	if b.expressions > MaxUnifiedExpressions {
		return fmt.Errorf("dynamic values exceed %d expressions", MaxUnifiedExpressions)
	}
	return nil
}
