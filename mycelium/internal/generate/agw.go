package generate

import (
	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	agwAPIVersion     = "agentgateway.dev/v1alpha1"
	gatewayAPIVersion = "gateway.networking.k8s.io/v1"
)

// MCPBackend generates an AgentgatewayBackend for the engine as an MCP server.
func MCPBackend(tc *v1alpha1.TenantConfig) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": agwAPIVersion,
			"kind":       "AgentgatewayBackend",
			"metadata": map[string]interface{}{
				"name":      "mycelium-engine",
				"namespace": tc.Namespace,
				"labels":    managedLabelsMap(),
			},
			"spec": map[string]interface{}{
				"mcp": map[string]interface{}{
					"targets": []interface{}{
						map[string]interface{}{
							"name": "mycelium-engine",
							"backendRef": map[string]interface{}{
								"name": "mycelium-engine",
							},
							"port":     int64(8080),
							"protocol": "StreamableHTTP",
						},
					},
				},
			},
		},
	}
}

// MCPRoute generates an HTTPRoute routing /mcp to the engine MCP backend.
func MCPRoute(tc *v1alpha1.TenantConfig) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": gatewayAPIVersion,
			"kind":       "HTTPRoute",
			"metadata": map[string]interface{}{
				"name":      "mcp-route",
				"namespace": tc.Namespace,
				"labels":    managedLabelsMap(),
			},
			"spec": map[string]interface{}{
				"parentRefs": []interface{}{
					map[string]interface{}{
						"name":        "tenant-gateway",
						"sectionName": "internal",
					},
				},
				"rules": []interface{}{
					map[string]interface{}{
						"matches": []interface{}{
							map[string]interface{}{
								"path": map[string]interface{}{
									"type":  "PathPrefix",
									"value": "/mcp",
								},
							},
						},
						"backendRefs": []interface{}{
							map[string]interface{}{
								"name":  "mycelium-engine",
								"group": "agentgateway.dev",
								"kind":  "AgentgatewayBackend",
							},
						},
					},
				},
			},
		},
	}
}

// JWTPolicy generates an AgentgatewayPolicy for JWT validation on the external listener.
func JWTPolicy(tc *v1alpha1.TenantConfig) *unstructured.Unstructured {
	idp := tc.Spec.IdentityProvider

	provider := map[string]interface{}{
		"issuer":    idp.Issuer,
		"audiences": toInterfaceSlice(idp.Audiences),
	}
	if len(idp.AllowedClients) > 0 {
		provider["allowedClients"] = toInterfaceSlice(idp.AllowedClients)
	}
	if len(idp.AllowedScopes) > 0 {
		provider["allowedScopes"] = toInterfaceSlice(idp.AllowedScopes)
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": agwAPIVersion,
			"kind":       "AgentgatewayPolicy",
			"metadata": map[string]interface{}{
				"name":      "jwt-auth",
				"namespace": tc.Namespace,
				"labels":    managedLabelsMap(),
			},
			"spec": map[string]interface{}{
				"targetRefs": []interface{}{
					map[string]interface{}{
						"group":       "gateway.networking.k8s.io",
						"kind":        "Gateway",
						"name":        "tenant-gateway",
						"sectionName": "external",
					},
				},
				"traffic": map[string]interface{}{
					"jwtAuthentication": map[string]interface{}{
						"mode": "Strict",
						"providers": []interface{}{
							provider,
						},
					},
				},
			},
		},
	}
}

// SourceContextPolicy generates a PreRouting transformation policy on the internal
// listener that injects source identity headers (X-Source-Pod-IP, X-Source-Service-Account).
func SourceContextPolicy(tc *v1alpha1.TenantConfig) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": agwAPIVersion,
			"kind":       "AgentgatewayPolicy",
			"metadata": map[string]interface{}{
				"name":      "internal-source-context",
				"namespace": tc.Namespace,
				"labels":    managedLabelsMap(),
			},
			"spec": map[string]interface{}{
				"targetRefs": []interface{}{
					map[string]interface{}{
						"group":       "gateway.networking.k8s.io",
						"kind":        "Gateway",
						"name":        "tenant-gateway",
						"sectionName": "internal",
					},
				},
				"traffic": map[string]interface{}{
					"phase": "PreRouting",
					"transformation": map[string]interface{}{
						"request": map[string]interface{}{
							"set": []interface{}{
								map[string]interface{}{
									"name":  "X-Source-Pod-IP",
									"value": "source.address",
								},
								map[string]interface{}{
									"name":  "X-Source-Service-Account",
									"value": "source.workload.unverified.serviceAccount",
								},
							},
						},
					},
				},
			},
		},
	}
}

// ToolAccessPolicy generates an AgentgatewayPolicy with backend.mcp.authorization
// for tool-level access control based on agent identity.
func ToolAccessPolicy(namespace string, celExpressions []string) *unstructured.Unstructured {
	exprs := make([]interface{}, len(celExpressions))
	for i, e := range celExpressions {
		exprs[i] = e
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": agwAPIVersion,
			"kind":       "AgentgatewayPolicy",
			"metadata": map[string]interface{}{
				"name":      "mcp-tool-access",
				"namespace": namespace,
				"labels":    managedLabelsMap(),
			},
			"spec": map[string]interface{}{
				"targetRefs": []interface{}{
					map[string]interface{}{
						"group": "agentgateway.dev",
						"kind":  "AgentgatewayBackend",
						"name":  "mycelium-engine",
					},
				},
				"backend": map[string]interface{}{
					"mcp": map[string]interface{}{
						"authorization": map[string]interface{}{
							"action": "Allow",
							"policy": map[string]interface{}{
								"matchExpressions": exprs,
							},
						},
					},
				},
			},
		},
	}
}

func managedLabelsMap() map[string]interface{} {
	return map[string]interface{}{"mycelium.io/managed-by": "controller"}
}

func toInterfaceSlice(ss []string) []interface{} {
	result := make([]interface{}, len(ss))
	for i, s := range ss {
		result[i] = s
	}
	return result
}
