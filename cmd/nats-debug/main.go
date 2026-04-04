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
		fmt.Println("Troubleshooting hints:")
		fmt.Println("  - Verify the client is using the correct nkey seed")
		fmt.Println("  - Check client logs for connection errors")
		fmt.Println("  - Verify network connectivity to the NATS server")
	}

	if len(errors) > 0 {
		fmt.Println("Server errors:")
		for _, e := range errors {
			fmt.Println(e)
		}
	}

	return nil
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
	}

	return nil
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
