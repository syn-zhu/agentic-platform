package v1alpha1_test

import (
	"testing"

	"github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestProject_HasExpectedFields(t *testing.T) {
	p := &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "acme",
		},
		Spec: v1alpha1.ProjectSpec{
			UserVerifierURL: "https://app.acme.com/verify",
			IdentityProvider: v1alpha1.IdentityProviderConfig{
				Issuer:         "https://accounts.google.com",
				Audiences:      []string{"mycelium-acme"},
				AllowedClients: []string{"client-id-1"},
				AllowedScopes:  []string{"openid", "profile"},
			},
		},
	}

	assert.Equal(t, "https://app.acme.com/verify", p.Spec.UserVerifierURL)
	assert.Equal(t, "https://accounts.google.com", p.Spec.IdentityProvider.Issuer)
	assert.Equal(t, []string{"mycelium-acme"}, p.Spec.IdentityProvider.Audiences)
	assert.Equal(t, []string{"client-id-1"}, p.Spec.IdentityProvider.AllowedClients)
	assert.Equal(t, []string{"openid", "profile"}, p.Spec.IdentityProvider.AllowedScopes)
}

func TestProject_IsClusterScoped(t *testing.T) {
	p := &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "acme",
			// No namespace — cluster-scoped
		},
	}
	assert.Empty(t, p.Namespace)
}

func TestProject_StatusTracksNamespace(t *testing.T) {
	p := &v1alpha1.Project{
		Status: v1alpha1.ProjectStatus{
			Namespace: "acme",
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Reconciled"},
				{Type: "NamespaceReady", Status: metav1.ConditionTrue, Reason: "Created"},
			},
		},
	}
	assert.Equal(t, "acme", p.Status.Namespace)
	assert.Len(t, p.Status.Conditions, 2)
}
