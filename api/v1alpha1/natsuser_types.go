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
