package api

import "testing"

// TestServiceInfo_DisplaySlug covers the three cases `service <slug> show`
// (cli/cmd/service.go) relies on to avoid showing a bare, only-unique-
// per-account slug for a service the caller doesn't own.
func TestServiceInfo_DisplaySlug(t *testing.T) {
	cases := []struct {
		name string
		info ServiceInfo
		want string
	}{
		{
			name: "owned service returns bare slug",
			info: ServiceInfo{Slug: "stripe", IsOwner: true, Provider: &ServiceProviderIdentity{Handle: "acme"}},
			want: "stripe",
		},
		{
			name: "non-owned service with provider returns qualified slug",
			info: ServiceInfo{Slug: "plunk", IsOwner: false, Provider: &ServiceProviderIdentity{Handle: "acme"}},
			want: "@acme/plunk",
		},
		{
			name: "non-owned service with no provider falls back to bare slug",
			info: ServiceInfo{Slug: "plunk", IsOwner: false},
			want: "plunk",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.info.DisplaySlug(); got != tc.want {
				t.Errorf("DisplaySlug() = %q, want %q", got, tc.want)
			}
		})
	}
}
