package v1alpha1_test

import (
	"testing"

	"github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTenantConfig_HasExpectedFields(t *testing.T) {
	tc := &v1alpha1.TenantConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "config",
			Namespace: "tenant-a",
		},
		Spec: v1alpha1.TenantConfigSpec{
			UserVerifierURL: "https://app.acme.com/verify",
			IdentityProvider: v1alpha1.IdentityProviderConfig{
				Issuer:         "https://accounts.google.com",
				Audiences:      []string{"mycelium-tenant-a"},
				AllowedClients: []string{"client-id-1"},
				AllowedScopes:  []string{"openid", "profile"},
			},
		},
	}

	assert.Equal(t, "https://app.acme.com/verify", tc.Spec.UserVerifierURL)
	assert.Equal(t, "https://accounts.google.com", tc.Spec.IdentityProvider.Issuer)
	assert.Equal(t, []string{"mycelium-tenant-a"}, tc.Spec.IdentityProvider.Audiences)
	assert.Equal(t, []string{"client-id-1"}, tc.Spec.IdentityProvider.AllowedClients)
	assert.Equal(t, []string{"openid", "profile"}, tc.Spec.IdentityProvider.AllowedScopes)
}
