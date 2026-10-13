package configfile

import "testing"

// TestRuntimeBucketConfig validates opt-in before CLI hashing or transport can publish it.
func TestRuntimeBucketConfig(t *testing.T) {
	for _, tc := range []struct {
		kind  ConfigKind
		names []string
		valid bool
	}{
		{KindUnifiedApp, nil, true}, {KindUnifiedApp, []string{"tenant"}, true}, {KindSDK, []string{"tenant"}, false},
		{KindUnifiedApp, []string{"*"}, false}, {KindUnifiedApp, []string{"a", "a"}, false}, {KindUnifiedApp, []string{" a"}, false}, {KindUnifiedApp, make([]string, 33), false},
	} {
		err := validateRuntimeBucketConfig(&AppConfig{RuntimeBuckets: tc.names}, tc.kind)
		// CLI and Engine must reject the same ambiguous or cross-kind configurations.
		if (err == nil) != tc.valid {
			t.Fatalf("kind=%s names=%v err=%v", tc.kind, tc.names, err)
		}
	}
}
