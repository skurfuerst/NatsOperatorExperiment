package controller

import (
	"context"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	natsv1alpha1 "github.com/sandstorm/NatsAuthOperator/api/v1alpha1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(natsv1alpha1.AddToScheme(s))
	return s
}

func TestMapUserToCluster_Success(t *testing.T) {
	scheme := testScheme()
	account := &natsv1alpha1.NatsAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "my-account", Namespace: "nats"},
		Spec:       natsv1alpha1.NatsAccountSpec{ClusterRef: natsv1alpha1.LocalObjectReference{Name: "my-cluster"}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(account).Build()
	r := &NatsClusterReconciler{Client: c, Scheme: scheme}

	user := &natsv1alpha1.NatsUser{
		ObjectMeta: metav1.ObjectMeta{Name: "my-user", Namespace: "nats"},
		Spec: natsv1alpha1.NatsUserSpec{
			AccountRef: natsv1alpha1.NamespacedObjectReference{Name: "my-account"},
		},
	}

	requests := r.mapUserToCluster(context.Background(), user)
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	if requests[0].Name != "my-cluster" || requests[0].Namespace != "nats" {
		t.Errorf("expected my-cluster/nats, got %s/%s", requests[0].Name, requests[0].Namespace)
	}
}

func TestMapUserToCluster_AccountNotFound_ReturnsNil(t *testing.T) {
	scheme := testScheme()
	// No account in the fake client
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &NatsClusterReconciler{Client: c, Scheme: scheme}

	user := &natsv1alpha1.NatsUser{
		ObjectMeta: metav1.ObjectMeta{Name: "my-user", Namespace: "nats"},
		Spec: natsv1alpha1.NatsUserSpec{
			AccountRef: natsv1alpha1.NamespacedObjectReference{Name: "nonexistent"},
		},
	}

	requests := r.mapUserToCluster(context.Background(), user)
	if len(requests) != 0 {
		t.Errorf("expected 0 requests for NotFound account, got %d", len(requests))
	}
}

func TestMapUserToCluster_TransientError_FallsBackToNamespace(t *testing.T) {
	scheme := testScheme()

	// A cluster exists in the namespace — the fallback should find it
	cluster := &natsv1alpha1.NatsCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: "nats"},
	}

	// Intercepting client: Get fails with transient error, List works
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*natsv1alpha1.NatsAccount); ok {
					return fmt.Errorf("connection refused")
				}
				return client.Get(ctx, key, obj, opts...)
			},
		}).Build()
	r := &NatsClusterReconciler{Client: c, Scheme: scheme}

	user := &natsv1alpha1.NatsUser{
		ObjectMeta: metav1.ObjectMeta{Name: "my-user", Namespace: "nats"},
		Spec: natsv1alpha1.NatsUserSpec{
			AccountRef: natsv1alpha1.NamespacedObjectReference{Name: "my-account"},
		},
	}

	requests := r.mapUserToCluster(context.Background(), user)
	if len(requests) != 1 {
		t.Fatalf("expected 1 fallback request, got %d", len(requests))
	}
	if requests[0].Name != "my-cluster" || requests[0].Namespace != "nats" {
		t.Errorf("expected my-cluster/nats from fallback, got %s/%s", requests[0].Name, requests[0].Namespace)
	}
}

func TestMapUserToCluster_TransientError_CrossNamespace(t *testing.T) {
	scheme := testScheme()

	cluster := &natsv1alpha1.NatsCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "central-cluster", Namespace: "nats-system"},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*natsv1alpha1.NatsAccount); ok {
					return fmt.Errorf("i/o timeout")
				}
				return client.Get(ctx, key, obj, opts...)
			},
		}).Build()
	r := &NatsClusterReconciler{Client: c, Scheme: scheme}

	user := &natsv1alpha1.NatsUser{
		ObjectMeta: metav1.ObjectMeta{Name: "cross-user", Namespace: "team-alpha"},
		Spec: natsv1alpha1.NatsUserSpec{
			AccountRef: natsv1alpha1.NamespacedObjectReference{
				Name:      "my-account",
				Namespace: "nats-system",
			},
		},
	}

	requests := r.mapUserToCluster(context.Background(), user)
	if len(requests) != 1 {
		t.Fatalf("expected 1 fallback request for cross-ns, got %d", len(requests))
	}
	if requests[0].Name != "central-cluster" || requests[0].Namespace != "nats-system" {
		t.Errorf("expected central-cluster/nats-system, got %s/%s", requests[0].Name, requests[0].Namespace)
	}
}

func TestMapUserToCluster_TransientError_NoClusters(t *testing.T) {
	scheme := testScheme()

	// No clusters in namespace — fallback finds nothing
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*natsv1alpha1.NatsAccount); ok {
					return fmt.Errorf("connection reset")
				}
				return client.Get(ctx, key, obj, opts...)
			},
		}).Build()
	r := &NatsClusterReconciler{Client: c, Scheme: scheme}

	user := &natsv1alpha1.NatsUser{
		ObjectMeta: metav1.ObjectMeta{Name: "my-user", Namespace: "nats"},
		Spec: natsv1alpha1.NatsUserSpec{
			AccountRef: natsv1alpha1.NamespacedObjectReference{Name: "my-account"},
		},
	}

	requests := r.mapUserToCluster(context.Background(), user)
	if len(requests) != 0 {
		t.Errorf("expected 0 requests when no clusters exist, got %d", len(requests))
	}
}
