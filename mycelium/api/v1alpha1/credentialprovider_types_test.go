package v1alpha1_test

import (
	"testing"

	"github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCredentialProvider_OAuthWithExplicitEndpoints(t *testing.T) {
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "tenant-a"},
		Spec: v1alpha1.CredentialProviderSpec{
			OAuth: &v1alpha1.OAuthProviderSpec{
				ClientID: "Iv1.abc123",
				ClientSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "github-oauth-secret"},
					Key:                  "client-secret",
				},
				Discovery: v1alpha1.OAuthDiscovery{
					AuthorizationServerMetadata: &v1alpha1.OAuthAuthorizationServerMetadata{
						Issuer:                "https://github.com",
						AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
						TokenEndpoint:         "https://github.com/login/oauth/access_token",
					},
				},
			},
		},
	}

	assert.True(t, cp.IsOAuth())
	assert.False(t, cp.IsAPIKey())
	assert.Equal(t, "Iv1.abc123", cp.Spec.OAuth.ClientID)
	assert.NotNil(t, cp.Spec.OAuth.Discovery.AuthorizationServerMetadata)
	assert.Empty(t, cp.Spec.OAuth.Discovery.DiscoveryURL)
	assert.Equal(t, "https://github.com", cp.Spec.OAuth.Discovery.AuthorizationServerMetadata.Issuer)
	assert.Equal(t, "https://github.com/login/oauth/authorize", cp.Spec.OAuth.Discovery.AuthorizationServerMetadata.AuthorizationEndpoint)
	assert.Equal(t, "https://github.com/login/oauth/access_token", cp.Spec.OAuth.Discovery.AuthorizationServerMetadata.TokenEndpoint)
}

func TestCredentialProvider_OAuthWithDiscoveryURL(t *testing.T) {
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "google", Namespace: "tenant-a"},
		Spec: v1alpha1.CredentialProviderSpec{
			OAuth: &v1alpha1.OAuthProviderSpec{
				ClientID: "google-client-id",
				ClientSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "google-secret"},
					Key:                  "client-secret",
				},
				Discovery: v1alpha1.OAuthDiscovery{
					DiscoveryURL: "https://accounts.google.com/.well-known/openid-configuration",
				},
			},
		},
	}

	assert.True(t, cp.IsOAuth())
	assert.Equal(t, "https://accounts.google.com/.well-known/openid-configuration", cp.Spec.OAuth.Discovery.DiscoveryURL)
	assert.Nil(t, cp.Spec.OAuth.Discovery.AuthorizationServerMetadata)
}

func TestCredentialProvider_OAuthWithTokenEndpointAuthMethods(t *testing.T) {
	cp := &v1alpha1.CredentialProvider{
		Spec: v1alpha1.CredentialProviderSpec{
			OAuth: &v1alpha1.OAuthProviderSpec{
				ClientID: "client",
				ClientSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "secret"},
					Key:                  "key",
				},
				Discovery: v1alpha1.OAuthDiscovery{
					AuthorizationServerMetadata: &v1alpha1.OAuthAuthorizationServerMetadata{
						Issuer:                "https://example.com",
						AuthorizationEndpoint: "https://example.com/authorize",
						TokenEndpoint:         "https://example.com/token",
						ResponseTypes:         []string{"code"},
						TokenEndpointAuthMethods: []v1alpha1.TokenEndpointAuthMethod{
							v1alpha1.TokenEndpointAuthMethodPost,
							v1alpha1.TokenEndpointAuthMethodBasic,
						},
					},
				},
			},
		},
	}

	meta := cp.Spec.OAuth.Discovery.AuthorizationServerMetadata
	assert.Equal(t, []string{"code"}, meta.ResponseTypes)
	assert.Len(t, meta.TokenEndpointAuthMethods, 2)
	assert.Equal(t, v1alpha1.TokenEndpointAuthMethodPost, meta.TokenEndpointAuthMethods[0])
	assert.Equal(t, v1alpha1.TokenEndpointAuthMethodBasic, meta.TokenEndpointAuthMethods[1])
}

func TestCredentialProvider_APIKey(t *testing.T) {
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "stripe-api", Namespace: "tenant-a"},
		Spec: v1alpha1.CredentialProviderSpec{
			APIKey: &v1alpha1.APIKeyProviderSpec{
				APIKeySecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "stripe-secret"},
					Key:                  "api-key",
				},
			},
		},
	}

	assert.False(t, cp.IsOAuth())
	assert.True(t, cp.IsAPIKey())
	assert.Equal(t, "stripe-secret", cp.Spec.APIKey.APIKeySecretRef.Name)
	assert.Equal(t, "api-key", cp.Spec.APIKey.APIKeySecretRef.Key)
}
