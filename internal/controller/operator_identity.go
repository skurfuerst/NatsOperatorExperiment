package controller

import (
	"context"
	"fmt"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// OperatorIdentity holds the discovered operator deployment information.
type OperatorIdentity struct {
	Namespace      string
	DeploymentName string
}

// DiscoverOperatorIdentity discovers the operator's own Deployment name by walking
// the ownerReference chain: Pod → ReplicaSet → Deployment.
// Returns an error if discovery fails (caller should fall back to bare commands).
func DiscoverOperatorIdentity(ctx context.Context, c client.Reader, podName, podNamespace string) (*OperatorIdentity, error) {
	// Fetch the pod
	pod := &corev1.Pod{}
	if err := c.Get(ctx, types.NamespacedName{Name: podName, Namespace: podNamespace}, pod); err != nil {
		return nil, fmt.Errorf("failed to get pod %s/%s: %w", podNamespace, podName, err)
	}

	// Find ReplicaSet owner
	rsName := ""
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "ReplicaSet" {
			rsName = ref.Name
			break
		}
	}
	if rsName == "" {
		return nil, fmt.Errorf("pod %s/%s has no ReplicaSet owner", podNamespace, podName)
	}

	// Fetch the ReplicaSet
	rs := &appsv1.ReplicaSet{}
	if err := c.Get(ctx, types.NamespacedName{Name: rsName, Namespace: podNamespace}, rs); err != nil {
		return nil, fmt.Errorf("failed to get ReplicaSet %s/%s: %w", podNamespace, rsName, err)
	}

	// Find Deployment owner
	deployName := ""
	for _, ref := range rs.OwnerReferences {
		if ref.Kind == "Deployment" {
			deployName = ref.Name
			break
		}
	}
	if deployName == "" {
		return nil, fmt.Errorf("ReplicaSet %s/%s has no Deployment owner", podNamespace, rsName)
	}

	return &OperatorIdentity{
		Namespace:      podNamespace,
		DeploymentName: deployName,
	}, nil
}

// DiscoverOperatorIdentityFromEnv reads POD_NAME and POD_NAMESPACE environment variables
// and discovers the operator identity. Returns nil (no error) if env vars are not set,
// indicating the operator is likely running outside the cluster.
func DiscoverOperatorIdentityFromEnv(ctx context.Context, c client.Reader) (*OperatorIdentity, error) {
	podName := os.Getenv("POD_NAME")
	podNamespace := os.Getenv("POD_NAMESPACE")
	if podName == "" || podNamespace == "" {
		return nil, nil
	}
	return DiscoverOperatorIdentity(ctx, c, podName, podNamespace)
}
