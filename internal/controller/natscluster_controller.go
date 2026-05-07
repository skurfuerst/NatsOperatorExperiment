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
	"sort"
	"time"

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

	natsv1alpha1 "github.com/sandstorm/NatsAuthOperator/api/v1alpha1"
	"github.com/sandstorm/NatsAuthOperator/internal/natsconfig"
)

// NatsClusterReconciler reconciles a NatsCluster object
type NatsClusterReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	PodReloader PodReloader

	// OperatorNamespace and OperatorDeploymentName are discovered at startup
	// and used to build full kubectl exec debug commands. If empty, debug
	// commands fall back to bare nats-debug commands.
	OperatorNamespace      string
	OperatorDeploymentName string

	ruleEvaluator *UserRuleEvaluator
	nkeyManager   *NKeySecretManager
}

func (r *NatsClusterReconciler) getUserRuleEvaluator() *UserRuleEvaluator {
	if r.ruleEvaluator == nil {
		r.ruleEvaluator = &UserRuleEvaluator{
			NamespaceFetcher: &clientNamespaceFetcher{Reader: r.Client},
		}
	}
	return r.ruleEvaluator
}

func (r *NatsClusterReconciler) getNKeySecretManager() *NKeySecretManager {
	if r.nkeyManager == nil {
		r.nkeyManager = &NKeySecretManager{
			Client: r.Client,
			Scheme: r.Scheme,
		}
	}
	return r.nkeyManager
}

// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsclusters,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsusers,verbs=get;list;watch
// +kubebuilder:rbac:groups=nats.k8s.sandstorm.de,resources=natsusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;update
// apps/{deployments,statefulsets,replicasets} permissions live in a namespaced
// Role (config/rbac/role_namespaced.yaml) bound only in the operator namespace.
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list
// +kubebuilder:rbac:groups="",resources=pods/exec,verbs=create
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

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
	matchingAccounts, err := r.listMatchingAccounts(ctx, cluster)
	if err != nil {
		_ = r.setClusterCondition(ctx, cluster, metav1.ConditionFalse, natsv1alpha1.ReasonReconcileError, err.Error())
		return ctrl.Result{}, err
	}

	// 3. List all NatsUsers once (fixes O(N) per-account listing)
	allUsers, err := r.listAllUsers(ctx)
	if err != nil {
		_ = r.setClusterCondition(ctx, cluster, metav1.ConditionFalse, natsv1alpha1.ReasonReconcileError, err.Error())
		return ctrl.Result{}, err
	}

	// 4. For each account, match users and ensure NKey secrets
	accountsWithUsers, totalUsers, statusUpdateFailed, err := r.reconcileAccountsAndUsers(ctx, cluster, matchingAccounts, allUsers)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 5. Generate NATS config
	cfg := natsconfig.ConvertToNatsConfig(accountsWithUsers)
	configStr := natsconfig.Generate(cfg)

	// 6. Compute hash and create/update ConfigMap
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(configStr)))
	if err := r.ensureConfigMap(ctx, cluster, configStr); err != nil {
		_ = r.setClusterCondition(ctx, cluster, metav1.ConditionFalse, natsv1alpha1.ReasonReconcileError, err.Error())
		return ctrl.Result{}, err
	}

	// 7. Send SIGHUP to NATS pods if config changed
	if hash != cluster.Status.LastConfigHash && cluster.Spec.ServerRef != nil {
		if err := r.reloadNatsPods(ctx, cluster); err != nil {
			log.Error(err, "failed to reload NATS pods")
			_ = r.setClusterCondition(ctx, cluster, metav1.ConditionFalse, natsv1alpha1.ReasonReconcileError,
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

	// 9. If any status updates failed during reconciliation, requeue to retry them.
	// We use RequeueAfter instead of returning an error to avoid rate-limiting the workqueue.
	if statusUpdateFailed {
		log.Info("requeuing due to failed status updates on accounts/users")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// listMatchingAccounts returns accounts in the cluster's namespace that reference this cluster, sorted by name.
func (r *NatsClusterReconciler) listMatchingAccounts(ctx context.Context, cluster *natsv1alpha1.NatsCluster) ([]natsv1alpha1.NatsAccount, error) {
	accountList := &natsv1alpha1.NatsAccountList{}
	if err := r.List(ctx, accountList, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, err
	}

	var matching []natsv1alpha1.NatsAccount
	for _, acct := range accountList.Items {
		if acct.Spec.ClusterRef.Name == cluster.Name {
			matching = append(matching, acct)
		}
	}

	sort.Slice(matching, func(i, j int) bool {
		return matching[i].Name < matching[j].Name
	})
	return matching, nil
}

// listAllUsers returns all NatsUsers across all namespaces.
func (r *NatsClusterReconciler) listAllUsers(ctx context.Context) ([]natsv1alpha1.NatsUser, error) {
	userList := &natsv1alpha1.NatsUserList{}
	if err := r.List(ctx, userList); err != nil {
		return nil, err
	}
	return userList.Items, nil
}

// reconcileAccountsAndUsers processes each account: validates rules, matches users,
// ensures NKey secrets, and updates status conditions.
// Returns statusUpdateFailed=true if any account/user status update failed (caller should requeue).
func (r *NatsClusterReconciler) reconcileAccountsAndUsers(
	ctx context.Context,
	cluster *natsv1alpha1.NatsCluster,
	accounts []natsv1alpha1.NatsAccount,
	allUsers []natsv1alpha1.NatsUser,
) (accountsWithUsers []natsconfig.AccountWithUsers, totalUsers int, statusUpdateFailed bool, err error) {
	log := logf.FromContext(ctx)
	evaluator := r.getUserRuleEvaluator()
	nkeyMgr := r.getNKeySecretManager()

	accountsWithUsers = make([]natsconfig.AccountWithUsers, 0, len(accounts))

	for i := range accounts {
		acct := &accounts[i]

		// Pre-validate all regex rules for this account
		if err := evaluator.ValidateRegexRules(acct.Spec.UserRules); err != nil {
			log.Error(err, "invalid namespaceRegex in userRules", "account", acct.Name)
			if condErr := r.setAccountCondition(ctx, acct, metav1.ConditionFalse, natsv1alpha1.ReasonInvalidRegex, err.Error()); condErr != nil {
				statusUpdateFailed = true
			}
			continue
		}

		// Match users to this account from the pre-fetched list
		var usersWithKeys []natsconfig.UserWithPublicKey
		for j := range allUsers {
			user := &allUsers[j]

			if !r.userReferencesAccount(user, acct) {
				continue
			}

			// Evaluate user rules
			allowed, ruleErr := evaluator.Evaluate(ctx, acct.Spec.UserRules, user.Namespace, acct.Namespace)
			if ruleErr != nil {
				log.Error(ruleErr, "failed to evaluate user rules", "user", user.Name)
				if condErr := r.setUserCondition(ctx, user, metav1.ConditionFalse, natsv1alpha1.ReasonNamespaceFetchError,
					fmt.Sprintf("error evaluating user rules for namespace %q: %v", user.Namespace, ruleErr)); condErr != nil {
					statusUpdateFailed = true
				}
				continue
			}
			if !allowed {
				log.Info("user not allowed by rules", "user", user.Name, "userNamespace", user.Namespace, "account", acct.Name)
				if condErr := r.setUserCondition(ctx, user, metav1.ConditionFalse, natsv1alpha1.ReasonNamespaceNotAllowed,
					fmt.Sprintf("namespace %q not allowed by account %q userRules", user.Namespace, acct.Name)); condErr != nil {
					statusUpdateFailed = true
				}
				continue
			}

			// Ensure NKey secret exists
			publicKey, inboxPrefix, nkeyErr := nkeyMgr.EnsureNKeySecret(ctx, user)
			if nkeyErr != nil {
				log.Error(nkeyErr, "failed to ensure NKey secret", "user", user.Name)
				return nil, 0, statusUpdateFailed, nkeyErr
			}

			// Set debug command and user Ready condition
			if cluster.Spec.ServerRef != nil {
				user.Status.DebugCommand = r.buildDebugCommand(fmt.Sprintf("nats-debug user-connections --cluster %s --namespace %s --nkey %s", cluster.Name, cluster.Namespace, publicKey))
			}
			if condErr := r.setUserCondition(ctx, user, metav1.ConditionTrue, natsv1alpha1.ReasonReconciled, "User reconciled successfully"); condErr != nil {
				statusUpdateFailed = true
			}

			usersWithKeys = append(usersWithKeys, natsconfig.UserWithPublicKey{
				User:        *user,
				PublicKey:   publicKey,
				InboxPrefix: inboxPrefix,
			})
		}

		// Sort users by name for deterministic output
		sort.Slice(usersWithKeys, func(i, j int) bool {
			return usersWithKeys[i].User.Name < usersWithKeys[j].User.Name
		})

		// Update account status
		acct.Status.UserCount = len(usersWithKeys)
		if cluster.Spec.ServerRef != nil {
			acct.Status.DebugCommand = r.buildDebugCommand(fmt.Sprintf("nats-debug account-connections --cluster %s --namespace %s --account %s", cluster.Name, cluster.Namespace, acct.Name))
		}
		if condErr := r.setAccountCondition(ctx, acct, metav1.ConditionTrue, natsv1alpha1.ReasonReconciled, "Account reconciled successfully"); condErr != nil {
			statusUpdateFailed = true
		}

		totalUsers += len(usersWithKeys)
		accountsWithUsers = append(accountsWithUsers, natsconfig.AccountWithUsers{
			Account: *acct,
			Users:   usersWithKeys,
		})
	}

	return accountsWithUsers, totalUsers, statusUpdateFailed, nil
}

// userReferencesAccount checks if a user's accountRef points to the given account.
func (r *NatsClusterReconciler) userReferencesAccount(user *natsv1alpha1.NatsUser, acct *natsv1alpha1.NatsAccount) bool {
	accountNs := user.Spec.AccountRef.Namespace
	if accountNs == "" {
		accountNs = user.Namespace
	}
	return user.Spec.AccountRef.Name == acct.Name && accountNs == acct.Namespace
}

// ensureConfigMap creates or updates the NATS auth ConfigMap.
func (r *NatsClusterReconciler) ensureConfigMap(ctx context.Context, cluster *natsv1alpha1.NatsCluster, configStr string) error {
	log := logf.FromContext(ctx)
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
		return err
	}
	if result != controllerutil.OperationResultNone {
		log.Info("ConfigMap updated", "name", configMapName, "operation", result)
	}
	return nil
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

//nolint:unparam // status kept as parameter for consistency with setAccountCondition/setUserCondition
func (r *NatsClusterReconciler) setClusterCondition(ctx context.Context, cluster *natsv1alpha1.NatsCluster, status metav1.ConditionStatus, reason, message string) error {
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               natsv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cluster.Generation,
	})
	if err := r.Status().Update(ctx, cluster); err != nil {
		logf.FromContext(ctx).Error(err, "failed to update cluster condition")
		return err
	}
	return nil
}

func (r *NatsClusterReconciler) setAccountCondition(ctx context.Context, account *natsv1alpha1.NatsAccount, status metav1.ConditionStatus, reason, message string) error {
	meta.SetStatusCondition(&account.Status.Conditions, metav1.Condition{
		Type:               natsv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: account.Generation,
	})
	if err := r.Status().Update(ctx, account); err != nil {
		logf.FromContext(ctx).Error(err, "failed to update account condition")
		return err
	}
	return nil
}

func (r *NatsClusterReconciler) setUserCondition(ctx context.Context, user *natsv1alpha1.NatsUser, status metav1.ConditionStatus, reason, message string) error {
	meta.SetStatusCondition(&user.Status.Conditions, metav1.Condition{
		Type:               natsv1alpha1.ConditionReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: user.Generation,
	})
	if err := r.Status().Update(ctx, user); err != nil {
		logf.FromContext(ctx).Error(err, "failed to update user condition")
		return err
	}
	return nil
}

// buildDebugCommand wraps a bare nats-debug command with kubectl exec prefix
// if the operator's own deployment identity is known.
func (r *NatsClusterReconciler) buildDebugCommand(bareCmd string) string {
	if r.OperatorDeploymentName != "" && r.OperatorNamespace != "" {
		return fmt.Sprintf("kubectl exec -it deploy/%s -n %s -- %s", r.OperatorDeploymentName, r.OperatorNamespace, bareCmd)
	}
	return bareCmd
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
// On transient API errors it falls back to reconciling all clusters in the namespace
// so that events are not silently lost.
func (r *NatsClusterReconciler) mapUserToCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	log := logf.FromContext(ctx)
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
		if errors.IsNotFound(err) {
			return nil
		}
		// Transient error — fall back to reconciling all clusters in the namespace
		log.Error(err, "failed to resolve account for user, falling back to namespace-wide reconcile",
			"user", user.Name, "userNamespace", user.Namespace,
			"account", user.Spec.AccountRef.Name, "accountNamespace", accountNs)
		return r.allClustersInNamespace(ctx, accountNs)
	}

	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{
			Name:      account.Spec.ClusterRef.Name,
			Namespace: account.Namespace,
		}},
	}
}

// allClustersInNamespace returns reconcile requests for every NatsCluster in the
// given namespace. Used as a fallback when the exact cluster can't be resolved.
func (r *NatsClusterReconciler) allClustersInNamespace(ctx context.Context, namespace string) []reconcile.Request {
	clusterList := &natsv1alpha1.NatsClusterList{}
	if err := r.List(ctx, clusterList, client.InNamespace(namespace)); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list clusters for fallback reconcile", "namespace", namespace)
		return nil
	}
	requests := make([]reconcile.Request, 0, len(clusterList.Items))
	for _, c := range clusterList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: c.Name, Namespace: c.Namespace},
		})
	}
	return requests
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
