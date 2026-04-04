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
	"encoding/json"

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
	// Accepts a boolean or an object:
	//   - true           → emit "allow_responses: true"
	//   - false          → do not emit allow_responses
	//   - {}             → emit "allow_responses: true" (object boolean form)
	//   - {maxMsgs: N}   → emit structured form with max limit
	//   - {ttl: "5m"}   → emit structured form with time limit
	// +optional
	AllowResponses *AllowResponsesSpec `json:"allowResponses,omitempty"`
}

// AllowResponsesSpec configures NATS allow_responses for request-reply patterns.
// Supports both a boolean form (true/false) and an object form ({maxMsgs, ttl}).
//
// +kubebuilder:object:generate=false
type AllowResponsesSpec struct {
	// boolValue holds the boolean value when the boolean form (true/false) is used.
	// json:"-" because custom UnmarshalJSON/MarshalJSON handle serialization entirely.
	boolValue *bool `json:"-"`

	// MaxMsgs is the maximum number of response messages allowed (object form).
	// +optional
	MaxMsgs *int `json:"maxMsgs,omitempty"`

	// TTL is the time window for responses, e.g. "5m", "30s" (object form).
	// +optional
	TTL *string `json:"ttl,omitempty"`
}

// UnmarshalJSON implements json.Unmarshaler, accepting both bool and object forms.
func (a *AllowResponsesSpec) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		a.boolValue = &b
		a.MaxMsgs = nil
		a.TTL = nil
		return nil
	}
	type alias struct {
		MaxMsgs *int    `json:"maxMsgs,omitempty"`
		TTL     *string `json:"ttl,omitempty"`
	}
	var obj alias
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	a.boolValue = nil
	a.MaxMsgs = obj.MaxMsgs
	a.TTL = obj.TTL
	return nil
}

// MarshalJSON implements json.Marshaler, emitting bool or object form.
func (a AllowResponsesSpec) MarshalJSON() ([]byte, error) {
	if a.boolValue != nil {
		return json.Marshal(*a.boolValue)
	}
	type alias struct {
		MaxMsgs *int    `json:"maxMsgs,omitempty"`
		TTL     *string `json:"ttl,omitempty"`
	}
	return json.Marshal(alias{MaxMsgs: a.MaxMsgs, TTL: a.TTL})
}

// NewAllowResponsesBool creates an AllowResponsesSpec from a boolean value.
// true emits "allow_responses: true"; false suppresses the directive entirely.
func NewAllowResponsesBool(enabled bool) *AllowResponsesSpec {
	return &AllowResponsesSpec{boolValue: &enabled}
}

// ShouldEmit returns false when the boolean false form was used; true otherwise.
func (a *AllowResponsesSpec) ShouldEmit() bool {
	if a.boolValue != nil {
		return *a.boolValue
	}
	return true
}

// DeepCopyInto copies all fields including the unexported boolValue.
func (in *AllowResponsesSpec) DeepCopyInto(out *AllowResponsesSpec) {
	*out = *in
	if in.boolValue != nil {
		b := *in.boolValue
		out.boolValue = &b
	}
	if in.MaxMsgs != nil {
		m := *in.MaxMsgs
		out.MaxMsgs = &m
	}
	if in.TTL != nil {
		s := *in.TTL
		out.TTL = &s
	}
}

// DeepCopy creates a deep copy of AllowResponsesSpec.
func (in *AllowResponsesSpec) DeepCopy() *AllowResponsesSpec {
	if in == nil {
		return nil
	}
	out := new(AllowResponsesSpec)
	in.DeepCopyInto(out)
	return out
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

	// DebugCommand is a full kubectl command to check this user's NATS connections.
	// Copy-paste it to debug connection issues.
	// +optional
	DebugCommand string `json:"debugCommand,omitempty"`
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
