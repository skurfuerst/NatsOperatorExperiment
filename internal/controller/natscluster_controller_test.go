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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
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
		It("should create an empty ConfigMap with Ready condition", func() {
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

			// Verify status and Ready condition
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.AccountCount).To(Equal(0))
			Expect(cluster.Status.UserCount).To(Equal(0))
			Expect(cluster.Status.LastConfigHash).NotTo(BeEmpty())

			readyCond := meta.FindStatusCondition(cluster.Status.Conditions, natsv1alpha1.ConditionReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal(natsv1alpha1.ReasonReconciled))

			// Cleanup
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})

	Context("NatsCluster with NatsAccount", func() {
		It("should include account config with Ready conditions and account status", func() {
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

			// Verify cluster status
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.AccountCount).To(Equal(1))

			// Verify account Ready condition and userCount
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: accountName, Namespace: clusterNs,
			}, account)).To(Succeed())
			acctReady := meta.FindStatusCondition(account.Status.Conditions, natsv1alpha1.ConditionReady)
			Expect(acctReady).NotTo(BeNil())
			Expect(acctReady.Status).To(Equal(metav1.ConditionTrue))
			Expect(account.Status.UserCount).To(Equal(0))

			// Cleanup
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})

	Context("NatsCluster with NatsAccount and NatsUser (same namespace)", func() {
		It("should create NKey secret, include user in config, set conditions", func() {
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

			// Verify NKey Secret
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

			// Verify user status and condition
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: userName, Namespace: clusterNs,
			}, user)).To(Succeed())
			Expect(user.Status.NKeyPublicKey).To(Equal(publicKey))
			Expect(user.Status.SecretRef).NotTo(BeNil())
			Expect(user.Status.SecretRef.Name).To(Equal(userName + "-nats-nkey"))

			userReady := meta.FindStatusCondition(user.Status.Conditions, natsv1alpha1.ConditionReady)
			Expect(userReady).NotTo(BeNil())
			Expect(userReady.Status).To(Equal(metav1.ConditionTrue))

			// Verify account userCount
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: accountName, Namespace: clusterNs,
			}, account)).To(Succeed())
			Expect(account.Status.UserCount).To(Equal(1))

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
			teamNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "team-alpha"},
			}
			Expect(k8sClient.Create(ctx, teamNs)).To(Or(Succeed(), MatchError(ContainSubstring("already exists"))))

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

			// Verify user Ready condition
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "cross-ns-user", Namespace: "team-alpha",
			}, crossNsUser)).To(Succeed())
			userReady := meta.FindStatusCondition(crossNsUser.Status.Conditions, natsv1alpha1.ConditionReady)
			Expect(userReady).NotTo(BeNil())
			Expect(userReady.Status).To(Equal(metav1.ConditionTrue))

			// Cleanup
			Expect(k8sClient.Delete(ctx, crossNsUser)).To(Succeed())
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})

		It("should reject user from namespace not matching allowedUserNamespaces with condition", func() {
			rejectedNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "unauthorized-ns"},
			}
			Expect(k8sClient.Create(ctx, rejectedNs)).To(Or(Succeed(), MatchError(ContainSubstring("already exists"))))

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

			// Verify no NKey secret
			secret := &corev1.Secret{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "rejected-user-nats-nkey", Namespace: "unauthorized-ns",
			}, secret)
			Expect(err).To(HaveOccurred())

			// Verify user has NamespaceNotAllowed condition
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "rejected-user", Namespace: "unauthorized-ns",
			}, rejectedUser)).To(Succeed())
			userReady := meta.FindStatusCondition(rejectedUser.Status.Conditions, natsv1alpha1.ConditionReady)
			Expect(userReady).NotTo(BeNil())
			Expect(userReady.Status).To(Equal(metav1.ConditionFalse))
			Expect(userReady.Reason).To(Equal(natsv1alpha1.ReasonNamespaceNotAllowed))

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

			secret1 := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: userName + "-nats-nkey", Namespace: clusterNs,
			}, secret1)).To(Succeed())
			originalSeed := string(secret1.Data["nkey-seed"])
			originalPublicKey := string(secret1.Data["nkey-public"])

			// Reconcile again
			doReconcile()

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

	Context("Delete reconciliation", func() {
		It("should regenerate config when account is deleted", func() {
			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			account1 := &natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "account-1",
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsAccountSpec{
					ClusterRef: natsv1alpha1.LocalObjectReference{Name: clusterName},
				},
			}
			account2 := &natsv1alpha1.NatsAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "account-2",
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsAccountSpec{
					ClusterRef: natsv1alpha1.LocalObjectReference{Name: clusterName},
				},
			}
			Expect(k8sClient.Create(ctx, account1)).To(Succeed())
			Expect(k8sClient.Create(ctx, account2)).To(Succeed())

			doReconcile()

			// Verify both accounts in config
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())
			Expect(cm.Data["auth.conf"]).To(ContainSubstring("account-1"))
			Expect(cm.Data["auth.conf"]).To(ContainSubstring("account-2"))

			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.AccountCount).To(Equal(2))

			// Delete account-1
			Expect(k8sClient.Delete(ctx, account1)).To(Succeed())

			doReconcile()

			// Verify config only has account-2
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())
			Expect(cm.Data["auth.conf"]).NotTo(ContainSubstring("account-1"))
			Expect(cm.Data["auth.conf"]).To(ContainSubstring("account-2"))

			// Verify cluster status updated
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.AccountCount).To(Equal(1))

			// Cleanup
			Expect(k8sClient.Delete(ctx, account2)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})

		It("should regenerate config when user is deleted", func() {
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

			user1 := &natsv1alpha1.NatsUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-1",
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsUserSpec{
					AccountRef: natsv1alpha1.NamespacedObjectReference{Name: accountName},
				},
			}
			user2 := &natsv1alpha1.NatsUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-2",
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsUserSpec{
					AccountRef: natsv1alpha1.NamespacedObjectReference{Name: accountName},
				},
			}
			Expect(k8sClient.Create(ctx, user1)).To(Succeed())
			Expect(k8sClient.Create(ctx, user2)).To(Succeed())

			doReconcile()

			// Get user-1's public key
			secret1 := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "user-1-nats-nkey", Namespace: clusterNs,
			}, secret1)).To(Succeed())
			user1Key := string(secret1.Data["nkey-public"])

			// Verify both users in config
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())
			Expect(cm.Data["auth.conf"]).To(ContainSubstring(user1Key))

			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.UserCount).To(Equal(2))

			// Delete user-1
			Expect(k8sClient.Delete(ctx, user1)).To(Succeed())

			doReconcile()

			// Verify config no longer has user-1's key
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())
			Expect(cm.Data["auth.conf"]).NotTo(ContainSubstring(user1Key))

			// Verify cluster status updated
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			Expect(cluster.Status.UserCount).To(Equal(1))

			// Verify account userCount updated
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: accountName, Namespace: clusterNs,
			}, account)).To(Succeed())
			Expect(account.Status.UserCount).To(Equal(1))

			// Cleanup
			Expect(k8sClient.Delete(ctx, user2)).To(Succeed())
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})

	Context("Reload mechanism", func() {
		It("should annotate Deployment when serverRef is set and config changes", func() {
			// Create a Deployment to reference
			deploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nats-server",
					Namespace: clusterNs,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "nats"},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "nats"},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: "nats", Image: "nats:latest"},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, deploy)).To(Succeed())

			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsClusterSpec{
					ServerRef: &natsv1alpha1.WorkloadReference{
						Kind: "Deployment",
						Name: "nats-server",
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			doReconcile()

			// Verify Deployment pod template has config-hash annotation
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "nats-server", Namespace: clusterNs,
			}, deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Annotations).To(HaveKey("nats.k8s.sandstorm.de/config-hash"))
			firstHash := deploy.Spec.Template.Annotations["nats.k8s.sandstorm.de/config-hash"]
			Expect(firstHash).NotTo(BeEmpty())

			// Add an account to change config
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

			doReconcile()

			// Verify hash changed
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "nats-server", Namespace: clusterNs,
			}, deploy)).To(Succeed())
			secondHash := deploy.Spec.Template.Annotations["nats.k8s.sandstorm.de/config-hash"]
			Expect(secondHash).NotTo(Equal(firstHash))

			// Cleanup
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Delete(ctx, deploy)).To(Succeed())
		})

		It("should not fail when serverRef is nil", func() {
			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			doReconcile()

			// Should succeed without error
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: clusterNs,
			}, cluster)).To(Succeed())
			readyCond := meta.FindStatusCondition(cluster.Status.Conditions, natsv1alpha1.ConditionReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))

			// Cleanup
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})

		It("should annotate StatefulSet when serverRef kind is StatefulSet", func() {
			sts := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nats-sts",
					Namespace: clusterNs,
				},
				Spec: appsv1.StatefulSetSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "nats"},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "nats"},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: "nats", Image: "nats:latest"},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, sts)).To(Succeed())

			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsClusterSpec{
					ServerRef: &natsv1alpha1.WorkloadReference{
						Kind: "StatefulSet",
						Name: "nats-sts",
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			doReconcile()

			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: "nats-sts", Namespace: clusterNs,
			}, sts)).To(Succeed())
			Expect(sts.Spec.Template.Annotations).To(HaveKey("nats.k8s.sandstorm.de/config-hash"))

			// Cleanup
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sts)).To(Succeed())
		})

		It("should set error condition when referenced workload does not exist", func() {
			cluster := &natsv1alpha1.NatsCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
				Spec: natsv1alpha1.NatsClusterSpec{
					ServerRef: &natsv1alpha1.WorkloadReference{
						Kind: "Deployment",
						Name: "nonexistent",
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			r := reconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: clusterName, Namespace: clusterNs},
			})
			Expect(err).To(HaveOccurred())

			// Cleanup
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})

	Context("Invalid regex on account", func() {
		It("should set InvalidRegex condition on account and skip it", func() {
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
					AllowedUserNamespaces: []string{"[invalid-regex"},
				},
			}
			Expect(k8sClient.Create(ctx, account)).To(Succeed())

			doReconcile()

			// Verify account has InvalidRegex condition
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: accountName, Namespace: clusterNs,
			}, account)).To(Succeed())
			acctReady := meta.FindStatusCondition(account.Status.Conditions, natsv1alpha1.ConditionReady)
			Expect(acctReady).NotTo(BeNil())
			Expect(acctReady.Status).To(Equal(metav1.ConditionFalse))
			Expect(acctReady.Reason).To(Equal(natsv1alpha1.ReasonInvalidRegex))

			// ConfigMap should still exist but without that account
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: configMapName, Namespace: clusterNs,
			}, cm)).To(Succeed())
			Expect(cm.Data["auth.conf"]).NotTo(ContainSubstring(accountName))

			// Cleanup
			Expect(k8sClient.Delete(ctx, account)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})
})
