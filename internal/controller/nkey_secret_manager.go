package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	natsv1alpha1 "github.com/skurfuerst/natsoperatorexperiment/api/v1alpha1"
	nkeysutil "github.com/skurfuerst/natsoperatorexperiment/internal/nkeys"
)

// NKeySecretManager manages NKey secrets for NatsUsers.
// It ensures a Secret with NKey seed/public key (and inbox prefix) exists for each user.
type NKeySecretManager struct {
	client.Client
	Scheme *runtime.Scheme
}

// EnsureNKeySecret ensures a Secret with NKey seed/public key (and inbox prefix) exists for
// the user. Returns the public key and resolved inbox prefix (empty if isolation is disabled).
//
// Inbox prefix logic (secure by default):
//   - InsecureSharedInboxPrefix == true: no inbox isolation; empty prefix returned
//   - InboxPrefix set to non-empty:      use provided prefix, store in Secret
//   - otherwise (default):               auto-generate random prefix, store in Secret
func (m *NKeySecretManager) EnsureNKeySecret(ctx context.Context, user *natsv1alpha1.NatsUser) (publicKey, inboxPrefix string, err error) {
	secretName := user.Name + "-nats-nkey"
	secret := &corev1.Secret{}
	getErr := m.Get(ctx, types.NamespacedName{Name: secretName, Namespace: user.Namespace}, secret)

	if getErr == nil {
		// Secret exists — read existing values
		publicKey = string(secret.Data["nkey-public"])
		inboxPrefix = string(secret.Data["inbox-prefix"])

		needsUpdate := false
		if user.Spec.InsecureSharedInboxPrefix {
			// Isolation explicitly disabled — remove prefix if it was previously set
			if _, hadPrefix := secret.Data["inbox-prefix"]; hadPrefix {
				delete(secret.Data, "inbox-prefix")
				inboxPrefix = ""
				needsUpdate = true
			}
		} else if inboxPrefix == "" {
			// Isolation wanted but prefix not yet in the secret — add it now.
			// (Handles upgrade from pre-isolation secrets.)
			inboxPrefix, err = resolveInboxPrefix(user.Spec.InboxPrefix)
			if err != nil {
				return "", "", err
			}
			secret.Data["inbox-prefix"] = []byte(inboxPrefix)
			needsUpdate = true
		}
		if needsUpdate {
			if err := m.Update(ctx, secret); err != nil {
				return "", "", err
			}
		}

		// Update user status if needed
		if user.Status.NKeyPublicKey != publicKey || user.Status.SecretRef == nil || user.Status.SecretRef.Name != secretName {
			user.Status.NKeyPublicKey = publicKey
			user.Status.SecretRef = &natsv1alpha1.SecretReference{Name: secretName}
			if err := m.Status().Update(ctx, user); err != nil {
				return "", "", err
			}
		}

		return publicKey, inboxPrefix, nil
	}

	if !errors.IsNotFound(getErr) {
		return "", "", getErr
	}

	// Generate new NKey
	publicKey, seed, err := nkeysutil.GenerateUserNKey()
	if err != nil {
		return "", "", fmt.Errorf("generating nkey: %w", err)
	}

	secretData := map[string][]byte{
		"nkey-seed":   seed,
		"nkey-public": []byte(publicKey),
	}

	// Resolve and store inbox prefix unless isolation is explicitly disabled
	if !user.Spec.InsecureSharedInboxPrefix {
		inboxPrefix, err = resolveInboxPrefix(user.Spec.InboxPrefix)
		if err != nil {
			return "", "", err
		}
		secretData["inbox-prefix"] = []byte(inboxPrefix)
	}

	// Create Secret
	secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: user.Namespace,
		},
		Data: secretData,
	}

	if err := controllerutil.SetOwnerReference(user, secret, m.Scheme); err != nil {
		return "", "", err
	}

	if err := m.Create(ctx, secret); err != nil {
		return "", "", err
	}

	// Update user status
	user.Status.NKeyPublicKey = publicKey
	user.Status.SecretRef = &natsv1alpha1.SecretReference{Name: secretName}
	if err := m.Status().Update(ctx, user); err != nil {
		return "", "", err
	}

	return publicKey, inboxPrefix, nil
}

// resolveInboxPrefix returns the inbox prefix to use.
// If a non-empty override is provided, it is used directly.
// Otherwise a random prefix is auto-generated.
func resolveInboxPrefix(override *string) (string, error) {
	if override != nil && *override != "" {
		return *override, nil
	}
	return nkeysutil.GenerateInboxPrefix()
}
