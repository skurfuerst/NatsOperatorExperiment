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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UserRuleAction specifies whether a matching rule grants or denies access.
// +kubebuilder:validation:Enum=grant;deny
type UserRuleAction string

const (
	UserRuleActionGrant UserRuleAction = "grant"
	UserRuleActionDeny  UserRuleAction = "deny"
)

// UserRule is a single ordered rule evaluated against a NatsUser's namespace.
// Exactly one of NamespaceRegex, NamespaceLabels, or SameNamespace must be set.
//
// +kubebuilder:validation:XValidation:rule="(has(self.namespaceRegex) && !has(self.namespaceLabels) && !has(self.sameNamespace)) || (!has(self.namespaceRegex) && has(self.namespaceLabels) && !has(self.sameNamespace)) || (!has(self.namespaceRegex) && !has(self.namespaceLabels) && has(self.sameNamespace))",message="exactly one of namespaceRegex, namespaceLabels, or sameNamespace must be set"
type UserRule struct {
	// Action is grant or deny.
	Action UserRuleAction `json:"action"`

	// NamespaceRegex is a Go regular expression matched against the user's namespace name.
	// +optional
	NamespaceRegex *string `json:"namespaceRegex,omitempty"`

	// NamespaceLabels matches namespaces whose labels contain all key/value pairs
	// in this map (equality matching).
	// +optional
	NamespaceLabels map[string]string `json:"namespaceLabels,omitempty"`

	// SameNamespace, when true, matches users that are in the same namespace as this NatsAccount.
	// +optional
	SameNamespace *bool `json:"sameNamespace,omitempty"`
}

// NatsAccountSpec defines the desired state of NatsAccount.
type NatsAccountSpec struct {
	// ClusterRef references the NatsCluster this account belongs to.
	// Must be in the same namespace as this NatsAccount.
	ClusterRef LocalObjectReference `json:"clusterRef"`

	// UserRules is an ordered list of rules controlling which NatsUsers can reference
	// this account. Rules are evaluated in order; the first matching rule's action applies.
	// If no rule matches, access is denied.
	// +optional
	UserRules []UserRule `json:"userRules,omitempty"`

	// JetStream configures JetStream resource limits for this account.
	// +optional
	JetStream *AccountJetStream `json:"jetstream,omitempty"`

	// Limits configures connection and resource limits for this account.
	// +optional
	Limits *AccountLimits `json:"limits,omitempty"`

	// SystemAccount, when true, designates this account as the NATS System
	// Account. Users in this account can subscribe to $SYS.> subjects (server
	// events, monitoring via `nats server *`, `nats top`, `nats account info`).
	// At most one NatsAccount per NatsCluster may set this to true; if multiple
	// are set, the alphabetically-first one wins (deterministic).
	// +optional
	SystemAccount bool `json:"systemAccount,omitempty"`
}

// LocalObjectReference references a resource in the same namespace.
type LocalObjectReference struct {
	// Name of the referenced resource.
	Name string `json:"name"`
}

// AccountJetStream configures JetStream limits for an account.
// These fields mirror the NATS server account-level JetStream configuration.
type AccountJetStream struct {
	// MaxMemory is the maximum in-memory storage for this account (e.g. "512Mi", "1Gi").
	// +optional
	MaxMemory *resource.Quantity `json:"maxMemory,omitempty"`

	// MaxFile is the maximum file/disk-based storage for this account (e.g. "1Gi", "10Gi").
	// +optional
	MaxFile *resource.Quantity `json:"maxFile,omitempty"`

	// MaxStreams is the maximum number of streams allowed.
	// +optional
	MaxStreams *int64 `json:"maxStreams,omitempty"`

	// MaxConsumers is the maximum number of consumers allowed.
	// +optional
	MaxConsumers *int64 `json:"maxConsumers,omitempty"`

	// MaxBytesRequired requires all streams to have max_bytes set when true.
	// +optional
	MaxBytesRequired *bool `json:"maxBytesRequired,omitempty"`

	// MemoryMaxStreamBytes is the per-stream memory storage ceiling.
	// +optional
	MemoryMaxStreamBytes *resource.Quantity `json:"memoryMaxStreamBytes,omitempty"`

	// DiskMaxStreamBytes is the per-stream disk storage ceiling.
	// +optional
	DiskMaxStreamBytes *resource.Quantity `json:"diskMaxStreamBytes,omitempty"`

	// MaxAckPending is the maximum number of pending ACKs per consumer.
	// +optional
	MaxAckPending *int64 `json:"maxAckPending,omitempty"`
}

// AccountLimits configures connection and resource limits for an account.
// These fields mirror the NATS server account-level limits configuration.
type AccountLimits struct {
	// MaxConnections is the maximum number of concurrent client connections.
	// +optional
	MaxConnections *int64 `json:"maxConnections,omitempty"`

	// MaxSubscriptions is the maximum number of subscriptions per connection.
	// +optional
	MaxSubscriptions *int64 `json:"maxSubscriptions,omitempty"`

	// MaxPayload is the maximum message payload size (e.g. "1Mi", "64Mi").
	// +optional
	MaxPayload *resource.Quantity `json:"maxPayload,omitempty"`

	// MaxLeafnodes is the maximum number of leaf node connections.
	// +optional
	MaxLeafnodes *int64 `json:"maxLeafnodes,omitempty"`
}

// NatsAccountStatus defines the observed state of NatsAccount.
type NatsAccountStatus struct {
	// Conditions represent the latest available observations of the account's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// UserCount is the number of NatsUsers in this account.
	// +optional
	UserCount int `json:"userCount,omitempty"`

	// DebugCommand is a full kubectl command to check this account's NATS connections.
	// Copy-paste it to debug connection issues.
	// +optional
	DebugCommand string `json:"debugCommand,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef.name`
// +kubebuilder:printcolumn:name="Users",type=integer,JSONPath=`.status.userCount`

// NatsAccount is the Schema for the natsaccounts API.
type NatsAccount struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NatsAccountSpec   `json:"spec,omitempty"`
	Status NatsAccountStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NatsAccountList contains a list of NatsAccount.
type NatsAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NatsAccount `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NatsAccount{}, &NatsAccountList{})
}
