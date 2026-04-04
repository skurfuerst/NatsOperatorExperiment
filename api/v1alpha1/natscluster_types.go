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

// NatsClusterSpec defines the desired state of NatsCluster.
type NatsClusterSpec struct {
	// ServerRef optionally references the Deployment or StatefulSet running NATS.
	// When set, the controller annotates the workload's pod template with the
	// config hash, triggering a rolling restart on config changes.
	// +optional
	ServerRef *WorkloadReference `json:"serverRef,omitempty"`
}

// WorkloadReference references a Deployment or StatefulSet in the same namespace as the NatsCluster.
type WorkloadReference struct {
	// Kind is the workload type.
	// +kubebuilder:validation:Enum=Deployment;StatefulSet
	Kind string `json:"kind"`

	// Name of the Deployment or StatefulSet.
	Name string `json:"name"`
}

// NatsClusterStatus defines the observed state of NatsCluster.
type NatsClusterStatus struct {
	// Conditions represent the latest available observations of the cluster's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// AccountCount is the number of NatsAccounts associated with this cluster.
	// +optional
	AccountCount int `json:"accountCount,omitempty"`

	// UserCount is the total number of NatsUsers across all accounts.
	// +optional
	UserCount int `json:"userCount,omitempty"`

	// LastConfigHash is the SHA256 hash of the last generated NATS config.
	// +optional
	LastConfigHash string `json:"lastConfigHash,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Accounts",type=integer,JSONPath=`.status.accountCount`
// +kubebuilder:printcolumn:name="Users",type=integer,JSONPath=`.status.userCount`

// NatsCluster is the Schema for the natsclusters API.
type NatsCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NatsClusterSpec   `json:"spec,omitempty"`
	Status NatsClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NatsClusterList contains a list of NatsCluster.
type NatsClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NatsCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NatsCluster{}, &NatsClusterList{})
}
