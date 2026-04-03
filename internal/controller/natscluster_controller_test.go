/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	natsv1alpha1 "github.com/skurfuerst/natsoperatorexperiment/api/v1alpha1"
)

var _ = Describe("NatsCluster Controller", func() {
	const (
		clusterName   = "test-cluster"
		clusterNs     = "default"
		accountName   = "test-account"
		userName      = "test-user"
		configMapName = "test-cluster-nats-config"
	)

	ctx := context.Background()

	reconciler := func() *NatsClusterReconciler {
		return &NatsClusterReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	}

	doReconcile := func() {
		r := reconciler()
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: clusterName, Namespace: clusterNs},
		})
		Expect(err).NotTo(HaveOccurred())
	}

	Context("NatsCluster alone", func() {
		It("should create an empty ConfigMap", func() {
			// Create NatsCluster
			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			doReconcile()

			// Verify ConfigMap exists
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())

			Expect(cm.Data).To(HaveKey("auth.conf"))
			Expect(cm.Data["auth.conf"]).To(ContainSubstring("accounts {"))

			// Verify status
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.AccountCount).To(Equal(0))
			Expect(cluster.Status.UserCount).To(Equal(0))
			Expect(cluster.Status.LastConfigHash).NotTo(BeEmpty())

			// Cleanup
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})

	Context("NatsCluster with NatsAccount", func() {
		It("should include account config in ConfigMap", func() {
			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			maxStreams := int64(10)
			maxConsumers := int64(100)
			mem := resource.MustParse("512Mi")
			file := resource.MustParse("1Gi")
			maxConn := int64(500)

			account := &natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      accountName,
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsAccountSpec{
					ClusterRef: natsv1alpha1.LocalObjectReference{Name: clusterName},
					JetStream: &natsv1alpha1.AccountJetStream{
						MaxMemory:    &mem,
						MaxFile:      &file,
						MaxStreams:   &maxStreams,
						MaxConsumers: &maxConsumers,
					},
					Limits: &natsv1alpha1.AccountLimits{
						MaxConnections: &maxConn,
					},
				},
			}
			Expect(k8sClient.Create(ctx, account)).To(Succeed())

			doReconcile()

			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())

			conf := cm.Data["auth.conf"]
			Expect(conf).To(ContainSubstring(accountName))
			Expect(conf).To(ContainSubstring("jetstream"))
			Expect(conf).To(ContainSubstring("max_mem: 512MI"))
			Expect(conf).To(ContainSubstring("max_file: 1GI"))
			Expect(conf).To(ContainSubstring("max_streams: 10"))
			Expect(conf).To(ContainSubstring("max_connections: 500"))

			// Verify status
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.AccountCount).To(Equal(1))

			// Cleanup
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})

	Context("NatsCluster with NatsAccount and NatsUser (same namespace)", func() {
		It("should create NKey secret and include user in config", func() {
			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			account := &natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      accountName,
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsAccountSpec{
					ClusterRef: natsv1alpha1.LocalObjectReference{Name: clusterName},
				},
			}
			Expect(k8sClient.Create(ctx, account)).To(Succeed())

			user := &natsv1alpha1.NatsUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      userName,
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsUserSpec{
					AccountRef: natsv1alpha1.NamespacedObjectReference{
						Name: accountName,
					},
					Permissions: &natsv1alpha1.Permissions{
						Publish: &natsv1alpha1.PermissionRule{
							Allow: []string{"events.>"},
						},
						Subscribe: &natsv1alpha1.PermissionRule{
							Allow: []string{"responses.>"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, user)).To(Succeed())

			doReconcile()

			// Verify NKey Secret was created
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: userName + "-nats-nkey", Namespace: clusterNs,
			}, secret)).To(Succeed())
			Expect(secret.Data).To(HaveKey("nkey-seed"))
			Expect(secret.Data).To(HaveKey("nkey-public"))
			publicKey := string(secret.Data["nkey-public"])
			Expect(publicKey).To(HavePrefix("U"))

			// Verify ConfigMap includes user
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())
			conf := cm.Data["auth.conf"]
			Expect(conf).To(ContainSubstring(publicKey))
			Expect(conf).To(ContainSubstring(`"events.>"`))
			Expect(conf).To(ContainSubstring(`"responses.>"`))

			// Verify user status updated
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: userName, Namespace: clusterNs,
			}, user)).To(Succeed())
			Expect(user.Status.NKeyPublicKey).To(Equal(publicKey))
			Expect(user.Status.SecretRef).NotTo(BeNil())
			Expect(user.Status.SecretRef.Name).To(Equal(userName + "-nats-nkey"))

			// Verify cluster status
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.AccountCount).To(Equal(1))
			Expect(cluster.Status.UserCount).To(Equal(1))

			// Cleanup
			Expect(k8sClient.Delete(ctx, user)).To(Succeed())
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})

	Context("Cross-namespace NatsUser", func() {
		It("should allow user from namespace matching allowedUserNamespaces regex", func() {
			// Create namespace for cross-ns user
			teamNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "team-alpha"},
			}
			Expect(k8sClient.Create(ctx, teamNs)).To(Succeed())

			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			account := &natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      accountName,
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsAccountSpec{
					ClusterRef:            natsv1alpha1.LocalObjectReference{Name: clusterName},
					AllowedUserNamespaces: []string{"^team-.*$"},
				},
			}
			Expect(k8sClient.Create(ctx, account)).To(Succeed())

			crossNsUser := &natsv1alpha1.NatsUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cross-ns-user",
					Namespace: "team-alpha",
				},
				Spec: natsv1alpha1.NatsUserSpec{
					AccountRef: natsv1alpha1.NamespacedObjectReference{
						Name:      accountName,
						Namespace: clusterNs,
					},
				},
			}
			Expect(k8sClient.Create(ctx, crossNsUser)).To(Succeed())

			doReconcile()

			// Verify NKey secret created in user's namespace
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "cross-ns-user-nats-nkey", Namespace: "team-alpha",
			}, secret)).To(Succeed())

			// Verify config includes user
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())
			publicKey := string(secret.Data["nkey-public"])
			Expect(cm.Data["auth.conf"]).To(ContainSubstring(publicKey))

			// Verify cluster status
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.UserCount).To(Equal(1))

			// Cleanup
			Expect(k8sClient.Delete(ctx, crossNsUser)).To(Succeed())
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})

		It("should reject user from namespace not matching allowedUserNamespaces", func() {
			// Create namespace
			rejectedNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "unauthorized-ns"},
			}
			Expect(k8sClient.Create(ctx, rejectedNs)).To(Succeed())

			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			account := &natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      accountName,
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsAccountSpec{
					ClusterRef:            natsv1alpha1.LocalObjectReference{Name: clusterName},
					AllowedUserNamespaces: []string{"^team-.*$"},
				},
			}
			Expect(k8sClient.Create(ctx, account)).To(Succeed())

			rejectedUser := &natsv1alpha1.NatsUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "rejected-user",
					Namespace: "unauthorized-ns",
				},
				Spec: natsv1alpha1.NatsUserSpec{
					AccountRef: natsv1alpha1.NamespacedObjectReference{
						Name:      accountName,
						Namespace: clusterNs,
					},
				},
			}
			Expect(k8sClient.Create(ctx, rejectedUser)).To(Succeed())

			doReconcile()

			// Verify no NKey secret was created
			secret := &corev1.Secret{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "rejected-user-nats-nkey", Namespace: "unauthorized-ns",
			}, secret)
			Expect(err).To(HaveOccurred())

			// Verify config does NOT include rejected user
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())
			Expect(cm.Data["auth.conf"]).NotTo(ContainSubstring("rejected-user"))

			// Verify cluster status shows 0 users
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.UserCount).To(Equal(0))

			// Cleanup
			Expect(k8sClient.Delete(ctx, rejectedUser)).To(Succeed())
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})

	Context("Config update on account change", func() {
		It("should update ConfigMap when account limits change", func() {
			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			maxConn := int64(100)
			account := &natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      accountName,
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsAccountSpec{
					ClusterRef: natsv1alpha1.LocalObjectReference{Name: clusterName},
					Limits: &natsv1alpha1.AccountLimits{
						MaxConnections: &maxConn,
					},
				},
			}
			Expect(k8sClient.Create(ctx, account)).To(Succeed())

			doReconcile()

			// Verify initial config
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())
			Expect(cm.Data["auth.conf"]).To(ContainSubstring("max_connections: 100"))

			// Get initial hash
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			initialHash := cluster.Status.LastConfigHash

			// Update account limits
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: accountName, Namespace: clusterNs,
			}, account)).To(Succeed())
			newMaxConn := int64(200)
			account.Spec.Limits.MaxConnections = &newMaxConn
			Expect(k8sClient.Update(ctx, account)).To(Succeed())

			doReconcile()

			// Verify updated config
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())
			Expect(cm.Data["auth.conf"]).To(ContainSubstring("max_connections: 200"))

			// Verify hash changed
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.LastConfigHash).NotTo(Equal(initialHash))

			// Cleanup
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})

	Context("NKey secret idempotency", func() {
		It("should not regenerate NKey secret on re-reconcile", func() {
			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			account := &natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      accountName,
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsAccountSpec{
					ClusterRef: natsv1alpha1.LocalObjectReference{Name: clusterName},
				},
			}
			Expect(k8sClient.Create(ctx, account)).To(Succeed())

			user := &natsv1alpha1.NatsUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      userName,
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsUserSpec{
					AccountRef: natsv1alpha1.NamespacedObjectReference{Name: accountName},
				},
			}
			Expect(k8sClient.Create(ctx, user)).To(Succeed())

			doReconcile()

			// Get the secret after first reconcile
			secret1 := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: userName + "-nats-nkey", Namespace: clusterNs,
			}, secret1)).To(Succeed())
			originalSeed := string(secret1.Data["nkey-seed"])
			originalPublicKey := string(secret1.Data["nkey-public"])

			// Reconcile again
			doReconcile()

			// Verify secret is unchanged
			secret2 := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: userName + "-nats-nkey", Namespace: clusterNs,
			}, secret2)).To(Succeed())
			Expect(string(secret2.Data["nkey-seed"])).To(Equal(originalSeed))
			Expect(string(secret2.Data["nkey-public"])).To(Equal(originalPublicKey))

			// Cleanup
			Expect(k8sClient.Delete(ctx, user)).To(Succeed())
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})
})
