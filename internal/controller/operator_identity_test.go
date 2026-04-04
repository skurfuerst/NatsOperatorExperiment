package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDiscoverOperatorIdentity(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	tests := []struct {
		name           string
		podName        string
		podNamespace   string
		objects        []runtime.Object
		wantDeployment string
		wantNamespace  string
		wantErr        bool
	}{
		{
			name:         "successful discovery through Pod -> ReplicaSet -> Deployment",
			podName:      "operator-abc-12345",
			podNamespace: "nats-system",
			objects: []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-abc-12345",
						Namespace: "nats-system",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "ReplicaSet", Name: "operator-abc"},
						},
					},
				},
				&appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-abc",
						Namespace: "nats-system",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "Deployment", Name: "natsoperator-controller-manager"},
						},
					},
				},
			},
			wantDeployment: "natsoperator-controller-manager",
			wantNamespace:  "nats-system",
		},
		{
			name:         "pod not found",
			podName:      "nonexistent",
			podNamespace: "nats-system",
			objects:      []runtime.Object{},
			wantErr:      true,
		},
		{
			name:         "pod has no ReplicaSet owner",
			podName:      "standalone-pod",
			podNamespace: "nats-system",
			objects: []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "standalone-pod",
						Namespace: "nats-system",
					},
				},
			},
			wantErr: true,
		},
		{
			name:         "ReplicaSet has no Deployment owner",
			podName:      "operator-abc-12345",
			podNamespace: "nats-system",
			objects: []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-abc-12345",
						Namespace: "nats-system",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "ReplicaSet", Name: "operator-abc"},
						},
					},
				},
				&appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-abc",
						Namespace: "nats-system",
					},
				},
			},
			wantErr: true,
		},
		{
			name:         "pod owned by StatefulSet instead of ReplicaSet",
			podName:      "operator-0",
			podNamespace: "nats-system",
			objects: []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-0",
						Namespace: "nats-system",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "StatefulSet", Name: "operator"},
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tt.objects...).Build()

			identity, err := DiscoverOperatorIdentity(context.Background(), c, tt.podName, tt.podNamespace)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if identity.DeploymentName != tt.wantDeployment {
				t.Errorf("deployment name = %q, want %q", identity.DeploymentName, tt.wantDeployment)
			}
			if identity.Namespace != tt.wantNamespace {
				t.Errorf("namespace = %q, want %q", identity.Namespace, tt.wantNamespace)
			}
		})
	}
}
