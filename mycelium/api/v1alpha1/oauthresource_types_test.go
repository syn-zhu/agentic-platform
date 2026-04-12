package v1alpha1_test

import (
	"testing"

	"github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestOAuthResource_HasExpectedFields(t *testing.T) {
	r := &v1alpha1.OAuthResource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "github",
			Namespace: "tenant-a",
		},
		Spec: v1alpha1.OAuthResourceSpec{
			AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
			TokenEndpoint:         "https://github.com/login/oauth/access_token",
			ClientID:              "Iv1.abc123",
			ClientSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "github-oauth-secret",
				},
				Key: "client-secret",
			},
		},
	}

	assert.Equal(t, "Iv1.abc123", r.Spec.ClientID)
	assert.Equal(t, "github-oauth-secret", r.Spec.ClientSecretRef.Name)
	assert.Equal(t, "client-secret", r.Spec.ClientSecretRef.Key)
	assert.Equal(t, "https://github.com/login/oauth/authorize", r.Spec.AuthorizationEndpoint)
	assert.Equal(t, "https://github.com/login/oauth/access_token", r.Spec.TokenEndpoint)
}

func TestOAuthResource_OptionalDiscoveryURL(t *testing.T) {
	r := &v1alpha1.OAuthResource{
		Spec: v1alpha1.OAuthResourceSpec{
			DiscoveryURL: "https://accounts.google.com/.well-known/openid-configuration",
		},
	}
	assert.Equal(t, "https://accounts.google.com/.well-known/openid-configuration", r.Spec.DiscoveryURL)
}
