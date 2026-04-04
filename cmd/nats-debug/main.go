package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	natsv1alpha1 "github.com/skurfuerst/natsoperatorexperiment/api/v1alpha1"
	"github.com/skurfuerst/natsoperatorexperiment/internal/natsmonitor"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(natsv1alpha1.AddToScheme(scheme))
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "user-connections":
		if err := runUserConnections(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "account-connections":
		if err := runAccountConnections(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "connections":
		if err := runConnections(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: nats-debug <command> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  user-connections       Check connections for a specific user by nkey")
	fmt.Fprintln(os.Stderr, "  account-connections    Check connections for an account")
	fmt.Fprintln(os.Stderr, "  connections            Overview of all connections")
}

func parseFlags(args []string) (clusterName, namespace, nkey, account string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cluster":
			if i+1 >= len(args) {
				return "", "", "", "", fmt.Errorf("--cluster requires a value")
			}
			i++
			clusterName = args[i]
		case "--namespace":
			if i+1 >= len(args) {
				return "", "", "", "", fmt.Errorf("--namespace requires a value")
			}
			i++
			namespace = args[i]
		case "--nkey":
			if i+1 >= len(args) {
				return "", "", "", "", fmt.Errorf("--nkey requires a value")
			}
			i++
			nkey = args[i]
		case "--account":
			if i+1 >= len(args) {
				return "", "", "", "", fmt.Errorf("--account requires a value")
			}
			i++
			account = args[i]
		default:
			return "", "", "", "", fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	return clusterName, namespace, nkey, account, nil
}

func newClient() (client.Client, error) {
	config, err := ctrl.GetConfig()
	if err != nil {
		// Fallback to default kubeconfig
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		config, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("unable to get kubeconfig: %w", err)
		}
	}

	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}
	return c, nil
}

// discoverPodURLs reads the NatsCluster CR, gets the workload's pod selector,
// lists Running pods, and returns a map of podName -> monitoringURL.
func discoverPodURLs(ctx context.Context, c client.Client, clusterName, namespace string) (map[string]string, error) {
	cluster := &natsv1alpha1.NatsCluster{}
	if err := c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: namespace}, cluster); err != nil {
		return nil, fmt.Errorf("getting NatsCluster %s/%s: %w", namespace, clusterName, err)
	}

	if cluster.Spec.ServerRef == nil {
		return nil, fmt.Errorf("NatsCluster %s/%s has no serverRef configured", namespace, clusterName)
	}

	ref := cluster.Spec.ServerRef
	key := types.NamespacedName{Name: ref.Name, Namespace: namespace}

	var podSelector *metav1.LabelSelector
	switch ref.Kind {
	case "Deployment":
		deploy := &appsv1.Deployment{}
		if err := c.Get(ctx, key, deploy); err != nil {
			return nil, fmt.Errorf("getting deployment %s: %w", key, err)
		}
		podSelector = deploy.Spec.Selector
	case "StatefulSet":
		sts := &appsv1.StatefulSet{}
		if err := c.Get(ctx, key, sts); err != nil {
			return nil, fmt.Errorf("getting statefulset %s: %w", key, err)
		}
		podSelector = sts.Spec.Selector
	default:
		return nil, fmt.Errorf("unsupported workload kind: %s", ref.Kind)
	}

	selector, err := metav1.LabelSelectorAsSelector(podSelector)
	if err != nil {
		return nil, fmt.Errorf("parsing label selector: %w", err)
	}

	podList := &corev1.PodList{}
	listOpts := []client.ListOption{client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: selector}}
	if err := c.List(ctx, podList, listOpts...); err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	port := int32(8222)
	if cluster.Spec.MonitoringPort != nil {
		port = *cluster.Spec.MonitoringPort
	}

	urls := make(map[string]string)
	for _, pod := range podList.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		if pod.Status.PodIP == "" {
			continue
		}
		urls[pod.Name] = fmt.Sprintf("http://%s:%d", pod.Status.PodIP, port)
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("no running pods found for %s %s/%s", ref.Kind, namespace, ref.Name)
	}

	return urls, nil
}

func runUserConnections(args []string) error {
	clusterName, namespace, nkey, _, err := parseFlags(args)
	if err != nil {
		return err
	}
	if clusterName == "" || namespace == "" || nkey == "" {
		return fmt.Errorf("--cluster, --namespace, and --nkey are required")
	}

	ctx := context.Background()
	c, err := newClient()
	if err != nil {
		return err
	}

	urls, err := discoverPodURLs(ctx, c, clusterName, namespace)
	if err != nil {
		return err
	}

	monClient := natsmonitor.NewHTTPMonitorClient()
	allResults := natsmonitor.QueryAllServers(ctx, monClient, urls)
	filtered := natsmonitor.FilterByNKey(allResults, nkey)

	// Count total connections and errors
	totalConns := 0
	var errors []string
	for _, s := range filtered {
		if s.Error != nil {
			errors = append(errors, fmt.Sprintf("  %s: %v", s.ServerName, s.Error))
			continue
		}
		totalConns += len(s.Connections)
	}

	// Count servers queried
	serversQueried := len(urls)

	fmt.Printf("User: %s\n", nkey)
	if totalConns > 0 {
		// Count distinct servers with connections
		serverCount := 0
		for _, s := range filtered {
			if s.Error == nil && len(s.Connections) > 0 {
				serverCount++
			}
		}
		fmt.Printf("Status: CONNECTED (%d active connection(s) across %d server(s))\n", totalConns, serverCount)
		fmt.Println()

		connNum := 0
		for _, s := range sortedServers(filtered) {
			for _, conn := range s.Connections {
				connNum++
				fmt.Printf("  Connection #%d (server: %s):\n", connNum, s.ServerName)
				fmt.Printf("    CID:            %d\n", conn.CID)
				fmt.Printf("    Remote:         %s:%d\n", conn.IP, conn.Port)
				if conn.Uptime != "" {
					fmt.Printf("    Uptime:         %s\n", conn.Uptime)
				}
				if conn.RTT != "" {
					fmt.Printf("    RTT:            %s\n", conn.RTT)
				}
				fmt.Printf("    Subscriptions:  %d\n", conn.Subscriptions)
				fmt.Printf("    Msgs In/Out:    %d / %d\n", conn.InMsgs, conn.OutMsgs)
				fmt.Println()
			}
		}
	} else {
		fmt.Printf("Status: NOT CONNECTED (queried %d server(s), 0 connections found)\n", serversQueried)
		fmt.Println()
		runUserDiagnostics(ctx, c, monClient, urls, clusterName, namespace, nkey)
	}

	if len(errors) > 0 {
		fmt.Println("Server errors:")
		for _, e := range errors {
			fmt.Println(e)
		}
	}

	return nil
}

// runUserDiagnostics performs automated checks when a user has 0 active connections.
func runUserDiagnostics(
	ctx context.Context,
	c client.Client,
	monClient natsmonitor.MonitorClient,
	serverURLs map[string]string,
	clusterName, namespace, nkey string,
) {
	fmt.Println("Diagnostics:")
	fmt.Println()

	// Check 1: Recently closed connections for this NKey (auth failures show up here)
	checkClosedConnections(ctx, monClient, serverURLs, nkey)

	// Check 2: Is the NKey present in auth.conf?
	configMapName := clusterName + "-nats-config"
	cm := &corev1.ConfigMap{}
	cmErr := c.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: namespace}, cm)
	authConf := ""
	if cmErr == nil {
		authConf = cm.Data["auth.conf"]
	}

	nkeyInConfig := authConf != "" && strings.Contains(authConf, nkey)

	if cmErr != nil {
		fmt.Printf("  [?] Could not read ConfigMap %s: %v\n", configMapName, cmErr)
	} else if !nkeyInConfig {
		fmt.Printf("  [!] NKey %s NOT FOUND in auth.conf (ConfigMap %s)\n", nkey, configMapName)
		fmt.Println("      This user's NKey is not in the NATS server config.")
		fmt.Println("      Likely causes:")
		fmt.Println("        - User denied by account's userRules (namespace not allowed)")
		fmt.Println("        - NatsUser not yet reconciled")
		fmt.Println()
	} else {
		fmt.Printf("  [ok] NKey %s is present in auth.conf\n", nkey)
		fmt.Println()
	}

	// Check 3: Find the NatsUser CR and verify status
	checkNatsUserStatus(ctx, c, nkey, nkeyInConfig)

	// Check 4: Show configured permissions (if NKey is in config)
	if nkeyInConfig {
		showConfiguredPermissions(authConf, nkey)
	}

	// Check 5: Show helpful commands
	cluster := &natsv1alpha1.NatsCluster{}
	if err := c.Get(ctx, types.NamespacedName{
		Name: clusterName, Namespace: namespace,
	}, cluster); err == nil {
		showHelpfulCommands(cluster, configMapName, namespace)
	}
}

func checkClosedConnections(
	ctx context.Context,
	monClient natsmonitor.MonitorClient,
	serverURLs map[string]string,
	nkey string,
) {
	closedResults := natsmonitor.QueryAllServersClosed(ctx, monClient, serverURLs, 100)
	closedFiltered := natsmonitor.FilterByNKey(closedResults, nkey)

	// Also look for auth violations without NKey match (failed auth won't have NKey set)
	var authViolations []closedConnSummary
	var recentClosed []closedConnSummary

	for _, s := range closedResults {
		if s.Error != nil {
			continue
		}
		for _, conn := range s.Connections {
			if conn.NKey == nkey {
				recentClosed = append(recentClosed, closedConnSummary{
					server: s.ServerName,
					conn:   conn,
				})
			}
			if conn.Reason == "Authorization Violation" {
				authViolations = append(authViolations, closedConnSummary{
					server: s.ServerName,
					conn:   conn,
				})
			}
		}
	}

	// Also count NKey-specific closed from the filtered results
	nkeyClosedCount := 0
	for _, s := range closedFiltered {
		if s.Error == nil {
			nkeyClosedCount += len(s.Connections)
		}
	}

	if len(recentClosed) > 0 {
		fmt.Printf("  [!] Found %d recently closed connection(s) for this NKey:\n", len(recentClosed))
		// Show most recent (up to 5)
		shown := 0
		for i := len(recentClosed) - 1; i >= 0 && shown < 5; i-- {
			c := recentClosed[i]
			fmt.Printf("      Server: %s | Reason: %s | IP: %s:%d",
				c.server, reasonOrUnknown(c.conn.Reason), c.conn.IP, c.conn.Port)
			if c.conn.Stop != "" {
				fmt.Printf(" | Closed: %s", c.conn.Stop)
			}
			fmt.Println()
			shown++
		}
		fmt.Println()
	}

	if len(authViolations) > 0 {
		fmt.Printf("  [!] Found %d recent \"Authorization Violation\" across all users:\n", len(authViolations))
		shown := 0
		for i := len(authViolations) - 1; i >= 0 && shown < 5; i-- {
			c := authViolations[i]
			nkeyInfo := ""
			if c.conn.NKey != "" {
				nkeyInfo = fmt.Sprintf(" | NKey: %s", c.conn.NKey)
			}
			fmt.Printf("      Server: %s | IP: %s:%d%s",
				c.server, c.conn.IP, c.conn.Port, nkeyInfo)
			if c.conn.Stop != "" {
				fmt.Printf(" | Closed: %s", c.conn.Stop)
			}
			fmt.Println()
			shown++
		}
		fmt.Println()
	}

	if len(recentClosed) == 0 && len(authViolations) == 0 {
		fmt.Println("  [ok] No recently closed connections or auth violations found")
		fmt.Println("       (client may not have attempted to connect yet)")
		fmt.Println()
	}
}

type closedConnSummary struct {
	server string
	conn   natsmonitor.ConnectionInfo
}

func reasonOrUnknown(reason string) string {
	if reason == "" {
		return "(unknown)"
	}
	return reason
}

func checkNatsUserStatus(ctx context.Context, c client.Client, nkey string, nkeyInConfig bool) {
	// List NatsUsers to find the one matching this NKey
	userList := &natsv1alpha1.NatsUserList{}
	if err := c.List(ctx, userList); err != nil {
		fmt.Printf("  [?] Could not list NatsUsers: %v\n\n", err)
		return
	}

	var matchingUser *natsv1alpha1.NatsUser
	for i := range userList.Items {
		if userList.Items[i].Status.NKeyPublicKey == nkey {
			matchingUser = &userList.Items[i]
			break
		}
	}

	if matchingUser == nil {
		fmt.Printf("  [!] No NatsUser found with NKey %s\n", nkey)
		fmt.Println("      The user CR may have been deleted or not yet reconciled.")
		fmt.Println()
		return
	}

	fmt.Printf("  NatsUser: %s/%s\n", matchingUser.Namespace, matchingUser.Name)
	fmt.Printf("  Account:  %s/%s\n", matchingUser.Spec.AccountRef.Namespace, matchingUser.Spec.AccountRef.Name)

	// Show Ready condition
	for _, cond := range matchingUser.Status.Conditions {
		if cond.Type == natsv1alpha1.ConditionReady {
			if cond.Status == "True" {
				fmt.Printf("  Ready:    True (%s)\n", cond.Message)
			} else {
				fmt.Printf("  [!] Ready: %s | Reason: %s | %s\n", cond.Status, cond.Reason, cond.Message)
			}
			break
		}
	}

	// Check secret exists
	if matchingUser.Status.SecretRef != nil {
		secretName := matchingUser.Status.SecretRef.Name
		secret := &corev1.Secret{}
		if err := c.Get(ctx, types.NamespacedName{
			Name: secretName, Namespace: matchingUser.Namespace,
		}, secret); err != nil {
			fmt.Printf("  [!] Secret %s not found: %v\n", secretName, err)
		} else {
			storedPubKey := string(secret.Data["nkey-public"])
			if storedPubKey != nkey {
				fmt.Printf("  [!] NKey MISMATCH: Secret has %s, expected %s\n", storedPubKey, nkey)
				fmt.Println("      The secret was likely regenerated. Restart the client to pick up the new seed.")
			} else {
				fmt.Printf("  Secret:   %s/%s (nkey-public matches)\n", matchingUser.Namespace, secretName)
			}
		}
	}
	fmt.Println()

	// Check account limits
	if !nkeyInConfig {
		return // no point checking limits if user isn't in config
	}
	acctRef := matchingUser.Spec.AccountRef
	acct := &natsv1alpha1.NatsAccount{}
	if err := c.Get(ctx, types.NamespacedName{
		Name: acctRef.Name, Namespace: acctRef.Namespace,
	}, acct); err == nil && acct.Spec.Limits != nil && acct.Spec.Limits.MaxConnections != nil {
		maxConn := *acct.Spec.Limits.MaxConnections
		fmt.Printf("  Account limit: maxConnections=%d (check account-connections to see current count)\n", maxConn)
		fmt.Println()
	}
}

// showConfiguredPermissions extracts and displays the permissions block for a specific NKey
// from the raw auth.conf text.
func showConfiguredPermissions(authConf, nkey string) {
	// Find the nkey line and extract the surrounding user block
	lines := strings.Split(authConf, "\n")
	nkeyLineIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "nkey: "+nkey) {
			nkeyLineIdx = i
			break
		}
	}
	if nkeyLineIdx < 0 {
		return
	}

	// Walk backwards to find the user block opening "{"
	startIdx := nkeyLineIdx
	for startIdx > 0 && !strings.Contains(lines[startIdx], "{") {
		startIdx--
	}

	// Walk forward to find the matching closing "}"
	depth := 0
	endIdx := startIdx
	for endIdx < len(lines) {
		depth += strings.Count(lines[endIdx], "{") - strings.Count(lines[endIdx], "}")
		if depth <= 0 {
			break
		}
		endIdx++
	}

	fmt.Println("  Configured permissions (from auth.conf):")
	for i := startIdx; i <= endIdx && i < len(lines); i++ {
		fmt.Printf("    %s\n", strings.TrimRight(lines[i], " "))
	}
	fmt.Println()
}

func showHelpfulCommands(cluster *natsv1alpha1.NatsCluster, configMapName, namespace string) {
	fmt.Println("  Useful commands:")
	fmt.Printf("    View auth config:  kubectl get configmap %s -n %s -o jsonpath='{.data.auth\\.conf}'\n",
		configMapName, namespace)

	if cluster.Spec.ServerRef != nil {
		ref := cluster.Spec.ServerRef
		workload := strings.ToLower(ref.Kind)
		fmt.Printf("    View NATS logs:    kubectl logs %s/%s -n %s --tail=50 | grep -i auth\n",
			workload, ref.Name, namespace)
	}
	fmt.Println()
}

func runAccountConnections(args []string) error {
	clusterName, namespace, _, account, err := parseFlags(args)
	if err != nil {
		return err
	}
	if clusterName == "" || namespace == "" || account == "" {
		return fmt.Errorf("--cluster, --namespace, and --account are required")
	}

	ctx := context.Background()
	c, err := newClient()
	if err != nil {
		return err
	}

	urls, err := discoverPodURLs(ctx, c, clusterName, namespace)
	if err != nil {
		return err
	}

	monClient := natsmonitor.NewHTTPMonitorClient()
	allResults := natsmonitor.QueryAllServers(ctx, monClient, urls)
	filtered := natsmonitor.FilterByAccount(allResults, account)

	totalConns := 0
	userConns := make(map[string][]string) // nkey -> list of server names
	for _, s := range filtered {
		if s.Error != nil {
			continue
		}
		for _, conn := range s.Connections {
			totalConns++
			userConns[conn.NKey] = append(userConns[conn.NKey], s.ServerName)
		}
	}

	fmt.Printf("Account: %s\n", account)
	if totalConns > 0 {
		// Count distinct servers
		serverSet := make(map[string]bool)
		for _, s := range filtered {
			if s.Error == nil && len(s.Connections) > 0 {
				serverSet[s.ServerName] = true
			}
		}
		fmt.Printf("Active Connections: %d (across %d user(s), %d server(s))\n", totalConns, len(userConns), len(serverSet))
		fmt.Println()

		// Sort users by nkey for deterministic output
		var keys []string
		for k := range userConns {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, nkey := range keys {
			servers := userConns[nkey]
			fmt.Printf("  User %s : %d connection(s) (%s)\n", nkey, len(servers), strings.Join(uniqueSorted(servers), ", "))
		}
	} else {
		fmt.Printf("Active Connections: 0 (queried %d server(s))\n", len(urls))
		fmt.Println()
		runAccountDiagnostics(ctx, c, monClient, urls, clusterName, namespace, account)
	}

	return nil
}

// runAccountDiagnostics performs checks when an account has 0 active connections.
func runAccountDiagnostics(
	ctx context.Context,
	c client.Client,
	monClient natsmonitor.MonitorClient,
	serverURLs map[string]string,
	clusterName, namespace, account string,
) {
	fmt.Println("Diagnostics:")
	fmt.Println()

	// Check auth violations in closed connections
	closedResults := natsmonitor.QueryAllServersClosed(ctx, monClient, serverURLs, 100)
	var authViolations int
	for _, s := range closedResults {
		if s.Error != nil {
			continue
		}
		for _, conn := range s.Connections {
			if conn.Reason == "Authorization Violation" {
				authViolations++
			}
		}
	}
	if authViolations > 0 {
		fmt.Printf("  [!] Found %d recent \"Authorization Violation\" in closed connections\n\n", authViolations)
	}

	// Check if account exists in auth.conf
	configMapName := clusterName + "-nats-config"
	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: namespace}, cm); err != nil {
		fmt.Printf("  [?] Could not read ConfigMap %s: %v\n\n", configMapName, err)
	} else {
		authConf := cm.Data["auth.conf"]
		if !strings.Contains(authConf, account+" {") {
			fmt.Printf("  [!] Account %q NOT FOUND in auth.conf\n", account)
			fmt.Println("      The account may not have any allowed users, or may not reference this cluster.")
			fmt.Println()
		} else {
			fmt.Printf("  [ok] Account %q is present in auth.conf\n\n", account)
		}
	}

	// Check account limits
	acct := &natsv1alpha1.NatsAccount{}
	if err := c.Get(ctx, types.NamespacedName{
		Name: account, Namespace: namespace,
	}, acct); err == nil {
		if acct.Spec.Limits != nil && acct.Spec.Limits.MaxConnections != nil {
			fmt.Printf("  Account limit: maxConnections=%d\n\n", *acct.Spec.Limits.MaxConnections)
		}
		fmt.Printf("  Configured users: %d\n\n", acct.Status.UserCount)
	}

	// Helpful commands
	cluster := &natsv1alpha1.NatsCluster{}
	if err := c.Get(ctx, types.NamespacedName{
		Name: clusterName, Namespace: namespace,
	}, cluster); err == nil {
		showHelpfulCommands(cluster, configMapName, namespace)
	}
}

func runConnections(args []string) error {
	clusterName, namespace, _, _, err := parseFlags(args)
	if err != nil {
		return err
	}
	if clusterName == "" || namespace == "" {
		return fmt.Errorf("--cluster and --namespace are required")
	}

	ctx := context.Background()
	c, err := newClient()
	if err != nil {
		return err
	}

	urls, err := discoverPodURLs(ctx, c, clusterName, namespace)
	if err != nil {
		return err
	}

	monClient := natsmonitor.NewHTTPMonitorClient()
	allResults := natsmonitor.QueryAllServers(ctx, monClient, urls)

	fmt.Printf("Cluster: %s/%s\n", namespace, clusterName)
	fmt.Printf("Servers queried: %d\n\n", len(urls))

	totalConns := 0
	for _, s := range sortedServers(allResults) {
		if s.Error != nil {
			fmt.Printf("  %s: ERROR - %v\n", s.ServerName, s.Error)
			continue
		}
		fmt.Printf("  %s: %d connection(s)\n", s.ServerName, len(s.Connections))
		totalConns += len(s.Connections)
	}
	fmt.Printf("\nTotal: %d connection(s)\n", totalConns)

	return nil
}

func sortedServers(servers []natsmonitor.ServerConnections) []natsmonitor.ServerConnections {
	sorted := make([]natsmonitor.ServerConnections, len(servers))
	copy(sorted, servers)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ServerName < sorted[j].ServerName
	})
	return sorted
}

func uniqueSorted(items []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	sort.Strings(result)
	return result
}
