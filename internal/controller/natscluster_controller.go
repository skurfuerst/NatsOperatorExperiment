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
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	natsv1alpha1 "github.com/skurfuerst/natsoperatorexperiment/api/v1alpha1"
	"github.com/skurfuerst/natsoperatorexperiment/internal/natsconfig"
	nkeysutil "github.com/skurfuerst/natsoperatorexperiment/internal/nkeys"
)

// NatsClusterReconciler reconciles a NatsCluster object
type NatsClusterReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	PodReloader PodReloader
}

// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsclusters,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsusers,verbs=get;list;watch
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list
// +kubebuilder:rbac:groups="",resources=pods/exec,verbs=create

func (r *NatsClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. Fetch NatsCluster
	cluster := &natsv1alpha1.NatsCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 2. List NatsAccounts in same namespace referencing this cluster
	accountList := &natsv1alpha1.NatsAccountList{}
	if err := r.List(ctx, accountList, client.InNamespace(cluster.Namespace)); err != nil {
		r.setClusterCondition(ctx, cluster, metav1.ConditionFalse, natsv1alpha1.ReasonReconcileError, err.Error())
		return ctrl.Result{}, err
	}

	var matchingAccounts []natsv1alpha1.NatsAccount
	for _, acct := range accountList.Items {
		if acct.Spec.ClusterRef.Name == cluster.Name {
			matchingAccounts = append(matchingAccounts, acct)
		}
	}

	// Sort accounts by name for deterministic output
	sort.Slice(matchingAccounts, func(i, j int) bool {
		return matchingAccounts[i].Name < matchingAccounts[j].Name
	})

	// 3. For each account, find users and ensure NKey secrets
	accountsWithUsers := make([]natsconfig.AccountWithUsers, 0, len(matchingAccounts))
	totalUsers := 0

	for i := range matchingAccounts {
		acct := &matchingAccounts[i]

		// Compile allowed namespace regexes
		regexes, err := compileNamespaceRegexes(acct.Spec.AllowedUserNamespaces)
		if err != nil {
			log.Error(err, "failed to compile allowedUserNamespaces regex", "account", acct.Name)
			r.setAccountCondition(ctx, acct, metav1.ConditionFalse, natsv1alpha1.ReasonInvalidRegex, err.Error())
			continue
		}

		// List all NatsUsers across all namespaces
		userList := &natsv1alpha1.NatsUserList{}
		if err := r.List(ctx, userList); err != nil {
			r.setClusterCondition(ctx, cluster, metav1.ConditionFalse, natsv1alpha1.ReasonReconcileError, err.Error())
			return ctrl.Result{}, err
		}

		var usersWithKeys []natsconfig.UserWithPublicKey
		for j := range userList.Items {
			user := &userList.Items[j]

			// Check if this user references this account
			accountNs := user.Spec.AccountRef.Namespace
			if accountNs == "" {
				accountNs = user.Namespace
			}
			if user.Spec.AccountRef.Name != acct.Name || accountNs != acct.Namespace {
				continue
			}

			// Validate user namespace against account's allowedUserNamespaces
			if user.Namespace != acct.Namespace && !isNamespaceAllowed(user.Namespace, regexes) {
				log.Info("user namespace not allowed", "user", user.Name, "userNamespace", user.Namespace, "account", acct.Name)
				r.setUserCondition(ctx, user, metav1.ConditionFalse, natsv1alpha1.ReasonNamespaceNotAllowed,
					fmt.Sprintf("namespace %q not allowed by account %q", user.Namespace, acct.Name))
				continue
			}

			// Ensure NKey secret exists
			publicKey, err := r.ensureNKeySecret(ctx, user)
			if err != nil {
				log.Error(err, "failed to ensure NKey secret", "user", user.Name)
				return ctrl.Result{}, err
			}

			// Set user Ready condition
			r.setUserCondition(ctx, user, metav1.ConditionTrue, natsv1alpha1.ReasonReconciled, "User reconciled successfully")

			usersWithKeys = append(usersWithKeys, natsconfig.UserWithPublicKey{
				User:      *user,
				PublicKey: publicKey,
			})
		}

		// Sort users by name for deterministic output
		sort.Slice(usersWithKeys, func(i, j int) bool {
			return usersWithKeys[i].User.Name < usersWithKeys[j].User.Name
		})

		// Update account status
		acct.Status.UserCount = len(usersWithKeys)
		r.setAccountCondition(ctx, acct, metav1.ConditionTrue, natsv1alpha1.ReasonReconciled, "Account reconciled successfully")

		totalUsers += len(usersWithKeys)
		accountsWithUsers = append(accountsWithUsers, natsconfig.AccountWithUsers{
			Account: *acct,
			Users:   usersWithKeys,
		})
	}

	// 4. Generate NATS config
	cfg := natsconfig.ConvertToNatsConfig(accountsWithUsers)
	configStr := natsconfig.Generate(cfg)

	// 5. Compute hash
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(configStr)))

	// 6. Create/update ConfigMap
	configMapName := cluster.Name + "-nats-config"
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: cluster.Namespace,
		},
	}

	result, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Data = map[string]string{
			"auth.conf": configStr,
		}
		return controllerutil.SetControllerReference(cluster, cm, r.Scheme)
	})
	if err != nil {
		r.setClusterCondition(ctx, cluster, metav1.ConditionFalse, natsv1alpha1.ReasonReconcileError, err.Error())
		return ctrl.Result{}, err
	}
	if result != controllerutil.OperationResultNone {
		log.Info("ConfigMap updated", "name", configMapName, "operation", result)
	}

	// 7. Send SIGHUP to NATS pods if config changed
	if hash != cluster.Status.LastConfigHash && cluster.Spec.ServerRef != nil {
		if err := r.reloadNatsPods(ctx, cluster); err != nil {
			log.Error(err, "failed to reload NATS pods")
			r.setClusterCondition(ctx, cluster, metav1.ConditionFalse, natsv1alpha1.ReasonReconcileError,
				fmt.Sprintf("failed to reload NATS pods: %v", err))
			return ctrl.Result{}, err
		}
		log.Info("sent SIGHUP to NATS pods for config reload")
	}

	// 8. Update cluster status
	cluster.Status.AccountCount = len(matchingAccounts)
	cluster.Status.UserCount = totalUsers
	cluster.Status.LastConfigHash = hash
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               natsv1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             natsv1alpha1.ReasonReconciled,
		Message:            fmt.Sprintf("Reconciled %d accounts, %d users", len(matchingAccounts), totalUsers),
		ObservedGeneration: cluster.Generation,
	})
	if err := r.Status().Update(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reloadNatsPods sends SIGHUP to all Running pods of the referenced workload,
// triggering the NATS server to reload its configuration without restarting.
func (r *NatsClusterReconciler) reloadNatsPods(ctx context.Context, cluster *natsv1alpha1.NatsCluster) error {
	log := logf.FromContext(ctx)
	ref := cluster.Spec.ServerRef
	key := types.NamespacedName{Name: ref.Name, Namespace: cluster.Namespace}

	var podSelector *metav1.LabelSelector
	switch ref.Kind {
	case "Deployment":
		deploy := &appsv1.Deployment{}
		if err := r.Get(ctx, key, deploy); err != nil {
			return fmt.Errorf("getting deployment %s: %w", key, err)
		}
		podSelector = deploy.Spec.Selector
	case "StatefulSet":
		sts := &appsv1.StatefulSet{}
		if err := r.Get(ctx, key, sts); err != nil {
			return fmt.Errorf("getting statefulset %s: %w", key, err)
		}
		podSelector = sts.Spec.Selector
	default:
		return fmt.Errorf("unsupported workload kind: %s", ref.Kind)
	}

	selector, err := metav1.LabelSelectorAsSelector(podSelector)
	if err != nil {
		return fmt.Errorf("parsing label selector: %w", err)
	}

	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(cluster.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return fmt.Errorf("listing pods: %w", err)
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase != corev1.PodRunning {
			log.Info("skipping non-running pod", "pod", pod.Name, "phase", pod.Status.Phase)
			continue
		}
		if err := r.PodReloader.ReloadPod(ctx, cluster.Namespace, pod.Name); err != nil {
			return fmt.Errorf("reloading pod %s: %w", pod.Name, err)
		}
		log.Info("sent SIGHUP to pod", "pod", pod.Name)
	}
	return nil
}

// ensureNKeySecret ensures a Secret with NKey seed/public key exists for the user.
// Returns the public key.
func (r *NatsClusterReconciler) ensureNKeySecret(ctx context.Context, user *natsv1alpha1.NatsUser) (string, error) {
	secretName := user.Name + "-nats-nkey"
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: user.Namespace}, secret)

	if err == nil {
		// Secret exists, return public key
		publicKey := string(secret.Data["nkey-public"])

		// Update user status if needed
		if user.Status.NKeyPublicKey != publicKey || user.Status.SecretRef == nil || user.Status.SecretRef.Name != secretName {
			user.Status.NKeyPublicKey = publicKey
			user.Status.SecretRef = &natsv1alpha1.SecretReference{Name: secretName}
			if err := r.Status().Update(ctx, user); err != nil {
				return "", err
			}
		}

		return publicKey, nil
	}

	if !errors.IsNotFound(err) {
		return "", err
	}

	// Generate new NKey
	publicKey, seed, err := nkeysutil.GenerateUserNKey()
	if err != nil {
		return "", fmt.Errorf("generating nkey: %w", err)
	}

	// Create Secret
	secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: user.Namespace,
		},
		Data: map[string][]byte{
			"nkey-seed":   seed,
			"nkey-public": []byte(publicKey),
		},
	}

	if err := controllerutil.SetOwnerReference(user, secret, r.Scheme); err != nil {
		return "", err
	}

	if err := r.Create(ctx, secret); err != nil {
		return "", err
	}

	// Update user status
	user.Status.NKeyPublicKey = publicKey
	user.Status.SecretRef = &natsv1alpha1.SecretReference{Name: secretName}
	if err := r.Status().Update(ctx, user); err != nil {
		return "", err
	}

	return publicKey, nil
}

//nolint:unparam // status kept as parameter for consistency with setAccountCondition/setUserCondition
func (r *NatsClusterReconciler) setClusterCondition(ctx context.Context, cluster *natsv1alpha1.NatsCluster, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               natsv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cluster.Generation,
	})
	if err := r.Status().Update(ctx, cluster); err != nil {
		logf.FromContext(ctx).Error(err, "failed to update cluster condition")
	}
}

func (r *NatsClusterReconciler) setAccountCondition(ctx context.Context, account *natsv1alpha1.NatsAccount, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&account.Status.Conditions, metav1.Condition{
		Type:               natsv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: account.Generation,
	})
	if err := r.Status().Update(ctx, account); err != nil {
		logf.FromContext(ctx).Error(err, "failed to update account condition")
	}
}

func (r *NatsClusterReconciler) setUserCondition(ctx context.Context, user *natsv1alpha1.NatsUser, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&user.Status.Conditions, metav1.Condition{
		Type:               natsv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: user.Generation,
	})
	if err := r.Status().Update(ctx, user); err != nil {
		logf.FromContext(ctx).Error(err, "failed to update user condition")
	}
}

func compileNamespaceRegexes(patterns []string) ([]*regexp.Regexp, error) {
	regexes := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid regex %q: %w", p, err)
		}
		regexes = append(regexes, re)
	}
	return regexes, nil
}

func isNamespaceAllowed(namespace string, regexes []*regexp.Regexp) bool {
	for _, re := range regexes {
		if re.MatchString(namespace) {
			return true
		}
	}
	return false
}

// mapAccountToCluster maps a NatsAccount change to the NatsCluster it references.
func (r *NatsClusterReconciler) mapAccountToCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	account, ok := obj.(*natsv1alpha1.NatsAccount)
	if !ok {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{
			Name:      account.Spec.ClusterRef.Name,
			Namespace: account.Namespace,
		}},
	}
}

// mapUserToCluster maps a NatsUser change to the NatsCluster via NatsAccount.
func (r *NatsClusterReconciler) mapUserToCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	user, ok := obj.(*natsv1alpha1.NatsUser)
	if !ok {
		return nil
	}

	// Resolve the account
	accountNs := user.Spec.AccountRef.Namespace
	if accountNs == "" {
		accountNs = user.Namespace
	}

	account := &natsv1alpha1.NatsAccount{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      user.Spec.AccountRef.Name,
		Namespace: accountNs,
	}, account); err != nil {
		return nil
	}

	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{
			Name:      account.Spec.ClusterRef.Name,
			Namespace: account.Namespace,
		}},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *NatsClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&natsv1alpha1.NatsCluster{}).
		Watches(&natsv1alpha1.NatsAccount{},
			handler.EnqueueRequestsFromMapFunc(r.mapAccountToCluster)).
		Watches(&natsv1alpha1.NatsUser{},
			handler.EnqueueRequestsFromMapFunc(r.mapUserToCluster)).
		Owns(&corev1.ConfigMap{}).
		Named("natscluster").
		Complete(r)
}
