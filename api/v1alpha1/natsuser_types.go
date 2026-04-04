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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NatsUserSpec defines the desired state of NatsUser.
type NatsUserSpec struct {
	// AccountRef references the NatsAccount this user belongs to.
	// Can be cross-namespace if the NatsAccount's allowedUserNamespaces permits it.
	AccountRef NamespacedObjectReference `json:"accountRef"`

	// InboxPrefix overrides the auto-generated inbox prefix for this user.
	// By default the operator generates a random prefix (e.g. "_I_ABCDE3FG4H5I6J7") and
	// stores it in the user's Secret under key "inbox-prefix".
	// Set this only if you need a stable, human-readable prefix instead.
	// Has no effect when InsecureSharedInboxPrefix is true.
	// The NATS client must use the same prefix:
	//   nats.CustomInboxPrefix("<prefix>") in Go, or --inbox-prefix <prefix> in CLI.
	// +optional
	InboxPrefix *string `json:"inboxPrefix,omitempty"`

	// InsecureSharedInboxPrefix disables inbox isolation for this user.
	// When false (default), the operator auto-generates a unique inbox prefix so that
	// other users in the same account cannot intercept this user's request-reply responses.
	// Set to true only if you fully trust all other users in the account or do not use
	// request-reply patterns.
	// WARNING: setting this to true means any account-member with subscribe access to
	// _INBOX.> can receive responses intended for this user.
	// +optional
	InsecureSharedInboxPrefix bool `json:"insecureSharedInboxPrefix,omitempty"`

	// Permissions defines publish/subscribe permissions for this user.
	// +optional
	Permissions *Permissions `json:"permissions,omitempty"`
}

// NamespacedObjectReference references a resource, optionally in another namespace.
type NamespacedObjectReference struct {
	// Name of the referenced resource.
	Name string `json:"name"`

	// Namespace of the referenced resource.
	// If empty, defaults to the referring resource's namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// Permissions defines NATS publish/subscribe permissions.
type Permissions struct {
	// Publish defines publish permissions.
	// +optional
	Publish *PermissionRule `json:"publish,omitempty"`

	// Subscribe defines subscribe permissions.
	// +optional
	Subscribe *PermissionRule `json:"subscribe,omitempty"`

	// AllowResponses enables or configures response permissions for request-reply.
	// If set with no fields, emits "allow_responses: true".
	// If MaxMsgs or TTL is set, emits the structured form.
	// +optional
	AllowResponses *ResponsePermission `json:"allowResponses,omitempty"`
}

// ResponsePermission configures NATS allow_responses for request-reply patterns.
type ResponsePermission struct {
	// MaxMsgs is the maximum number of response messages allowed.
	// +optional
	MaxMsgs *int `json:"maxMsgs,omitempty"`

	// TTL is the time window for responses (e.g., "5m", "30s").
	// +optional
	TTL *string `json:"ttl,omitempty"`
}

// PermissionRule defines allowed and denied subjects.
type PermissionRule struct {
	// Allow is a list of subjects to allow.
	// +optional
	Allow []string `json:"allow,omitempty"`

	// Deny is a list of subjects to deny.
	// +optional
	Deny []string `json:"deny,omitempty"`
}

// NatsUserStatus defines the observed state of NatsUser.
type NatsUserStatus struct {
	// Conditions represent the latest available observations of the user's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// NKeyPublicKey is the user's NKey public key (starts with 'U').
	// +optional
	NKeyPublicKey string `json:"nkeyPublicKey,omitempty"`

	// SecretRef references the Secret containing the user's NKey seed and public key.
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`
}

// SecretReference references a Secret by name in the same namespace.
type SecretReference struct {
	// Name of the Secret.
	Name string `json:"name"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Account",type=string,JSONPath=`.spec.accountRef.name`
// +kubebuilder:printcolumn:name="PublicKey",type=string,JSONPath=`.status.nkeyPublicKey`

// NatsUser is the Schema for the natsusers API.
type NatsUser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NatsUserSpec   `json:"spec,omitempty"`
	Status NatsUserStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NatsUserList contains a list of NatsUser.
type NatsUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NatsUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NatsUser{}, &NatsUserList{})
}
