package v1alpha1

// Condition type constants for NATS operator resources.
const (
	// ConditionReady indicates whether the resource has been successfully reconciled.
	ConditionReady = "Ready"
)

// Condition reason constants.
const (
	ReasonReconciled          = "Reconciled"
	ReasonReconcileError      = "ReconcileError"
	ReasonInvalidRegex        = "InvalidRegex"
	ReasonClusterNotFound     = "ClusterNotFound"
	ReasonAccountNotFound     = "AccountNotFound"
	ReasonNamespaceNotAllowed = "NamespaceNotAllowed"
	ReasonNamespaceFetchError = "NamespaceFetchError"
)
