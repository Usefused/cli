package api_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

type coverageManifest struct {
	ManifestVersion     int            `json:"manifest_version"`
	AllowedDispositions []string       `json:"allowed_dispositions"`
	Items               []coverageItem `json:"items"`
}

type coverageItem struct {
	ID                 string   `json:"id"`
	TargetDisposition  string   `json:"target_disposition"`
	OwnerCases         []string `json:"owner_cases"`
	FixtureRequirement string   `json:"fixture_requirement"`
}

func TestExecutionContractV2FixturesSemanticRoundTrip(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("..", "..", "..", "contract-fixtures", "execution", "v2_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) < 7 {
		t.Fatalf("expected at least seven v2 execution fixtures, got %d", len(fixtures))
	}
	for _, fixturePath := range fixtures {
		fixturePath := fixturePath
		t.Run(filepath.Base(fixturePath), func(t *testing.T) {
			data, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatal(err)
			}
			var contract api.ServiceRuntimeContract
			if err := json.Unmarshal(data, &contract); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(contract)
			if err != nil {
				t.Fatal(err)
			}
			assertSemanticJSON(t, encoded, data)
		})
	}
}

func TestExecutionContractUnknownCapabilityRemainsVisibleToFailClosedReader(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "execution", "v2_unknown_required_capability.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract api.ServiceRuntimeContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if got := contract.RequiredCapabilities; !reflect.DeepEqual(got, []string{"http.future-unknown.v1"}) {
		t.Fatalf("required capabilities were normalized or discarded: %#v", got)
	}
}

func TestExecutionContractFullFixtureUsesTypedNestedDTOs(t *testing.T) {
	contract := readExecutionContract(t, "v2_full.json")
	if contract.Service.Servers == nil || len(*contract.Service.Servers) != 1 {
		t.Fatalf("typed service servers did not decode: %#v", contract.Service)
	}
	if len(contract.Operations) != 1 {
		t.Fatalf("typed operations did not decode: %#v", contract.Operations)
	}
	operation := contract.Operations[0]
	if operation.Parameters == nil || operation.RequestContent == nil || len(operation.RequestContent.Representations) == 0 || operation.RequestContent.Representations[0].Schema == nil {
		t.Fatalf("typed request contract did not decode: %#v", operation)
	}
	if len(contract.Webhooks) != 1 || contract.Webhooks[0].RequestBody == nil {
		t.Fatalf("typed webhook schema did not decode: %#v", contract.Webhooks)
	}
}

// TestExecutionContractHTTPFixtureUsesTypedContracts guards the CLI boundary because map-shaped fallbacks would hide wire drift.
func TestExecutionContractHTTPFixtureUsesTypedContracts(t *testing.T) {
	contract := readExecutionContract(t, "v2_http_transport.json")
	if len(contract.Operations) != 3 {
		t.Fatalf("HTTP contract operations = %d", len(contract.Operations))
	}
	assertHTTPOperationServers(t, contract.Operations)
	assertHTTPParameters(t, contract.Operations)
}

// assertHTTPOperationServers preserves method and server-variable order so generated calls select the same target deterministically.
func assertHTTPOperationServers(t *testing.T, operations []api.ServiceRuntimeOperation) {
	t.Helper()
	methods := []string{"CONNECT", "OPTIONS", "TRACE"}
	for index, method := range methods {
		operation := operations[index]
		if operation.Method != method {
			t.Fatalf("operation %d method = %q", index, operation.Method)
		}
		if len(operation.OperationServers) != 1 {
			t.Fatalf("operation %d did not retain method/server variables: %#v", index, operation)
		}
		if len(operation.OperationServers[0].Variables) == 0 {
			t.Fatalf("operation %d has no server variables: %#v", index, operation)
		}
	}
}

// assertHTTPParameters keeps all OpenAPI locations distinct instead of inferring placement from HTTP methods.
func assertHTTPParameters(t *testing.T, operations []api.ServiceRuntimeOperation) {
	t.Helper()
	parameters := *operations[0].Parameters
	if len(parameters) != 4 {
		t.Fatalf("all parameter locations did not decode: %#v", parameters)
	}
	if parameters[3].In != "cookie" {
		t.Fatalf("cookie parameter did not decode: %#v", parameters[3])
	}
	assertHTTPQueryParameter(t, parameters[1])
	assertHTTPHeaderParameter(t, parameters[2])
	assertHTTPContentParameter(t, (*operations[2].Parameters)[0])
}

// assertHTTPQueryParameter protects explicit false values because omission has different serialization semantics.
func assertHTTPQueryParameter(t *testing.T, query api.ServiceRuntimeParameter) {
	t.Helper()
	if query.Schema == nil || query.Schema.Projection.Items == nil {
		t.Fatalf("array query schema changed: %#v", query)
	}
	if query.Serialization.Explode == nil || *query.Serialization.Explode {
		t.Fatalf("explicit explode=false changed: %#v", query)
	}
	if query.Serialization.AllowReserved == nil || !*query.Serialization.AllowReserved {
		t.Fatalf("array query serialization changed: %#v", query)
	}
}

// assertHTTPHeaderParameter protects absent pointer fields from becoming invented defaults during decoding.
func assertHTTPHeaderParameter(t *testing.T, header api.ServiceRuntimeParameter) {
	t.Helper()
	if header.Serialization.AllowReserved != nil || header.Serialization.AllowEmptyValue != nil {
		t.Fatalf("absent pointer fields became explicit: %#v", header)
	}
	if header.Deprecated != nil {
		t.Fatalf("absent deprecated field became explicit: %#v", header)
	}
}

// assertHTTPContentParameter ensures content-based parameters survive without being flattened into schema-only parameters.
func assertHTTPContentParameter(t *testing.T, parameter api.ServiceRuntimeParameter) {
	t.Helper()
	content := parameter.Content["application/json"]
	if content.Schema == nil {
		t.Fatalf("parameter content schema did not decode: %#v", content)
	}
	if content.Schema.Projection.Properties["published"].Type != "boolean" {
		t.Fatalf("parameter content property changed: %#v", content)
	}
	if content.Example == nil || len(content.Examples) != 1 {
		t.Fatalf("parameter content did not decode: %#v", content)
	}
}

// TestExecutionContractSchemaMediaFixturePreservesContracts guards the single canonical media and schema representation used across modules.
func TestExecutionContractSchemaMediaFixturePreservesContracts(t *testing.T) {
	contract := readExecutionContract(t, "v2_schema_media.json")
	if len(contract.Operations) != 3 {
		t.Fatalf("schema/media contract operations = %d", len(contract.Operations))
	}
	assertSchemaMediaRequestContent(t, contract.Operations[0].RequestContent)
	assertSchemaMediaResponses(t, contract.Operations[0].Responses)
	assertBoundedSchemas(t, contract.Operations)
	assertAdditionalPropertiesStates(t, contract.Operations)
}

// assertSchemaMediaRequestContent keeps reviewed media ordering and complete encoding objects authoritative.
func assertSchemaMediaRequestContent(t *testing.T, content *api.ServiceRuntimeRequestContent) {
	t.Helper()
	if content == nil || len(content.Representations) != 3 {
		t.Fatalf("ordered request representations did not decode: %#v", content)
	}
	if content.DefaultMediaType != "application/vnd.example.resource+json" {
		t.Fatalf("reviewed default = %q", content.DefaultMediaType)
	}
	selected := content.Representations[0]
	if selected.Schema == nil || selected.Schema.Projection.Properties["name"].Type != "string" {
		t.Fatalf("Registry projection did not decode: %#v", selected.Schema)
	}
	assertSchemaContractHash(t, selected.Schema)
	assertRawSchemaKeyword(t, selected.Schema.Raw, "allOf")
	form := content.Representations[1].Encoding["site"]
	if form.Explode == nil || *form.Explode || len(form.Headers) != 1 {
		t.Fatalf("complete request encoding changed: %#v", form)
	}
}

// assertSchemaMediaResponses ensures range responses, links, headers, and examples are not discarded by typed decoding.
func assertSchemaMediaResponses(t *testing.T, responses map[string]api.ServiceRuntimeResponseContract) {
	t.Helper()
	success := responses["200"]
	if len(success.Representations) != 3 || len(success.Headers) != 1 {
		t.Fatalf("response media/headers changed: %#v", success)
	}
	if len(success.Links) != 1 || len(responses["2XX"].Representations) != 0 {
		t.Fatalf("response links/range changed: %#v", responses)
	}
	if success.Representations[0].Example == nil || len(success.Representations[0].Examples) != 1 {
		t.Fatalf("response examples changed: %#v", success.Representations[0])
	}
}

// assertBoundedSchemas requires explicit diagnostics when a safe bounded projection cannot represent the source schema.
func assertBoundedSchemas(t *testing.T, operations []api.ServiceRuntimeOperation) {
	t.Helper()
	cycle := (*operations[1].Parameters)[0].Schema
	if cycle == nil || len(cycle.ProjectionDiagnostics) != 1 {
		t.Fatalf("cycle diagnostic missing: %#v", cycle)
	}
	assertRawSchemaKeyword(t, cycle.Raw, "$defs")
	unresolved := operations[1].Responses["200"].Representations[0].Schema
	if unresolved == nil || unresolved.ProjectionDiagnostics[0].Code != "unresolved_external_ref" {
		t.Fatalf("unresolved ref diagnostic changed: %#v", unresolved)
	}
	sizeLimited := operations[2].RequestContent.Representations[0].Schema
	if sizeLimited == nil || sizeLimited.ProjectionDiagnostics[0].Code != "schema_source_size_limit" {
		t.Fatalf("schema size diagnostic changed: %#v", sizeLimited)
	}
}

func assertRawSchemaKeyword(t *testing.T, raw json.RawMessage, keyword string) {
	t.Helper()
	schema := rawSchemaMap(t, raw)
	if _, ok := schema[keyword]; !ok {
		t.Fatalf("raw schema omitted %q: %s", keyword, raw)
	}
}

// assertAdditionalPropertiesStates preserves the four JSON Schema states because absent, false, true, and schema are not interchangeable.
func assertAdditionalPropertiesStates(t *testing.T, operations []api.ServiceRuntimeOperation) {
	t.Helper()
	falseSchema := rawSchemaMap(t, (*operations[0].Parameters)[0].Schema.Raw)
	if value, ok := falseSchema["additionalProperties"].(bool); !ok || value {
		t.Fatalf("additionalProperties=false changed: %#v", falseSchema)
	}
	request := operations[0].RequestContent
	trueSchema := rawSchemaMap(t, request.Representations[0].Schema.Raw)
	if value, ok := trueSchema["additionalProperties"].(bool); !ok || !value {
		t.Fatalf("additionalProperties=true changed: %#v", trueSchema)
	}
	mapSchema := rawSchemaMap(t, request.Representations[1].Schema.Raw)
	if _, ok := mapSchema["additionalProperties"].(map[string]any); !ok {
		t.Fatalf("additionalProperties schema changed: %#v", mapSchema)
	}
	absentSchema := rawSchemaMap(t, request.Representations[2].Schema.Raw)
	if _, ok := absentSchema["additionalProperties"]; ok {
		t.Fatalf("absent additionalProperties became explicit: %#v", absentSchema)
	}
}

func rawSchemaMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func assertSchemaContractHash(t *testing.T, contract *api.ServiceRuntimeSchemaContract) {
	t.Helper()
	var raw any
	if err := json.Unmarshal(contract.Raw, &raw); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(canonical))
	if contract.ContentHash != want {
		t.Fatalf("schema content hash = %q, want %q", contract.ContentHash, want)
	}
}

// TestExecutionContractAuthFixturePreservesStrategies prevents the CLI from selecting or flattening declared security alternatives.
func TestExecutionContractAuthFixturePreservesStrategies(t *testing.T) {
	contract := readExecutionContract(t, "v2_auth_security.json")
	assertAuthConfigs(t, *contract.Service.AuthConfigs)
	assertSecurityAlternatives(t, contract.Operations)
}

// assertAuthConfigs keeps all source-declared flows and reviewed edge policy available to connection setup.
func assertAuthConfigs(t *testing.T, configs []api.AuthConfig) {
	t.Helper()
	oauth := findAuthConfig(t, configs, "multiFlowOAuth")
	if len(oauth.OAuth2Flows) != 2 {
		t.Fatalf("multiple OAuth2 flows were collapsed: %#v", oauth)
	}
	if oauth.OAuth2Flows["authorizationCode"].RefreshURL == "" {
		t.Fatalf("OAuth2 refresh URL missing: %#v", oauth.OAuth2Flows)
	}
	oauth1 := findAuthConfig(t, configs, "signedOAuth1")
	if oauth1.Strategy == nil || oauth1.Strategy.OAuth1 == nil {
		t.Fatalf("OAuth1 strategy missing: %#v", oauth1)
	}
	if findAuthConfig(t, configs, "commaScopeOAuth").ScopesDelimiter != "comma" {
		t.Fatal("comma-delimited OAuth scopes changed")
	}
	if findAuthConfig(t, configs, "rotatingOAuth").PolicyProvenance["oauth2_flows"] != "source_spec" {
		t.Fatal("rotating OAuth provenance changed")
	}
}

// assertSecurityAlternatives protects ordered OR-of-AND semantics and their server-selection predicates.
func assertSecurityAlternatives(t *testing.T, operations []api.ServiceRuntimeOperation) {
	t.Helper()
	compound := *operations[0].SecurityRequirements
	if len(compound) != 1 || len(compound[0].Schemes) != 2 {
		t.Fatalf("compound auth alternative changed: %#v", compound)
	}
	if compound[0].ServerSelection == nil || compound[0].ServerSelection.Scheme != "clientCertificate" {
		t.Fatalf("certificate server predicate changed: %#v", compound[0])
	}
	optional := *operations[1].SecurityRequirements
	if len(optional) != 2 || len(optional[0].Schemes) != 0 {
		t.Fatalf("anonymous alternative changed: %#v", optional)
	}
}

// findAuthConfig requires exact scheme identity because security requirements
// refer to these names and normalization must not invent aliases.
func findAuthConfig(t *testing.T, configs []api.AuthConfig, name string) api.AuthConfig {
	t.Helper()
	for _, config := range configs {
		if config.Name == name {
			return config
		}
	}
	t.Fatalf("auth config %q missing", name)
	return api.AuthConfig{}
}

func TestExecutionContractTypedDTOCarriesUnknownDocumentationField(t *testing.T) {
	contract := readExecutionContract(t, "v2_unknown_documentation_field.json")
	if len(contract.Operations) != 1 || contract.Operations[0].AdditionalFields["future_documentation_hint"] == nil {
		t.Fatalf("additive documentation field was discarded: %#v", contract.Operations)
	}
}

// TestExecutionContractInboundFixturePreservesSourceMetadata keeps inert documentation separate from executable inbound contracts.
func TestExecutionContractInboundFixturePreservesSourceMetadata(t *testing.T) {
	contract := readExecutionContract(t, "v2_inbound_documentation.json")
	assertOperationDocumentation(t, contract.Operations)
	assertInboundContracts(t, contract.Webhooks)
}

// assertOperationDocumentation verifies source provenance without promoting unknown documentation into runtime behavior.
func assertOperationDocumentation(t *testing.T, operations []api.ServiceRuntimeOperation) {
	t.Helper()
	if len(operations) != 1 || operations[0].Documentation == nil {
		t.Fatalf("operation documentation missing: %#v", operations)
	}
	operation := operations[0]
	if operation.Documentation.Summary != "Create a subscription" || operation.Documentation.Extensions["x-acme-operation-docs"].Provenance != "source_spec" {
		t.Fatalf("operation documentation changed: %#v", operation.Documentation)
	}
	link := operation.Responses["201"].Links["GetSubscription"]
	if link.Parameters["id"] != "$response.body#/id" || link.Extensions["x-acme-link-docs"].Provenance != "source_spec" {
		t.Fatalf("response link changed: %#v", link)
	}
}

// assertInboundContracts keeps callbacks and top-level webhooks distinct because their routing origins differ.
func assertInboundContracts(t *testing.T, webhooks []api.ServiceRuntimeWebhook) {
	t.Helper()
	if len(webhooks) != 2 || webhooks[0].Contract == nil || webhooks[1].Contract == nil {
		t.Fatalf("inbound contracts missing: %#v", webhooks)
	}
	assertCallbackContract(t, webhooks[0].Contract)
	assertWebhookContract(t, webhooks[1].Contract)
}

// assertCallbackContract preserves the parent/runtime expression needed to derive callback routing safely.
func assertCallbackContract(t *testing.T, callback *api.InboundOperationContract) {
	t.Helper()
	if callback.Kind != "callback" || callback.RuntimeExpression != "{$request.body#/callbackUrl}" || callback.Parent == nil || callback.Parent.CallbackName != "statusCallback" {
		t.Fatalf("callback contract changed: %#v", callback)
	}
}

// assertWebhookContract keeps standalone webhook request metadata complete at the CLI boundary.
func assertWebhookContract(t *testing.T, webhook *api.InboundOperationContract) {
	t.Helper()
	if webhook.Kind != "webhook" || len(webhook.Parameters) != 1 || webhook.RequestContent == nil || webhook.Extensions["x-acme-webhook-docs"].Provenance != "source_spec" {
		t.Fatalf("top-level webhook contract changed: %#v", webhook)
	}
}

// TestRuntimeContractDecodesExtensionFieldsAtCanonicalPlacement rejects duplicate authorities for signature, upload, and catalog contracts.
func TestRuntimeContractDecodesExtensionFieldsAtCanonicalPlacement(t *testing.T) {
	root := filepath.Join("..", "..", "..", "contract-fixtures")
	signature := readJSONFixtureValue(t, filepath.Join(root, "signature", "v1_url_form_signature.json"))
	workflow := readJSONFixtureValue(t, filepath.Join(root, "workflow", "v1_media_upload.json"))
	catalog := readJSONFixtureValue(t, filepath.Join(root, "catalog", "v1_namespaced_source_collision.json"))
	payload, err := json.Marshal(map[string]any{
		"contract_version": 2, "required_capabilities": []string{},
		"service_id": "svc", "service_version_id": "ver", "version": "v1", "catalog": catalog,
		"service":    map[string]any{"id": "svc", "name": "extensions", "base_url": "https://api.example.test", "incoming_webhook_config": map[string]any{"auth_type": "hmac_signature", "signature_policy": signature}},
		"operations": []any{map[string]any{"id": "op", "name": "upload", "method": "POST", "path": "/upload", "request_content": map[string]any{"media_type": "application/octet-stream", "serialization": "raw", "representations": []any{}, "upload_workflow": workflow}}},
		"webhooks":   []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var contract api.ServiceRuntimeContract
	if err := json.Unmarshal(payload, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Catalog == nil || len(contract.Catalog.Sources) != 2 {
		t.Fatalf("catalog placement changed: %#v", contract.Catalog)
	}
	if contract.Service.IncomingWebhookConfig == nil || contract.Service.IncomingWebhookConfig.SignaturePolicy == nil {
		t.Fatalf("signature placement changed: %#v", contract.Service)
	}
	if contract.Operations[0].RequestContent.UploadWorkflow == nil || len(contract.Operations[0].RequestContent.UploadWorkflow.Modes) != 3 {
		t.Fatalf("upload workflow placement changed: %#v", contract.Operations[0].RequestContent)
	}
}

// TestExecutionContractOpenAPI32CanonicalFieldsRoundTrip guards semantic parity across the complete supported 3.2 envelope.
func TestExecutionContractOpenAPI32CanonicalFieldsRoundTrip(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "execution", "v2_openapi32.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract api.ServiceRuntimeContract
	if err := json.Unmarshal(payload, &contract); err != nil {
		t.Fatal(err)
	}
	assertOpenAPI32RuntimeFields(t, contract)
	encoded, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalentJSON(t, encoded, payload)
}

// assertOpenAPI32RuntimeFields keeps capability negotiation and typed fields checked as one atomic contract.
func assertOpenAPI32RuntimeFields(t *testing.T, contract api.ServiceRuntimeContract) {
	t.Helper()
	if len(contract.RequiredCapabilities) != 6 {
		t.Fatalf("OpenAPI 3.2 capabilities changed: %#v", contract.RequiredCapabilities)
	}
	assertOpenAPI32ServiceFields(t, contract.Service)
	operations := runtimeOperationsByName(contract.Operations)
	assertOpenAPI32MethodFields(t, operations)
	assertOpenAPI32QueryStringFields(t, operations["search"])
	assertOpenAPI32MediaFields(t, operations)
}

// assertOpenAPI32ServiceFields protects named servers, OAuth metadata, deprecation, and tag hierarchy from projection loss.
func assertOpenAPI32ServiceFields(t *testing.T, service api.ServiceRuntimeMetadata) {
	t.Helper()
	auth := (*service.AuthConfigs)[0]
	if (*service.Servers)[0].Name != "production" || auth.OAuth2MetadataURL == "" || auth.Deprecated == nil || !*auth.Deprecated {
		t.Fatalf("OpenAPI 3.2 service metadata changed: %#v", service)
	}
	if service.Documentation == nil || len(service.Documentation.Tags) != 2 || service.Documentation.Tags[1].Parent != "commerce" || service.Documentation.Tags[1].Kind != "badge" {
		t.Fatalf("OpenAPI 3.2 tag metadata changed: %#v", service.Documentation)
	}
}

// assertOpenAPI32MethodFields keeps extensible method tokens intact rather than coercing them to a legacy allowlist.
func assertOpenAPI32MethodFields(t *testing.T, operations map[string]api.ServiceRuntimeOperation) {
	t.Helper()
	if operations["querySearch"].Method != "QUERY" || operations["reportTunnel"].Method != "RePoRt" {
		t.Fatalf("OpenAPI 3.2 methods changed: %#v", operations)
	}
}

// assertOpenAPI32QueryStringFields preserves whole-query serialization as a distinct parameter location.
func assertOpenAPI32QueryStringFields(t *testing.T, operation api.ServiceRuntimeOperation) {
	t.Helper()
	parameter := (*operation.Parameters)[0]
	content := parameter.Content["application/x-www-form-urlencoded"]
	encoding := content.Encoding["phrase"]
	if parameter.In != "querystring" || encoding.AllowReserved == nil || !*encoding.AllowReserved {
		t.Fatalf("OpenAPI 3.2 method/querystring contract changed: %#v", operation)
	}
}

// assertOpenAPI32MediaFields protects sequential and positional shapes because flattening either changes request bytes.
func assertOpenAPI32MediaFields(t *testing.T, operations map[string]api.ServiceRuntimeOperation) {
	t.Helper()
	stream := operations["streamEvents"]
	request := stream.RequestContent.Representations[0]
	if request.ItemSchema == nil || stream.Responses["200"].Representations[0].ItemSchema == nil {
		t.Fatalf("OpenAPI 3.2 sequential request changed: %#v", request)
	}
	upload := operations["uploadMedia"].RequestContent.Representations[0]
	if len(upload.PrefixEncoding) != 3 || len(upload.PrefixEncoding[2].PrefixEncoding) != 1 || upload.ItemEncoding == nil {
		t.Fatalf("OpenAPI 3.2 positional request changed: %#v", upload)
	}
	if operations["health"].Responses["200"].Summary != "Healthy" {
		t.Fatalf("OpenAPI 3.2 response summary changed: %#v", operations["health"].Responses)
	}
}

func runtimeOperationsByName(operations []api.ServiceRuntimeOperation) map[string]api.ServiceRuntimeOperation {
	result := make(map[string]api.ServiceRuntimeOperation, len(operations))
	for _, operation := range operations {
		result[operation.Name] = operation
	}
	return result
}

func readJSONFixtureValue(t *testing.T, path string) any {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// TestFetchWebhooksProjectsAndDecodesInboundContract verifies the CLI reads the same inbound contract used by runtime projection.
func TestFetchWebhooksProjectsAndDecodesInboundContract(t *testing.T) {
	fixture := readExecutionContract(t, "v2_inbound_documentation.json")
	server := newGraphQLContractServer(t, func(query string) any {
		assertGraphQLFields(t, query, []string{"webhooks", "contract"})
		return map[string]any{"service": map[string]any{"webhooks": fixture.Webhooks}}
	})
	defer server.Close()

	webhooks, err := api.NewClient(server.URL, "test-key").FetchWebhooks("svc-1", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(webhooks) != 2 || webhooks[0].Contract == nil || webhooks[0].Contract.Kind != "callback" {
		t.Fatalf("inbound webhook contract changed: %#v", webhooks)
	}
}

// TestExecutionCoverageManifestIsMachineReadableAndComplete ties every fidelity
// decision to a semantic runner case instead of a temporary rollout sequence.
func TestExecutionCoverageManifestIsMachineReadableAndComplete(t *testing.T) {
	wantCounts := map[string]int{
		"coverage-manifest.json": 19,
	}
	for name, wantCount := range wantCounts {
		path := filepath.Join("..", "..", "..", "contract-fixtures", "execution", name)
		manifest := readCoverageManifest(t, path)
		if manifest.ManifestVersion != 1 || len(manifest.Items) != wantCount {
			t.Fatalf("%s has version %d and %d items, want version 1 and %d items", name, manifest.ManifestVersion, len(manifest.Items), wantCount)
		}
		if !reflect.DeepEqual(manifest.AllowedDispositions, []string{"captured", "diagnosed", "strict_error"}) {
			t.Fatalf("%s allows non-canonical dispositions: %#v", name, manifest.AllowedDispositions)
		}
		assertCoverageItems(t, name, manifest.Items)
	}
}

func readExecutionContract(t *testing.T, name string) api.ServiceRuntimeContract {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "execution", name))
	if err != nil {
		t.Fatal(err)
	}
	var contract api.ServiceRuntimeContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	return contract
}

// readCoverageManifest keeps strict fixture-ledger decoding in one helper.
func readCoverageManifest(t *testing.T, path string) coverageManifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest coverageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

// assertCoverageItems rejects duplicate or unowned decisions so the ledger
// cannot appear complete while no executable case is responsible for them.
func assertCoverageItems(t *testing.T, name string, items []coverageItem) {
	t.Helper()
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if item.ID == "" || seen[item.ID] || len(item.OwnerCases) == 0 || item.FixtureRequirement == "" {
			t.Fatalf("%s contains incomplete or duplicate item %#v", name, item)
		}
		seen[item.ID] = true
		if !validCoverageDisposition(item.TargetDisposition) {
			t.Fatalf("%s item %s has invalid disposition %q", name, item.ID, item.TargetDisposition)
		}
	}
}

// validCoverageDisposition limits the ledger to reviewed terminal outcomes.
func validCoverageDisposition(value string) bool {
	return value == "captured" || value == "diagnosed" || value == "strict_error"
}

func TestServiceVersionsRequestsAndDecodesExecutionEnvelope(t *testing.T) {
	server := newGraphQLContractServer(t, func(query string) any {
		assertGraphQLFields(t, query, []string{"contract_version", "required_capabilities", "documentation"})
		return map[string]any{"serviceVersions": []map[string]any{{
			"id": "ver-1", "service_id": "svc-1", "name": "2026-08-11",
			"contract_version": 2, "required_capabilities": []string{},
			"documentation": map[string]any{"terms_of_service": "https://example.test/terms", "tags": []any{}},
		}}}
	})
	defer server.Close()

	versions, err := api.NewClient(server.URL, "test-key").ServiceVersions("billing")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].ContractVersion != 2 || versions[0].RequiredCapabilities == nil || versions[0].Documentation == nil {
		t.Fatalf("execution envelope changed: %#v", versions)
	}
}

func assertSemanticJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("semantic JSON changed\ngot:  %s\nwant: %s", got, want)
	}
}
