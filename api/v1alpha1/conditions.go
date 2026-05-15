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
	// ReasonSystemAccountConflict is set on every NatsAccount that has
	// spec.systemAccount=true when more than one account in the same cluster
	// is so flagged. The cluster runs without a system_account directive
	// until exactly one remains.
	ReasonSystemAccountConflict = "SystemAccountConflict"
)
