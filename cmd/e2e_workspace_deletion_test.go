package cmd

// This file is intentionally empty.
// The workspace service-deletion and version-deprecation e2e tests live in
// e2e/src/flows/workspace-service-archive-deprecate.test.ts, where they run
// against live Registry and Engine instances using the shared harness
// (describeLive, importFixtureService, runCLI, graphql verification).
//
// Mock-based Go tests are not appropriate for these flows because they do not
// exercise the Registry soft-delete and version deprecation paths that are the
// whole point of the feature.
